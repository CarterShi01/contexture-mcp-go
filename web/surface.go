package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

// RestRouter is an allowlisted net/http adapter over Runtime Bindings.
// It never accepts a caller-supplied Tool ref or principal.
type RestRouter struct {
	runtime *contexture.Runtime
	routes  map[string]RestRoute
	order   []string
}

// NewRestRouter validates every explicit route before it can serve requests.
func NewRestRouter(runtime *contexture.Runtime, routes []RestRoute) (*RestRouter, error) {
	if runtime == nil {
		return nil, errors.New("Contexture Runtime must not be nil")
	}
	router := &RestRouter{runtime: runtime, routes: map[string]RestRoute{}}
	for _, route := range routes {
		route.Method = strings.ToUpper(route.Method)
		if !strings.HasPrefix(route.Path, "/") || route.Path == "/" {
			return nil, errors.New("A Contexture REST route path must begin with /.")
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
	route, exists := router.routes[routeKey(request.Method, request.URL.Path)]
	if !exists {
		if router.hasPath(request.URL.Path) {
			http.Error(writer, "Contexture REST method is not allowed for this route.", http.StatusMethodNotAllowed)
			return
		}
		http.NotFound(writer, request)
		return
	}
	arguments, err := requestArguments(writer, request)
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	var value any
	if route.Method == http.MethodGet || route.Method == http.MethodHead {
		value, err = router.runtime.InvokeReadOnly(request.Context(), route.Ref, arguments, contexture.AllRoots())
	} else {
		value, err = router.runtime.Invoke(request.Context(), route.Ref, arguments, contexture.AllRoots())
	}
	if err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	if route.Method == http.MethodHead {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
	}
}

func (router *RestRouter) hasPath(path string) bool {
	for _, route := range router.routes {
		if route.Path == path {
			return true
		}
	}
	return false
}

func requestArguments(writer http.ResponseWriter, request *http.Request) (json.RawMessage, error) {
	if request.Method == http.MethodGet || request.Method == http.MethodHead {
		return json.RawMessage("{}"), nil
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return json.RawMessage("{}"), nil
	}
	return json.RawMessage(body), nil
}

func routeKey(method, path string) string { return strings.ToUpper(method) + " " + path }
