package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	serverinstructions "github.com/CarterShi01/contexture-mcp-go/server/instructions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ApplicationServer owns one compiled declaration and its official MCP transport assembly.
type ApplicationServer struct {
	application  *RuntimeApplication
	identity     Identity
	instructions *string
	buildOnce    sync.Once
	adapter      *ContextureMCPServer
	buildErr     error
}

// ApplicationServerOptions controls host-facing facts fixed when an
// application is compiled for serving.
type ApplicationServerOptions struct {
	// Instructions replaces Contexture's generated root roster. A nil value
	// derives instructions from each selected root surface.
	Instructions *string
}

type rootSelectionContextKey struct{}

// BuildServer compiles one lazy declaration for serving.
func BuildServer(application *contexture.Application) (*ApplicationServer, error) {
	return BuildServerWithOptions(application, ApplicationServerOptions{})
}

// BuildServerWithOptions compiles one lazy declaration with explicit Host-facing options.
func BuildServerWithOptions(application *contexture.Application, options ApplicationServerOptions) (*ApplicationServer, error) {
	compiled, err := CompileApplication(application)
	if err != nil {
		return nil, err
	}
	return &ApplicationServer{application: compiled, identity: Identity{Name: application.Name(), Version: contexture.Version}, instructions: options.Instructions}, nil
}

// Build constructs a fresh official-SDK server for one Contexture transport service.
func (server *ApplicationServer) Build() (*ContextureMCPServer, error) {
	if server == nil {
		return nil, fmt.Errorf("Contexture application server must not be nil")
	}
	server.buildOnce.Do(func() {
		server.adapter, server.buildErr = server.BuildForRoots(contexture.AllRoots())
	})
	return server.adapter, server.buildErr
}

// BuildForRoots constructs an adapter whose gateway and publications share one
// validated immutable root projection.
func (server *ApplicationServer) BuildForRoots(selection contexture.RootSelection) (*ContextureMCPServer, error) {
	if server == nil || server.application == nil {
		return nil, fmt.Errorf("Contexture application server must not be nil")
	}
	selection, err := selection.Resolve(server.application.Index)
	if err != nil {
		return nil, err
	}
	gateway, err := server.application.Gateway()
	if err != nil {
		return nil, err
	}
	instructions, err := server.instructionsFor(selection)
	if err != nil {
		return nil, err
	}
	return newContextureMCPServerForRoots(server.identity, gateway, selection, instructions, server.application.Publications), nil
}

func (server *ApplicationServer) instructionsFor(selection contexture.RootSelection) (string, error) {
	if server.instructions != nil {
		return *server.instructions, nil
	}
	return serverinstructions.Build(server.application.Disclosure, selection, serverinstructions.RosterBudget)
}

// Start blocks while serving stdio or streamable HTTP with Channels open for the lifetime.
func (server *ApplicationServer) Start(ctx context.Context, options *ContextureOptions) error {
	return server.StartWithAuthAndRootSelector(ctx, options, nil, nil)
}

// StartWithAuthAndRootSelector starts one transport with optional HTTP bearer
// identity and request-local root attenuation. Those HTTP-only policies are
// rejected for stdio rather than silently ignored.
func (server *ApplicationServer) StartWithAuthAndRootSelector(ctx context.Context, options *ContextureOptions, identity *Auth, selector RootSelector) error {
	options, identity, err := resolveServeOptions(options, identity)
	if err != nil {
		return err
	}
	if err := ConfigureLogging(options.LogLevel); err != nil {
		return err
	}
	if options.Transport == StdioTransport {
		if identity != nil || selector != nil {
			return &ServeError{Message: "stdio cannot use HTTP identity or root selection."}
		}
		return server.application.Runtime.Serve(ctx, func(ctx context.Context) error {
			adapter, err := server.Build()
			if err != nil {
				return err
			}
			return adapter.Server.Run(ctx, &mcp.StdioTransport{})
		})
	}
	listener, err := net.Listen("tcp", net.JoinHostPort(options.Host, fmt.Sprint(options.Port)))
	if err != nil {
		return err
	}
	// resolveServeOptions has already installed an explicit identity into the
	// validated options. Do not pass it a second time through the embedding
	// entry point, where two sources are intentionally rejected.
	return server.ServeListenerWithAuthAndRootSelector(ctx, listener, options, nil, selector)
}

// ServeListener serves streamable HTTP through an existing listener; it is useful for embedding and tests.
func (server *ApplicationServer) ServeListener(ctx context.Context, listener net.Listener, options *ContextureOptions) error {
	return server.ServeListenerWithAuthAndRootSelector(ctx, listener, options, nil, nil)
}

// ServeListenerWithAuth serves one HTTP listener and validates bearer tokens before MCP dispatch.
func (server *ApplicationServer) ServeListenerWithAuth(ctx context.Context, listener net.Listener, options *ContextureOptions, identity *Auth) error {
	return server.ServeListenerWithAuthAndRootSelector(ctx, listener, options, identity, nil)
}

// ServeListenerWithAuthAndRootSelector serves one HTTP listener with optional
// bearer identity and request-local root attenuation. Authentication runs before
// selection, so a selector ceiling receives only a verified Principal.
func (server *ApplicationServer) ServeListenerWithAuthAndRootSelector(ctx context.Context, listener net.Listener, options *ContextureOptions, identity *Auth, selector RootSelector) error {
	if server == nil || server.application == nil || listener == nil {
		return fmt.Errorf("Contexture HTTP server requires an application and listener")
	}
	if options == nil {
		return &ServeError{Message: "ServeListener requires transport='streamable-http'."}
	}
	options, identity, err := resolveServeOptions(options, identity)
	if err != nil {
		return err
	}
	options, err = validateListenerOptions(options, listener)
	if err != nil {
		return err
	}
	if err := ConfigureLogging(options.LogLevel); err != nil {
		return err
	}
	if options.Transport != StreamableHTTPTransport {
		return &ServeError{Message: "ServeListener requires transport='streamable-http'."}
	}
	slog.Info("Serving MCP", "url", fmt.Sprintf("http://%s", listener.Addr()))
	if _, err := server.application.Gateway(); err != nil {
		return err
	}
	handler := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		selection := contexture.AllRoots()
		if selected, ok := request.Context().Value(rootSelectionContextKey{}).(contexture.RootSelection); ok {
			selection = selected
		}
		adapter, err := server.BuildForRoots(selection)
		if err != nil {
			// Selection middleware validates every request before this factory;
			// this defensive fallback only covers a future programming error.
			return NewMCPServer(server.identity)
		}
		return adapter.Server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: options.MaxRequestBodyBytes})
	var protected http.Handler = guarded(handler, options)
	if selector != nil {
		protected = selectedRoots(protected, server.application.Index, selector)
	}
	if identity != nil {
		middleware, err := identity.Middleware()
		if err != nil {
			return err
		}
		protected = middleware(protected)
	}
	httpServer := &http.Server{Handler: protected}
	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
	}()
	return server.application.Runtime.Serve(ctx, func(context.Context) error {
		err := httpServer.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, net.ErrClosed) {
			return nil
		}
		return err
	})
}

// validateListenerOptions makes an embedding listener subject to the same
// network policy as Start. A caller cannot declare a safe loopback Host and
// then hand Contexture an already-bound public listener. The listener owns its
// port, so only the bind host must agree with the declared endpoint.
func validateListenerOptions(options *ContextureOptions, listener net.Listener) (*ContextureOptions, error) {
	host, _, err := net.SplitHostPort(listener.Addr().String())
	if err != nil || host == "" {
		return nil, &ServeError{Message: fmt.Sprintf("ServeListener needs a TCP listener with a host address: %v", err)}
	}
	host = strings.Trim(host, "[]")
	actual := *options
	actual.Host = host
	// The listener did state a bind host even when the caller's options used a
	// default. Re-run validation against that fact before accepting the server.
	actual.hostSet = true
	validated, err := NewContextureOptions(actual)
	if err != nil {
		return nil, err
	}
	if !sameBindHost(options.Host, host) {
		return nil, &ServeError{Message: fmt.Sprintf("ContextureOptions Host %q does not match listener bind host %q.", options.Host, host)}
	}
	return validated, nil
}

func sameBindHost(declared, actual string) bool {
	declared = strings.Trim(declared, "[]")
	actual = strings.Trim(actual, "[]")
	if strings.EqualFold(declared, actual) {
		return true
	}
	// net.Listen("tcp", "localhost:...") reports a concrete loopback IP on
	// every supported platform, not the hostname the caller declared. Treat
	// localhost and either canonical loopback address as the same safe bind;
	// do not apply that equivalence to any public hostname or address.
	if strings.EqualFold(declared, "localhost") && isLoopback(actual) {
		return true
	}
	if strings.EqualFold(actual, "localhost") && isLoopback(declared) {
		return true
	}
	return false
}

// resolveServeOptions retains the older explicit-identity entry points while
// making ContextureOptions the canonical declaration of HTTP configuration.
// Supplying both would make it unclear which bearer policy protects the same
// listener, so it is a named startup error rather than a call-order decision.
func resolveServeOptions(input *ContextureOptions, explicit *Auth) (*ContextureOptions, *Auth, error) {
	if input == nil {
		input = &ContextureOptions{}
	}
	options := *input
	if explicit != nil {
		if options.Auth != nil {
			return nil, nil, &ServeError{Message: "state HTTP auth in ContextureOptions or the explicit StartWithAuth method, not both."}
		}
		options.Auth = explicit
	}
	validated, err := NewContextureOptions(options)
	if err != nil {
		return nil, nil, err
	}
	return validated, validated.Auth, nil
}

// selectedRoots resolves request facts once before MCP dispatch and retains the
// immutable projection in the request context consumed by the MCP factory.
func selectedRoots(next http.Handler, index *contexture.Index, selector RootSelector) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		selection, err := selector.Select(index, requestHeaders(request.Header), PrincipalOf(request.Context()))
		if err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		next.ServeHTTP(writer, request.WithContext(context.WithValue(request.Context(), rootSelectionContextKey{}, selection)))
	})
}

func requestHeaders(headers http.Header) map[string]string {
	result := make(map[string]string, len(headers))
	for name, values := range headers {
		result[name] = strings.Join(values, ", ")
	}
	return result
}

func guarded(next http.Handler, options *ContextureOptions) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != options.Path {
			http.NotFound(writer, request)
			return
		}
		if len(options.AllowedHosts) > 0 && !allowedHost(options.AllowedHosts, request.Host) {
			http.Error(writer, "Forbidden: invalid Host header", http.StatusForbidden)
			return
		}
		if origin := request.Header.Get("Origin"); origin != "" && len(options.AllowedOrigins) > 0 && !contains(options.AllowedOrigins, origin) {
			http.Error(writer, "Forbidden: invalid Origin header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func allowedHost(values []string, requestHost string) bool {
	name := hostName(requestHost)
	for _, value := range values {
		if value == requestHost || value == name {
			return true
		}
		if strings.HasSuffix(value, ":*") && strings.TrimSuffix(value, ":*") == name {
			return true
		}
	}
	return false
}

func hostName(host string) string {
	value, _, err := net.SplitHostPort(host)
	if err == nil {
		return value
	}
	return strings.Trim(host, "[]")
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
