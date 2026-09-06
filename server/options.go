package server

import (
	"fmt"
	"strings"
)

// Transport identifies the supported Contexture Host transports.
type Transport string

const (
	StdioTransport          Transport = "stdio"
	StreamableHTTPTransport Transport = "streamable-http"
	DefaultHost                       = "127.0.0.1"
	DefaultPort                       = 8000
	DefaultPath                       = "/mcp"
)

// ServeError reports an unsafe or contradictory server startup request.
type ServeError struct{ Message string }

func (err *ServeError) Error() string { return err.Message }

// ContextureOptions is the validated transport policy shared by CLI and server APIs.
type ContextureOptions struct {
	Transport      Transport
	Host           string
	Port           int
	Path           string
	AllowedHosts   []string
	AllowedOrigins []string
	AllowAnonymous bool

	hostSet bool
	portSet bool
	pathSet bool
}

// NewContextureOptions applies safe local defaults and rejects silent no-op options.
func NewContextureOptions(options ContextureOptions) (*ContextureOptions, error) {
	options.AllowedHosts = append([]string(nil), options.AllowedHosts...)
	options.AllowedOrigins = append([]string(nil), options.AllowedOrigins...)
	options.hostSet = options.Host != ""
	options.portSet = options.Port != 0
	options.pathSet = options.Path != ""
	if options.Transport == "" {
		options.Transport = StdioTransport
	}
	if options.Host == "" {
		options.Host = DefaultHost
	}
	if options.Port == 0 {
		options.Port = DefaultPort
	}
	if options.Path == "" {
		options.Path = DefaultPath
	}
	if options.Transport == StdioTransport {
		stated := []string{}
		if options.hostSet {
			stated = append(stated, "host")
		}
		if options.portSet {
			stated = append(stated, "port")
		}
		if options.pathSet {
			stated = append(stated, "path")
		}
		if len(options.AllowedHosts) > 0 {
			stated = append(stated, "allowed_hosts")
		}
		if len(options.AllowedOrigins) > 0 {
			stated = append(stated, "allowed_origins")
		}
		if options.AllowAnonymous {
			stated = append(stated, "allow_anonymous")
		}
		if len(stated) > 0 {
			return nil, &ServeError{Message: fmt.Sprintf("transport='stdio' cannot use %s: stdio has no address to bind or HTTP request to authenticate.", strings.Join(stated, ", "))}
		}
		return &options, nil
	}
	if options.Transport != StreamableHTTPTransport {
		return nil, &ServeError{Message: fmt.Sprintf("unknown Contexture transport %q", options.Transport)}
	}
	if options.Port < 0 || options.Port > 65535 {
		return nil, &ServeError{Message: "port must be an integer from 0 through 65535."}
	}
	if !strings.HasPrefix(options.Path, "/") {
		return nil, &ServeError{Message: "path must begin with /."}
	}
	if !isLoopback(options.Host) {
		if len(options.AllowedHosts) == 0 && len(options.AllowedOrigins) == 0 {
			return nil, &ServeError{Message: fmt.Sprintf("host=%q is not loopback; state allowed_hosts and/or allowed_origins for DNS rebinding protection.", options.Host)}
		}
		if !options.AllowAnonymous {
			return nil, &ServeError{Message: fmt.Sprintf("host=%q is not loopback. State allow_anonymous=true only when unauthenticated access is intentional.", options.Host)}
		}
	}
	return &options, nil
}

// URL is the public MCP endpoint once a streamable HTTP listener starts.
func (options *ContextureOptions) URL() string {
	if options == nil {
		return ""
	}
	return fmt.Sprintf("http://%s:%d%s", options.Host, options.Port, options.Path)
}

func isLoopback(host string) bool {
	switch host {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	default:
		return false
	}
}
