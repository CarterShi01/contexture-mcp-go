package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/web"
)

type restInput struct {
	Value string `json:"value"`
}

type restListInput struct {
	Value []string `json:"value"`
}

type restEmptyInput struct{}

func TestRestRouterUsesExplicitAllowlistAndFixedDoors(t *testing.T) {
	read, err := contexture.NewTool("status", "Status.", true, func(_ context.Context, input restInput) (string, error) { return input.Value, nil })
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("restart", "Restart.", false, func(_ context.Context, input restInput) (string, error) { return input.Value, nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "rest", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "ops", Description: "Ops.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return read }, func() contexture.Node { return write }}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	router, err := web.NewRestRouter(runtime, []web.RestRoute{{Method: http.MethodGet, Path: "/status", Ref: "ops/status"}, {Method: http.MethodPost, Path: "/restart", Ref: "ops/restart"}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/status", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("GET missing input = %d", response.Code)
	}
	assertProblem(t, response, http.StatusUnprocessableEntity, "invalid-arguments")
	request = httptest.NewRequest(http.MethodPost, "/restart", bytes.NewBufferString(`{"value":"ok"}`))
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("POST = %d: %s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/status", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("wrong method = %d", response.Code)
	}
	request = httptest.NewRequest(http.MethodPost, "/arbitrary/ops/status", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("arbitrary ref = %d", response.Code)
	}
	if _, err := web.NewRestRouter(runtime, []web.RestRoute{{Method: http.MethodPost, Path: "/wrong", Ref: "ops/status"}}); err == nil {
		t.Fatal("writing route accepted read-only Tool")
	}
}

func TestRestSurfaceMapsInvocationFailures(t *testing.T) {
	denied, err := contexture.NewTool("denied", "Denied.", true, func(context.Context, restEmptyInput) (string, error) {
		return "", fs.ErrPermission
	})
	if err != nil {
		t.Fatal(err)
	}
	failing, err := contexture.NewTool("failing", "Failing.", false, func(context.Context, restEmptyInput) (string, error) {
		return "", errors.New("controller stopped")
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := restRuntime(t, denied, failing)
	surface, err := web.NewRestSurface(runtime, []web.RestRoute{{Method: http.MethodGet, Path: "/denied", Ref: "ops/denied"}, {Method: http.MethodPost, Path: "/failing", Ref: "ops/failing"}}, web.RestRouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		method, path, kind string
		status             int
	}{
		{method: http.MethodGet, path: "/denied", status: http.StatusForbidden, kind: "forbidden"},
		{method: http.MethodPost, path: "/failing", status: http.StatusInternalServerError, kind: "controller-failed"},
	} {
		t.Run(scenario.kind, func(t *testing.T) {
			response := httptest.NewRecorder()
			surface.ServeHTTP(response, httptest.NewRequest(scenario.method, scenario.path, nil))
			assertProblem(t, response, scenario.status, scenario.kind)
		})
	}
}

func TestRestSurfacePreservesHTTPRequestSemantics(t *testing.T) {
	read, err := contexture.NewTool("status", "Status.", true, func(_ context.Context, input restInput) (map[string]string, error) {
		return map[string]string{"value": input.Value}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("restart", "Restart.", false, func(_ context.Context, input restInput) (map[string]string, error) {
		return map[string]string{"value": input.Value}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	runtime := restRuntime(t, read, write)
	router, err := web.NewRestSurface(runtime, []web.RestRoute{
		{Method: " get ", Path: " /status ", Ref: " ops/status ", Status: http.StatusAccepted},
		{Method: http.MethodPost, Path: "/restart", Ref: "ops/restart"},
	}, web.RestRouterOptions{})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/status?value=ready", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Body.String() != `{"value":"ready"}` {
		t.Fatalf("GET = %d %q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Fatalf("GET content type = %q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("GET cache control = %q", got)
	}

	request = httptest.NewRequest(http.MethodHead, "/status?value=ready", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || response.Body.Len() != 0 {
		t.Fatalf("HEAD fallback = %d %q", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Content-Length"); got != "17" {
		t.Fatalf("HEAD content length = %q", got)
	}

	for _, scenario := range []struct {
		name, body, contentType string
		status                  int
		kind                    string
	}{
		{name: "media type", body: `{"value":"ok"}`, contentType: "text/plain", status: http.StatusUnsupportedMediaType, kind: "unsupported-media-type"},
		{name: "invalid JSON", body: `{`, contentType: "application/json", status: http.StatusBadRequest, kind: "invalid-json"},
		{name: "array body", body: `[]`, contentType: "application/json", status: http.StatusBadRequest, kind: "invalid-body"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/restart", strings.NewReader(scenario.body))
			request.Header.Set("Content-Type", scenario.contentType)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != scenario.status {
				t.Fatalf("status = %d: %s", response.Code, response.Body.String())
			}
			assertProblem(t, response, scenario.status, scenario.kind)
		})
	}

	request = httptest.NewRequest(http.MethodGet, "/not-published", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	assertProblem(t, response, http.StatusNotFound, "route-not-found")
}

func TestRestSurfaceEnforcesBodyLimitAndCarriesAuthenticationAndRequest(t *testing.T) {
	read, err := contexture.NewTool("who", "Who.", true, func(ctx context.Context, input restListInput) (map[string]any, error) {
		principal := contexture.CurrentPrincipal(ctx)
		request, ok := web.CurrentRequest(ctx)
		if !ok || principal == nil {
			return nil, errors.New("missing HTTP invocation context")
		}
		return map[string]any{"subject": principal.Subject(), "header": request.Headers["x-request-id"], "values": request.Query["value"], "input": input.Value}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("restart", "Restart.", false, func(_ context.Context, input restInput) (string, error) { return input.Value, nil })
	if err != nil {
		t.Fatal(err)
	}
	runtime := restRuntime(t, read, write)
	surface, err := web.NewRestSurface(runtime, []web.RestRoute{{Method: http.MethodGet, Path: "/who", Ref: "ops/who"}, {Method: http.MethodPost, Path: "/restart", Ref: "ops/restart"}}, web.RestRouterOptions{
		MaxBodyBytes: 5,
		Authenticator: func(_ context.Context, request web.WebRequest) *contexture.Principal {
			if request.Headers["authorization"] != "Bearer test" {
				return nil
			}
			if request.Path == "/who" && (len(request.Query["value"]) != 2 || request.Query["value"][0] != "one" || request.Query["value"][1] != "two") {
				return nil
			}
			return contexture.NewPrincipal(contexture.PrincipalOptions{Subject: "alice"})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/who?value=one&value=two", nil)
	request.Header.Set("Authorization", "Bearer test")
	request.Header.Set("X-Request-ID", "request-42")
	response := httptest.NewRecorder()
	surface.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("authenticated invocation = %d: %s", response.Code, response.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result["subject"] != "alice" || result["header"] != "request-42" {
		t.Fatalf("invocation context = %#v", result)
	}

	request = httptest.NewRequest(http.MethodGet, "/who?value=one&value=two", nil)
	response = httptest.NewRecorder()
	surface.ServeHTTP(response, request)
	assertProblem(t, response, http.StatusUnauthorized, "unauthenticated")

	request = httptest.NewRequest(http.MethodPost, "/restart", strings.NewReader(`{"value":"too large"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer test")
	response = httptest.NewRecorder()
	surface.ServeHTTP(response, request)
	assertProblem(t, response, http.StatusRequestEntityTooLarge, "body-too-large")
}

func TestRestSurfaceRejectsInvalidRoutes(t *testing.T) {
	read, err := contexture.NewTool("status", "Status.", true, func(_ context.Context, input restInput) (string, error) { return input.Value, nil })
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("restart", "Restart.", false, func(_ context.Context, input restInput) (string, error) { return input.Value, nil })
	if err != nil {
		t.Fatal(err)
	}
	runtime := restRuntime(t, read, write)
	for _, route := range []web.RestRoute{
		{Method: http.MethodOptions, Path: "/status", Ref: "ops/status"},
		{Method: http.MethodGet, Path: "status", Ref: "ops/status"},
		{Method: http.MethodGet, Path: "/status/", Ref: "ops/status"},
		{Method: http.MethodGet, Path: "/status?live", Ref: "ops/status"},
		{Method: http.MethodGet, Path: "/status", Ref: "", Status: http.StatusOK},
		{Method: http.MethodGet, Path: "/status", Ref: "ops/status", Status: 99},
	} {
		if _, err := web.NewRestRouter(runtime, []web.RestRoute{route}); err == nil {
			t.Fatalf("invalid route accepted: %#v", route)
		}
	}
	if _, err := web.NewRestRouterWithOptions(runtime, nil, web.RestRouterOptions{MaxBodyBytes: -1}); err == nil {
		t.Fatal("negative body limit accepted")
	}
}

func TestRestSurfaceServeOwnsChannelsForTheServingLifetime(t *testing.T) {
	channels := &surfaceChannels{}
	read, err := contexture.NewTool("status", "Status.", true, func(_ context.Context, input restInput) (string, error) { return input.Value, nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "rest", Channels: channels,
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "ops", Description: "Ops.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return read }}}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	surface, err := web.NewRestSurface(runtime, []web.RestRoute{{Method: http.MethodGet, Path: "/status", Ref: "ops/status"}}, web.RestRouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = surface.Serve(context.Background(), func(_ context.Context, handler http.Handler) error {
		if channels.opened != 1 || channels.closed != 0 {
			t.Fatalf("channels during serving = opened %d, closed %d", channels.opened, channels.closed)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/status?value=ready", nil))
		if response.Code != http.StatusOK {
			t.Fatalf("handler response = %d: %s", response.Code, response.Body.String())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if channels.opened != 1 || channels.closed != 1 {
		t.Fatalf("channels after serving = opened %d, closed %d", channels.opened, channels.closed)
	}
}

func restRuntime(t *testing.T, read, write *contexture.Tool) *contexture.Runtime {
	t.Helper()
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "rest", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "ops", Description: "Ops.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return read }, func() contexture.Node { return write }}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

func assertProblem(t *testing.T, response *httptest.ResponseRecorder, status int, kind string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d, want %d: %s", response.Code, status, response.Body.String())
	}
	if got := response.Header().Get("Content-Type"); got != "application/problem+json; charset=utf-8" {
		t.Fatalf("problem content type = %q", got)
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("problem cache control = %q", got)
	}
	length, err := strconv.Atoi(response.Header().Get("Content-Length"))
	if err != nil || length != response.Body.Len() {
		t.Fatalf("problem content length = %q for %d-byte body", response.Header().Get("Content-Length"), response.Body.Len())
	}
	var value map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if value["type"] != "urn:contexture:problem:"+kind || value["status"] != float64(status) || value["title"] != strings.ReplaceAll(kind, "-", " ") {
		t.Fatalf("problem = %#v", value)
	}
}

type surfaceChannels struct{ opened, closed int }

func (channels *surfaceChannels) Open(_ context.Context, _ contexture.CleanupRegistrar) error {
	channels.opened++
	return nil
}

func (channels *surfaceChannels) Close(context.Context) error {
	channels.closed++
	return nil
}
