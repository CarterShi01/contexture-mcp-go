package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const packageVersion = "0.12.0rc1"

// ApplicationServer owns one compiled declaration and its official MCP transport assembly.
type ApplicationServer struct {
	application *RuntimeApplication
	identity    Identity
}

type rootSelectionContextKey struct{}

// BuildServer compiles one lazy declaration for serving.
func BuildServer(application *contexture.Application) (*ApplicationServer, error) {
	compiled, err := CompileApplication(application)
	if err != nil {
		return nil, err
	}
	return &ApplicationServer{application: compiled, identity: Identity{Name: application.Name(), Version: packageVersion}}, nil
}

// Build constructs a fresh official-SDK server for one Contexture transport service.
func (server *ApplicationServer) Build() (*ContextureMCPServer, error) {
	return server.BuildForRoots(contexture.AllRoots())
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
	return NewContextureMCPServerForRoots(server.identity, gateway, selection, server.application.Publications), nil
}

// Start blocks while serving stdio or streamable HTTP with Channels open for the lifetime.
func (server *ApplicationServer) Start(ctx context.Context, options *ContextureOptions) error {
	return server.StartWithAuthAndRootSelector(ctx, options, nil, nil)
}

// StartWithAuthAndRootSelector starts one transport with optional HTTP bearer
// identity and request-local root attenuation. Those HTTP-only policies are
// rejected for stdio rather than silently ignored.
func (server *ApplicationServer) StartWithAuthAndRootSelector(ctx context.Context, options *ContextureOptions, identity *Auth, selector RootSelector) error {
	if options == nil {
		options = &ContextureOptions{}
	}
	validated, err := NewContextureOptions(*options)
	if err != nil {
		return err
	}
	options = validated
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
	return server.ServeListenerWithAuthAndRootSelector(ctx, listener, options, identity, selector)
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
	validated, err := NewContextureOptions(*options)
	if err != nil {
		return err
	}
	options = validated
	if options.Transport != StreamableHTTPTransport {
		return &ServeError{Message: "ServeListener requires transport='streamable-http'."}
	}
	gateway, err := server.application.Gateway()
	if err != nil {
		return err
	}
	handler := mcp.NewStreamableHTTPHandler(func(request *http.Request) *mcp.Server {
		selection := contexture.AllRoots()
		if selected, ok := request.Context().Value(rootSelectionContextKey{}).(contexture.RootSelection); ok {
			selection = selected
		}
		return NewContextureMCPServerForRoots(server.identity, gateway, selection, server.application.Publications).Server
	}, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
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
		if len(options.AllowedHosts) > 0 && !contains(options.AllowedHosts, hostName(request.Host)) {
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
