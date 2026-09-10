package server

import (
	"fmt"
	"net"
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
	// Auth is the HTTP bearer policy for this server. It is deliberately owned
	// by the transport options so a public bind cannot be validated before the
	// authentication decision is known.
	Auth           *Auth
	AllowAnonymous bool
	LogLevel       LogLevel
	// MaxRequestBodyBytes limits one streamable-MCP HTTP request. Zero selects
	// the official SDK's safe 4 MiB default; a negative value is refused rather
	// than silently disabling a network safety boundary.
	MaxRequestBodyBytes int64

	hostSet   bool
	portSet   bool
	pathSet   bool
	validated bool
}

// NewContextureOptions applies safe local defaults and rejects silent no-op options.
func NewContextureOptions(options ContextureOptions) (*ContextureOptions, error) {
	options.AllowedHosts = append([]string(nil), options.AllowedHosts...)
	options.AllowedOrigins = append([]string(nil), options.AllowedOrigins...)
	if options.Auth != nil {
		copied := *options.Auth
		copied.RequiredScopes = append([]string(nil), options.Auth.RequiredScopes...)
		options.Auth = &copied
	}
	if options.validated {
		// A validated value already contains effective defaults. Preserve whether
		// those fields were originally stated, while still noticing a caller that
		// changes one before asking a second boundary to validate it.
		options.hostSet = options.hostSet || (options.Host != "" && options.Host != DefaultHost)
		options.portSet = options.portSet || (options.Port != 0 && options.Port != DefaultPort)
		options.pathSet = options.pathSet || (options.Path != "" && options.Path != DefaultPath)
	} else {
		options.hostSet = options.Host != ""
		options.portSet = options.Port != 0
		options.pathSet = options.Path != ""
	}
	if options.Transport == "" {
		options.Transport = StdioTransport
	}
	if options.LogLevel == "" {
		options.LogLevel = InfoLogLevel
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
	options.validated = true
	if options.Transport == StdioTransport {
		if _, ok := slogLevel(options.LogLevel); !ok {
			return nil, &ServeError{Message: fmt.Sprintf("unknown Contexture log level %q", options.LogLevel)}
		}
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
		if options.Auth != nil {
			stated = append(stated, "auth")
		}
		if options.MaxRequestBodyBytes != 0 {
			stated = append(stated, "max_request_body_bytes")
		}
		if len(stated) > 0 {
			return nil, &ServeError{Message: fmt.Sprintf("transport='stdio' cannot use %s: stdio has no address to bind or HTTP request to authenticate.", strings.Join(stated, ", "))}
		}
		return &options, nil
	}
	if options.Transport != StreamableHTTPTransport {
		return nil, &ServeError{Message: fmt.Sprintf("unknown Contexture transport %q", options.Transport)}
	}
	if _, ok := slogLevel(options.LogLevel); !ok {
		return nil, &ServeError{Message: fmt.Sprintf("unknown Contexture log level %q", options.LogLevel)}
	}
	if options.Port < 0 || options.Port > 65535 {
		return nil, &ServeError{Message: "port must be an integer from 0 through 65535."}
	}
	if !strings.HasPrefix(options.Path, "/") || strings.ContainsAny(options.Path, "?#") {
		return nil, &ServeError{Message: "path must begin with /."}
	}
	if options.MaxRequestBodyBytes < 0 {
		return nil, &ServeError{Message: "max_request_body_bytes must be zero or a positive number of bytes."}
	}
	if options.Auth != nil {
		if err := options.Auth.validate(); err != nil {
			return nil, &ServeError{Message: fmt.Sprintf("invalid auth: %v", err)}
		}
	}
	if !isLoopback(options.Host) {
		if len(options.AllowedHosts) == 0 && len(options.AllowedOrigins) == 0 {
			return nil, &ServeError{Message: fmt.Sprintf("host=%q is not loopback; state allowed_hosts and/or allowed_origins for DNS rebinding protection.", options.Host)}
		}
		if options.Auth == nil && !options.AllowAnonymous {
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
	if parsed := net.ParseIP(strings.Trim(host, "[]")); parsed != nil {
		return parsed.IsLoopback()
	}
	switch host {
	case "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	default:
		return false
	}
}
