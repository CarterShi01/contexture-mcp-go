package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/CarterShi01/contexture-mcp-go/managed"
)

func managedArguments(raw []byte, required []string, optional []string) (map[string]any, error) {
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	value, err := managed.DecodeJSON(raw)
	if err != nil {
		return nil, err
	}
	args, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("managed arguments must be object")
	}
	for key := range args {
		found := false
		for _, allowed := range append(append([]string{}, required...), optional...) {
			if key == allowed {
				found = true
			}
		}
		if !found {
			return nil, errors.New("unsupported managed argument")
		}
	}
	for _, key := range required {
		if _, ok := args[key]; !ok {
			return nil, errors.New("missing managed argument")
		}
	}
	return args, nil
}

func managedNavigation(q *managed.ManagedRequest, operation string, raw []byte) (any, error) {
	required, optional := []string{}, []string{}
	switch operation {
	case "discover", "routes", "prompts", "resources":
	case "inspect":
		required = []string{"refs"}
	case "open", "members", "uses":
		required = []string{"ref"}
	case "invoke", "invoke_read_only":
		required, optional = []string{"ref"}, []string{"arguments"}
	case "prompt.get":
		required = []string{"name", "ref"}
	case "resource.read":
		required = []string{"uri", "ref"}
	default:
		return nil, errors.New("unsupported managed gateway operation")
	}
	args, err := managedArguments(raw, required, optional)
	if err != nil {
		return nil, err
	}
	for _, key := range required {
		if key == "refs" {
			continue
		}
		if value, ok := args[key].(string); !ok || value == "" {
			return nil, errors.New("managed reference/name must be string")
		}
	}
	ref, _ := args["ref"].(string)
	switch operation {
	case "discover", "routes":
		value, err := q.Routes()
		return value.JSON(), err
	case "inspect":
		refs, ok := args["refs"].([]any)
		if !ok {
			return nil, errors.New("managed refs must be array")
		}
		result := []any{}
		for _, rawRef := range refs {
			ref, ok := rawRef.(string)
			if !ok {
				return nil, errors.New("managed ref must be string")
			}
			value, err := q.Inspect(ref)
			if err != nil {
				return nil, err
			}
			result = append(result, value.JSON())
		}
		return result, nil
	case "open":
		value, err := q.Open(ref)
		return value.JSON(), err
	case "members", "uses":
		value, err := q.Relation(ref, operation)
		return value.JSON(), err
	case "invoke", "invoke_read_only":
		arguments := args["arguments"]
		if arguments == nil {
			arguments = map[string]any{}
		}
		if _, ok := arguments.(map[string]any); !ok {
			return nil, errors.New("business arguments must be object")
		}
		value, err := q.Invoke(ref, arguments, operation == "invoke_read_only")
		return value.JSON(), err
	case "prompts", "resources":
		kind := "prompt"
		if operation == "resources" {
			kind = "resource"
		}
		publications, err := q.ListPublications(kind)
		if err != nil {
			return nil, err
		}
		result := []any{}
		for _, publication := range publications {
			ref, err := q.Reference(publication.OpensID)
			if err != nil {
				return nil, err
			}
			item := map[string]any{"name": publication.Name, "description": publication.Description, "reference": ref}
			if kind == "resource" {
				item["uri"], item["mime_type"] = publication.URI, publication.MIMEType
			}
			result = append(result, item)
		}
		return result, nil
	case "prompt.get":
		value, err := q.Prompt(args["name"].(string), ref)
		return value.JSON(), err
	case "resource.read":
		return q.ReadResource(args["uri"].(string), ref)
	}
	return nil, errors.New("unsupported managed navigation")
}

func singleManagedHeaders(request *http.Request) error {
	for _, name := range []string{ManagedProfileHeader, ManagedReferenceHeader, ManagedRunHeader} {
		values := request.Header.Values(name)
		if len(values) > 1 || (len(values) == 1 && (values[0] == "" || strings.Contains(values[0], ","))) {
			return errors.New("ambiguous protected managed header")
		}
	}
	return nil
}

// Handler is mountable, with current Host authentication on every dispatch.
// External listeners retain responsibility for TLS and network admission.
func (h *ManagedHostAdapter) Handler() http.Handler {
	protection := http.NewCrossOriginProtection()
	return protection.Handler(http.HandlerFunc(h.serveHTTP))
}

func (h *ManagedHostAdapter) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	if singleManagedHeaders(request) != nil || request.Header.Get(ManagedProfileHeader) != ManagedHostProfile ||
		request.URL.RawQuery != "" || request.URL.RawPath != "" || request.URL.User != nil ||
		request.URL.IsAbs() {
		http.Error(writer, "managed request refused", http.StatusBadRequest)
		return
	}
	// Preserve official SDK localhost/DNS-rebinding protection for MCP. REST
	// must share that boundary rather than bypass it.
	if request.Header.Get("Origin") != "" && request.Header.Get("Origin") != "http://"+request.Host &&
		request.Header.Get("Origin") != "https://"+request.Host {
		http.Error(writer, "managed origin refused", http.StatusForbidden)
		return
	}
	host := request.Host
	if strings.Contains(host, "@") || host == "" {
		http.Error(writer, "managed host refused", http.StatusForbidden)
		return
	}
	h.mu.Lock()
	listener := h.listener
	closing := h.closing
	h.mu.Unlock()
	if closing {
		http.Error(writer, "managed Host closed", http.StatusServiceUnavailable)
		return
	}
	if listener != nil && request.Host != listener.Addr().String() {
		http.Error(writer, "managed host refused", http.StatusForbidden)
		return
	}
	request.Body = http.MaxBytesReader(writer, request.Body, h.options.MaxBodyBytes)
	if request.URL.Path == h.options.MCPPath {
		request = request.WithContext(context.WithValue(request.Context(), managedHTTPRequestKey{}, request))
		h.http.ServeHTTP(writer, request)
		return
	}
	var mount *ManagedNavigationMount
	for i := range h.options.Navigation {
		if h.options.Navigation[i].Path == request.URL.Path {
			mount = &h.options.Navigation[i]
			break
		}
	}
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		http.Error(writer, "managed body refused", http.StatusRequestEntityTooLarge)
		return
	}
	if mount != nil && request.Method != navigationMethods[mount.Operation] {
		http.Error(writer, "managed method refused", http.StatusMethodNotAllowed)
		return
	}
	input := ManagedHostInput{Transport: "rest", Request: request, Method: request.Method}
	value, err := h.dispatch(request.Context(), input, func(q *managed.ManagedRequest) (any, error) {
		var value any
		var err error
		if mount != nil {
			if err := q.AssertHostMount(mount.Path); err != nil {
				return nil, err
			}
			value, err = managedNavigation(q, mount.Operation, raw)
		} else {
			if request.Method == "GET" || request.Method == "HEAD" {
				if len(raw) != 0 {
					return nil, errors.New("read route cannot carry body")
				}
				raw = []byte("{}")
			} else if request.Header.Get("Content-Type") != "application/json" {
				return nil, errors.New("published write route requires JSON")
			}
			args, decodeErr := managed.DecodeJSON(raw)
			if decodeErr != nil {
				return nil, decodeErr
			}
			result, callErr := q.Route(request.Method, request.URL.Path, request.Header.Get(ManagedReferenceHeader), args)
			value, err = result.JSON(), callErr
		}
		if err != nil {
			return nil, err
		}
		return managedEnvelope(q, value), nil
	})
	if err != nil {
		http.Error(writer, "managed request refused", http.StatusBadRequest)
		return
	}
	envelope, ok := value.(map[string]any)
	if !ok {
		http.Error(writer, "managed result refused", http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Contexture-Generation", envelope["generation_uuid"].(string))
	if request.Method == "HEAD" {
		writer.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(writer).Encode(value)
}
