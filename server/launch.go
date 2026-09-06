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
	if server == nil || server.application == nil {
		return nil, fmt.Errorf("Contexture application server must not be nil")
	}
	gateway, err := server.application.Gateway()
	if err != nil {
		return nil, err
	}
	return NewContextureMCPServer(server.identity, gateway, server.application.Publications), nil
}

// Start blocks while serving stdio or streamable HTTP with Channels open for the lifetime.
func (server *ApplicationServer) Start(ctx context.Context, options *ContextureOptions) error {
	if options == nil {
		options = &ContextureOptions{}
	}
	validated, err := NewContextureOptions(*options)
	if err != nil {
		return err
	}
	options = validated
	if options.Transport == StdioTransport {
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
	return server.ServeListener(ctx, listener, options)
}

// ServeListener serves streamable HTTP through an existing listener; it is useful for embedding and tests.
func (server *ApplicationServer) ServeListener(ctx context.Context, listener net.Listener, options *ContextureOptions) error {
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
	adapter, err := server.Build()
	if err != nil {
		return err
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return adapter.Server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	httpServer := &http.Server{Handler: guarded(handler, options)}
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
