package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

const defaultMaxBodyBytes int64 = 1024 * 1024

// WebRequest is the protocol snapshot an Authenticator or Tool may inspect.
// It deliberately contains request facts, never a Controller or Tool ref.
// Headers use lower-case names and Query preserves repeated values.
type WebRequest struct {
	Method  string
	Path    string
	Headers map[string]string
	Query   map[string][]string
}

// Authenticator supplies the immutable principal for one HTTP request. A nil
// result rejects the request without choosing application authorization policy.
type Authenticator func(context.Context, WebRequest) *contexture.Principal

// RejectedError is an intentional business rejection returned by a Tool. It
// becomes a 422 rejected REST problem without exposing an internal failure.
type RejectedError struct{ Detail string }

// Error reports the client-safe rejection detail.
func (err *RejectedError) Error() string {
	if err == nil || err.Detail == "" {
		return "Request was rejected."
	}
	return err.Detail
}

// Reject marks a Tool outcome as a client-correctable business rejection.
func Reject(detail string) error { return &RejectedError{Detail: detail} }

// RestRouterOptions configures optional HTTP-boundary behavior.
type RestRouterOptions struct {
	Authenticator Authenticator
	// MaxBodyBytes limits JSON command bodies. Zero uses the 1 MiB default.
	MaxBodyBytes int64
}

type webRequestKey struct{}

// CurrentRequest returns the HTTP request facts for a Tool invocation. The
// returned snapshot is defensively copied, so a Tool cannot mutate another
// component's view of the request.
func CurrentRequest(ctx context.Context) (WebRequest, bool) {
	request, ok := ctx.Value(webRequestKey{}).(WebRequest)
	if !ok {
		return WebRequest{}, false
	}
	return cloneWebRequest(request), true
}

// RestRouter is an allowlisted net/http adapter over Runtime Bindings.
// It never accepts a caller-supplied Tool ref or principal.
type RestRouter struct {
	runtime       *contexture.Runtime
	routes        map[string]RestRoute
	order         []string
	authenticator Authenticator
	maxBodyBytes  int64
}

// RestSurface is the native net/http name for an explicit REST publication.
// It is an alias so existing RestRouter users retain the same behavior.
type RestSurface = RestRouter

// NewRestRouter validates every explicit route before it can serve requests.
func NewRestRouter(runtime *contexture.Runtime, routes []RestRoute) (*RestRouter, error) {
	return NewRestRouterWithOptions(runtime, routes, RestRouterOptions{})
}

// NewRestSurface builds one explicit net/http REST publication.
func NewRestSurface(runtime *contexture.Runtime, routes []RestRoute, options RestRouterOptions) (*RestSurface, error) {
	return NewRestRouterWithOptions(runtime, routes, options)
}

// NewRestRouterWithOptions validates every route and HTTP boundary option
// before the router can serve requests.
func NewRestRouterWithOptions(runtime *contexture.Runtime, routes []RestRoute, options RestRouterOptions) (*RestRouter, error) {
	if runtime == nil {
		return nil, errors.New("Contexture Runtime must not be nil")
	}
	if options.MaxBodyBytes < 0 {
		return nil, errors.New("Contexture REST max body bytes must be non-negative")
	}
	maxBodyBytes := options.MaxBodyBytes
	if maxBodyBytes == 0 {
		maxBodyBytes = defaultMaxBodyBytes
	}
	router := &RestRouter{
		runtime: runtime, routes: map[string]RestRoute{},
		authenticator: options.Authenticator, maxBodyBytes: maxBodyBytes,
	}
	for _, route := range routes {
		var err error
		route, err = normalizeRestRoute(route)
		if err != nil {
			return nil, err
		}
		tool, err := runtime.Tool(route.Ref)
		if err != nil {
			return nil, err
		}
		readMethod := route.Method == http.MethodGet || route.Method == http.MethodHead
		if readMethod != tool.ReadOnly {
			return nil, errors.New("A Contexture REST route method must match its Tool read-only door.")
		}
		key := routeKey(route.Method, route.Path)
		if _, exists := router.routes[key]; exists {
			return nil, errors.New("A Contexture REST route is declared more than once.")
		}
		router.routes[key] = route
		router.order = append(router.order, key)
	}
	return router, nil
}

// Routes returns a defensive copy of the explicit route allowlist.
func (router *RestRouter) Routes() []RestRoute {
	result := make([]RestRoute, 0, len(router.order))
	for _, key := range router.order {
		result = append(result, router.routes[key])
	}
	return result
}

// ServeHTTP serves only exact allowlisted method/path pairs.
func (router *RestRouter) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	method := strings.ToUpper(request.Method)
	route, exists := router.routes[routeKey(method, request.URL.Path)]
	if !exists && method == http.MethodHead {
		route, exists = router.routes[routeKey(http.MethodGet, request.URL.Path)]
	}
	if !exists {
		problem(writer, http.StatusNotFound, "route-not-found", "No REST route is published here.")
		return
	}
	webRequest := newWebRequest(request)
	if router.authenticator != nil {
		principal := router.authenticator(request.Context(), cloneWebRequest(webRequest))
		if principal == nil {
			problem(writer, http.StatusUnauthorized, "unauthenticated", "Authentication is required.")
			return
		}
		request = request.WithContext(contexture.WithPrincipal(request.Context(), principal))
	}
	request = request.WithContext(context.WithValue(request.Context(), webRequestKey{}, webRequest))
	arguments, requestError := router.requestArguments(writer, request)
	if requestError != nil {
		problem(writer, requestError.status, requestError.kind, requestError.detail)
		return
	}
	var value any
	var err error
	if route.Method == http.MethodGet || route.Method == http.MethodHead {
		value, err = router.runtime.InvokeReadOnly(request.Context(), route.Ref, arguments, contexture.AllRoots())
	} else {
		value, err = router.runtime.Invoke(request.Context(), route.Ref, arguments, contexture.AllRoots())
	}
	if err != nil {
		router.invokeProblem(writer, err)
		return
	}
	jsonResponse(writer, route.Status, value, method == http.MethodHead)
}

// Serve holds the Runtime's Channels open for the entire HTTP serving scope.
// Call it around http.Server.Serve (or an equivalent Host loop), not per
// request, so application dependencies have one coherent lifetime.
func (router *RestRouter) Serve(ctx context.Context, serve func(context.Context, http.Handler) error) error {
	if serve == nil {
		return errors.New("Contexture REST serving function must not be nil")
	}
	return router.runtime.Serve(ctx, func(ctx context.Context) error { return serve(ctx, router) })
}

type requestFailure struct {
	status int
	kind   string
	detail string
}

func (router *RestRouter) requestArguments(writer http.ResponseWriter, request *http.Request) (json.RawMessage, *requestFailure) {
	method := strings.ToUpper(request.Method)
	if method == http.MethodGet || method == http.MethodHead {
		arguments := make(map[string]any, len(request.URL.Query()))
		for key, values := range request.URL.Query() {
			if len(values) == 1 {
				arguments[key] = values[0]
			} else {
				arguments[key] = append([]string(nil), values...)
			}
		}
		body, err := json.Marshal(arguments)
		if err != nil {
			return nil, &requestFailure{status: http.StatusInternalServerError, kind: "controller-failed", detail: typeName(err)}
		}
		return body, nil
	}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(request.Header.Get("Content-Type"), ";")[0]))
	if contentType != "" && contentType != "application/json" {
		return nil, &requestFailure{status: http.StatusUnsupportedMediaType, kind: "unsupported-media-type", detail: "REST commands accept application/json."}
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, router.maxBodyBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return nil, &requestFailure{status: http.StatusRequestEntityTooLarge, kind: "body-too-large", detail: "Request body exceeds the configured limit."}
		}
		return nil, &requestFailure{status: http.StatusBadRequest, kind: "invalid-json", detail: "Request body is not valid JSON."}
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return json.RawMessage("{}"), nil
	}
	var value any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, &requestFailure{status: http.StatusBadRequest, kind: "invalid-json", detail: "Request body is not valid JSON."}
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, &requestFailure{status: http.StatusBadRequest, kind: "invalid-body", detail: "Request body must be a JSON object."}
	}
	return json.RawMessage(body), nil
}

func routeKey(method, path string) string { return strings.ToUpper(method) + " " + path }

func newWebRequest(request *http.Request) WebRequest {
	headers := make(map[string]string, len(request.Header))
	for key, values := range request.Header {
		if len(values) > 0 {
			headers[strings.ToLower(key)] = values[len(values)-1]
		}
	}
	query := make(map[string][]string, len(request.URL.Query()))
	for key, values := range request.URL.Query() {
		query[key] = append([]string(nil), values...)
	}
	return WebRequest{Method: strings.ToUpper(request.Method), Path: request.URL.Path, Headers: headers, Query: query}
}

func cloneWebRequest(request WebRequest) WebRequest {
	result := WebRequest{Method: request.Method, Path: request.Path, Headers: make(map[string]string, len(request.Headers)), Query: make(map[string][]string, len(request.Query))}
	for key, value := range request.Headers {
		result.Headers[key] = value
	}
	for key, values := range request.Query {
		result.Query[key] = append([]string(nil), values...)
	}
	return result
}

func (router *RestRouter) invokeProblem(writer http.ResponseWriter, err error) {
	var rejected *RejectedError
	if errors.As(err, &rejected) {
		problem(writer, http.StatusUnprocessableEntity, "rejected", rejected.Error())
		return
	}
	if errors.Is(err, contexture.ErrInvalidInput) {
		problem(writer, http.StatusUnprocessableEntity, "invalid-arguments", err.Error())
		return
	}
	if errors.Is(err, contexture.ErrWrongDoor) || errors.Is(err, contexture.ErrNodeNotFound) {
		problem(writer, http.StatusInternalServerError, "invalid-surface", err.Error())
		return
	}
	if errors.Is(err, fs.ErrPermission) {
		problem(writer, http.StatusForbidden, "forbidden", nonEmptyDetail(err.Error(), "Forbidden."))
		return
	}
	problem(writer, http.StatusInternalServerError, "controller-failed", typeName(err))
}

func typeName(err error) string {
	return fmt.Sprintf("%T", err)
}

func nonEmptyDetail(detail, fallback string) string {
	if detail == "" {
		return fallback
	}
	return detail
}

func jsonResponse(writer http.ResponseWriter, status int, value any, head bool) {
	body, err := json.Marshal(value)
	if err != nil {
		problem(writer, http.StatusInternalServerError, "controller-failed", typeName(err))
		return
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	writer.WriteHeader(status)
	if !head {
		_, _ = writer.Write(body)
	}
}

func problem(writer http.ResponseWriter, status int, kind, detail string) {
	body, _ := json.Marshal(map[string]any{
		"type": "urn:contexture:problem:" + kind, "status": status,
		"title": strings.ReplaceAll(kind, "-", " "), "detail": detail,
	})
	writer.Header().Set("Content-Type", "application/problem+json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
	writer.WriteHeader(status)
	_, _ = writer.Write(body)
}
