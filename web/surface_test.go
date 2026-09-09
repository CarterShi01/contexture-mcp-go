package web_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
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
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
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

func TestRestRouteNormalizationMethodsAndDefensiveSnapshots(t *testing.T) {
	read, err := contexture.NewTool("status", "Status.", true, func(context.Context, restEmptyInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("reset", "Reset.", false, func(context.Context, restEmptyInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	runtime := restRuntime(t, read, write)
	router, err := web.NewRestRouter(runtime, []web.RestRoute{{Method: " get ", Path: " / ", Ref: " ops/status "}})
	if err != nil {
		t.Fatal(err)
	}
	routes := router.Routes()
	if len(routes) != 1 || routes[0] != (web.RestRoute{Method: http.MethodGet, Path: "/", Ref: "ops/status", Status: http.StatusOK}) {
		t.Fatalf("normalized routes = %#v", routes)
	}
	routes[0].Path = "/forged"
	if router.Routes()[0].Path != "/" {
		t.Fatal("Routes returned mutable internal state")
	}
	routes = append(routes, web.RestRoute{Method: http.MethodPost, Path: "/forged", Ref: "ops/reset"})
	routes[0], routes[1] = routes[1], routes[0]
	if snapshot := router.Routes(); len(snapshot) != 1 || snapshot[0].Path != "/" {
		t.Fatalf("Routes returned a mutable slice snapshot: %#v", snapshot)
	}
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		ref := "ops/reset"
		if method == http.MethodGet || method == http.MethodHead {
			ref = "ops/status"
		}
		if _, err := web.NewRestRouter(runtime, []web.RestRoute{{Method: method, Path: "/" + strings.ToLower(method), Ref: ref}}); err != nil {
			t.Fatalf("method %s = %v", method, err)
		}
	}
	for _, status := range []int{99, 600} {
		if _, err := web.NewRestRouter(runtime, []web.RestRoute{{Method: http.MethodGet, Path: "/status", Ref: "ops/status", Status: status}}); err == nil {
			t.Fatalf("status %d was accepted", status)
		}
	}
	if _, err := web.NewRestRouter(runtime, []web.RestRoute{
		{Method: " get ", Path: " /duplicate ", Ref: " ops/status "},
		{Method: http.MethodGet, Path: "/duplicate", Ref: "ops/status"},
	}); err == nil {
		t.Fatal("routes colliding after normalization were accepted")
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

func TestRestSurfaceMapsBusinessRejection(t *testing.T) {
	rejected, err := contexture.NewTool("rejected", "Rejected.", true, func(context.Context, restEmptyInput) (string, error) {
		return "", web.Reject("The change window is closed.")
	})
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("write", "Write.", false, func(context.Context, restEmptyInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	surface, err := web.NewRestSurface(restRuntime(t, rejected, write), []web.RestRoute{{Method: http.MethodGet, Path: "/rejected", Ref: "ops/rejected"}}, web.RestRouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	surface.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/rejected", nil))
	assertProblem(t, response, http.StatusUnprocessableEntity, "rejected")
}

func TestRestSurfaceAcceptsAnEmptyJSONCommandObject(t *testing.T) {
	read, err := contexture.NewTool("read", "Read.", true, func(context.Context, restEmptyInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("command", "Command.", false, func(context.Context, restEmptyInput) (string, error) { return "done", nil })
	if err != nil {
		t.Fatal(err)
	}
	surface, err := web.NewRestSurface(restRuntime(t, read, write), []web.RestRoute{{Method: http.MethodPost, Path: "/command", Ref: "ops/command"}}, web.RestRouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	surface.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/command", nil))
	if response.Code != http.StatusOK || response.Body.String() != `"done"` {
		t.Fatalf("empty command = %d %q", response.Code, response.Body.String())
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
		{Method: http.MethodHead, Path: "/status-head", Ref: "ops/status", Status: http.StatusPartialContent},
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
	request = httptest.NewRequest(http.MethodHead, "/status-head?value=ready", nil)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusPartialContent || response.Body.Len() != 0 {
		t.Fatalf("explicit HEAD = %d %q", response.Code, response.Body.String())
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
		request.Headers["x-request-id"] = "mutated"
		request.Query["value"][0] = "mutated"
		unchanged, ok := web.CurrentRequest(ctx)
		if !ok || unchanged.Headers["x-request-id"] != "request-42" || unchanged.Query["value"][0] != "" {
			return nil, errors.New("CurrentRequest did not return a defensive copy")
		}
		return map[string]any{"subject": principal.Subject(), "header": unchanged.Headers["x-request-id"], "method": unchanged.Method, "values": unchanged.Query["value"], "input": input.Value}, nil
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
			if request.Path == "/who" {
				if request.Method != http.MethodGet || len(request.Query["value"]) != 2 || request.Query["value"][0] != "" || request.Query["value"][1] != "two" {
					return nil
				}
				request.Headers["x-request-id"] = "auth-mutated"
				request.Query["value"][0] = "auth-mutated"
			}
			return contexture.NewPrincipal(contexture.PrincipalOptions{Subject: "alice"})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest("get", "/who?value=&value=two", strings.NewReader(`{"value":["ignored"]}`))
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
	if result["subject"] != "alice" || result["header"] != "request-42" || result["method"] != http.MethodGet || !reflect.DeepEqual(result["input"], []any{"", "two"}) {
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
		{Method: http.MethodGet, Path: "/status#fragment", Ref: "ops/status"},
		{Method: http.MethodGet, Path: "/status/{name}", Ref: "ops/status"},
		{Method: http.MethodGet, Path: "/status", Ref: "", Status: http.StatusOK},
		{Method: http.MethodGet, Path: "/status", Ref: "ops/status", Status: 99},
		{Method: http.MethodGet, Path: "/status", Ref: "ops/status", Status: 600},
		{Method: http.MethodGet, Path: "/status", Ref: "ops/status", Status: 199},
		{Method: http.MethodGet, Path: "/status", Ref: "ops/status", Status: http.StatusNoContent},
		{Method: http.MethodGet, Path: "/status", Ref: "ops/status", Status: http.StatusResetContent},
		{Method: http.MethodGet, Path: "/status", Ref: "ops/status", Status: http.StatusNotModified},
	} {
		if _, err := web.NewRestRouter(runtime, []web.RestRoute{route}); err == nil {
			t.Fatalf("invalid route accepted: %#v", route)
		}
	}
	if _, err := web.NewRestRouterWithOptions(runtime, nil, web.RestRouterOptions{MaxBodyBytes: -1}); err == nil {
		t.Fatal("negative body limit accepted")
	}
	if _, err := web.NewRestRouter(runtime, []web.RestRoute{{Method: http.MethodGet, Path: "/status", Ref: "ops"}}); err == nil {
		t.Fatal("non-Tool route ref accepted")
	}
	if _, err := web.NewRestRouter(runtime, []web.RestRoute{{Method: http.MethodGet, Path: "/status", Ref: "ops/status"}, {Method: http.MethodGet, Path: "/status", Ref: "ops/status"}}); err == nil {
		t.Fatal("duplicate route accepted")
	}
}

func TestRestSurfaceDoesNotTrustIdentityHeadersWithoutAnAuthenticator(t *testing.T) {
	read, err := contexture.NewTool("identity", "Identity.", true, func(ctx context.Context, _ restEmptyInput) (string, error) {
		if contexture.CurrentPrincipal(ctx) != nil {
			return "", errors.New("forged identity reached the Tool")
		}
		return "anonymous", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("write", "Write.", false, func(context.Context, restEmptyInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	surface, err := web.NewRestSurface(restRuntime(t, read, write), []web.RestRoute{{Method: http.MethodGet, Path: "/identity", Ref: "ops/identity"}}, web.RestRouterOptions{})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/identity", nil)
	request.Header.Set("Authorization", "Bearer forged")
	request.Header.Set("X-Contexture-Principal", "admin")
	response := httptest.NewRecorder()
	surface.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != `"anonymous"` {
		t.Fatalf("unauthenticated identity = %d %q", response.Code, response.Body.String())
	}
}

func TestRestSurfaceIsolatesConcurrentAuthenticatedHTTPRequests(t *testing.T) {
	read, err := contexture.NewTool("who", "Who.", true, func(ctx context.Context, input restInput) (map[string]string, error) {
		principal := contexture.CurrentPrincipal(ctx)
		request, ok := web.CurrentRequest(ctx)
		if !ok || principal == nil {
			return nil, errors.New("missing request identity")
		}
		return map[string]string{"subject": principal.Subject(), "request": request.Headers["x-request-id"], "input": input.Value}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	write, err := contexture.NewTool("write", "Write.", false, func(context.Context, restEmptyInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	surface, err := web.NewRestSurface(restRuntime(t, read, write), []web.RestRoute{{Method: http.MethodGet, Path: "/who", Ref: "ops/who"}}, web.RestRouterOptions{
		Authenticator: func(_ context.Context, request web.WebRequest) *contexture.Principal {
			name := request.Headers["x-user"]
			if name == "" || request.Headers["authorization"] != "Bearer "+name {
				return nil
			}
			return contexture.NewPrincipal(contexture.PrincipalOptions{Subject: name})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(surface)
	defer server.Close()
	type answer struct {
		name string
		body map[string]string
		err  error
	}
	answers := make(chan answer, 2)
	var group sync.WaitGroup
	for _, name := range []string{"alice", "bob"} {
		group.Add(1)
		go func(name string) {
			defer group.Done()
			request, err := http.NewRequest(http.MethodGet, server.URL+"/who?value="+name, nil)
			if err == nil {
				request.Header.Set("Authorization", "Bearer "+name)
				request.Header.Set("X-User", name)
				request.Header.Set("X-Request-ID", "request-"+name)
				response, callErr := http.DefaultClient.Do(request)
				if callErr != nil {
					err = callErr
				} else {
					defer response.Body.Close()
					body := map[string]string{}
					if response.StatusCode != http.StatusOK {
						err = fmt.Errorf("HTTP status %d", response.StatusCode)
					} else {
						err = json.NewDecoder(response.Body).Decode(&body)
					}
					answers <- answer{name: name, body: body, err: err}
					return
				}
			}
			answers <- answer{name: name, err: err}
		}(name)
	}
	group.Wait()
	close(answers)
	for answer := range answers {
		if answer.err != nil {
			t.Fatal(answer.err)
		}
		if answer.body["subject"] != answer.name || answer.body["request"] != "request-"+answer.name || answer.body["input"] != answer.name {
			t.Fatalf("concurrent response for %s = %#v", answer.name, answer.body)
		}
	}
}

func TestRestSurfaceServeOwnsChannelsAcrossRealHTTPRequestsAndRuns(t *testing.T) {
	channels := &surfaceChannels{}
	read, err := contexture.NewTool("status", "Status.", true, func(_ context.Context, input restInput) (string, error) {
		generation, live := channels.snapshot()
		if !live {
			return "", errors.New("request reached a closed Channel")
		}
		return fmt.Sprintf("%d:%s", generation, input.Value), nil
	})
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
	serve := func(wantGeneration int, values ...string) {
		t.Helper()
		if err := surface.Serve(context.Background(), func(_ context.Context, handler http.Handler) error {
			server := httptest.NewServer(handler)
			defer server.Close()
			generation, live := channels.snapshot()
			if generation != wantGeneration || !live {
				t.Fatalf("channels during generation %d = %d, live %v", wantGeneration, generation, live)
			}
			for _, value := range values {
				response, err := http.Get(server.URL + "/status?value=" + value)
				if err != nil {
					t.Fatal(err)
				}
				body, readErr := io.ReadAll(response.Body)
				response.Body.Close()
				if readErr != nil || response.StatusCode != http.StatusOK || string(body) != fmt.Sprintf(`"%d:%s"`, wantGeneration, value) {
					t.Fatalf("HTTP response = %d %q (%v)", response.StatusCode, body, readErr)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	serve(1, "one", "two")
	if generation, live := channels.snapshot(); generation != 1 || live || channels.counts() != [2]int{1, 1} {
		t.Fatalf("channels after first serving = generation %d, live %v, counts %#v", generation, live, channels.counts())
	}
	serve(2, "again")
	if generation, live := channels.snapshot(); generation != 2 || live || channels.counts() != [2]int{2, 2} {
		t.Fatalf("channels after second serving = generation %d, live %v, counts %#v", generation, live, channels.counts())
	}
	primary, closeFailure, cleanupFailure := errors.New("primary serving failure"), errors.New("close failure"), errors.New("cleanup failure")
	channels.setFailures(closeFailure, cleanupFailure)
	err = surface.Serve(context.Background(), func(context.Context, http.Handler) error { return primary })
	if !errors.Is(err, primary) || !errors.Is(err, closeFailure) || !errors.Is(err, cleanupFailure) {
		t.Fatalf("combined REST serving failure = %v", err)
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

type surfaceChannels struct {
	contexture.ChannelsLifecycle
	mu             sync.Mutex
	opened, closed int
	generation     int
	live           bool
	closeFailure   error
	cleanupFailure error
}

func (channels *surfaceChannels) Open(_ context.Context, registrar contexture.CleanupRegistrar) error {
	channels.mu.Lock()
	defer channels.mu.Unlock()
	channels.opened++
	channels.generation++
	channels.live = true
	registrar.Defer(func(context.Context) error { return channels.cleanupFailure })
	return nil
}

func (channels *surfaceChannels) Close(context.Context) error {
	channels.mu.Lock()
	defer channels.mu.Unlock()
	channels.closed++
	channels.live = false
	return channels.closeFailure
}

func (channels *surfaceChannels) snapshot() (int, bool) {
	channels.mu.Lock()
	defer channels.mu.Unlock()
	return channels.generation, channels.live
}

func (channels *surfaceChannels) counts() [2]int {
	channels.mu.Lock()
	defer channels.mu.Unlock()
	return [2]int{channels.opened, channels.closed}
}

func (channels *surfaceChannels) setFailures(closeFailure, cleanupFailure error) {
	channels.mu.Lock()
	defer channels.mu.Unlock()
	channels.closeFailure, channels.cleanupFailure = closeFailure, cleanupFailure
}
