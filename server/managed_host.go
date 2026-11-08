package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/managed"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ManagedHostProfile is LOCAL opt-in negotiation, not a normative spec revision.
const ManagedHostProfile = "contexture.go-native-host-candidate/v1"
const (
	ManagedProfileMeta     = "contexture/profile"
	ManagedReferenceMeta   = "contexture/reference"
	ManagedProfileHeader   = "Contexture-Managed-Profile"
	ManagedReferenceHeader = "Contexture-Reference"
	ManagedRunHeader       = "Contexture-Run"
)

type ManagedHostInput struct {
	Transport string
	Request   *http.Request
	Method    string
}
type ManagedHostScope = managed.ManagedRequestOptions

// ManagedTransportHost authenticates explicit contexts and CURRENT policy and
// selection. Fence must serialize the final dispatch with those same authorities.
// Both callbacks must run exactly once synchronously; neither may be retained.
// Runtime RequestIdentity/RequestAuthority must independently attest current
// facts from the supplied context, never cached grants or author wire data.
type ManagedTransportHost interface {
	Enter(context.Context, ManagedHostInput, func(context.Context, ManagedHostScope) error) error
	Fence(context.Context, ManagedHostInput, func(context.Context, ManagedHostScope) error) error
}

type ManagedRunCarrier interface {
	managedRunCarrier()
}
type ManagedChannelCarrier struct {
	Current func(context.Context, ManagedHostInput) (*managed.RunLeaseContext, error)
}

func (ManagedChannelCarrier) managedRunCarrier() {}

type ManagedHeaderCarrier struct {
	Approve func(context.Context, string, ManagedHostInput) (*managed.RunLeaseContext, error)
}

func (ManagedHeaderCarrier) managedRunCarrier() {}

type ManagedNavigationMount struct{ Path, Operation string }
type ManagedHostOptions struct {
	Identity         Identity
	Runtime          *managed.ManagedRuntime
	Host             ManagedTransportHost
	Runs             *managed.RunLeaseService
	Carrier          ManagedRunCarrier
	MCPPath          string
	Navigation       []ManagedNavigationMount
	MaxBodyBytes     int64
	MaxResponseBytes int
}

type managedHTTPRequestKey struct{}
type ManagedHostAdapter struct {
	options             ManagedHostOptions
	server              *mcp.Server
	http                http.Handler
	mu                  sync.Mutex
	closing             bool
	active              sync.WaitGroup
	ctx                 context.Context
	cancel              context.CancelFunc
	sessions            []*mcp.ServerSession
	sessionCancellation []func() bool
	listener            net.Listener
	httpServer          *http.Server
	httpDone            chan struct{}
	httpErr             error
	done                chan struct{}
	closeErr            error
}

var managedToken = regexp.MustCompile(`^[A-Za-z0-9_-]{16,512}$`)
var navigationMethods = map[string]string{
	"routes": "GET", "prompts": "GET", "resources": "GET",
	"inspect": "POST", "open": "POST", "members": "POST", "uses": "POST",
	"invoke": "POST", "invoke_read_only": "POST", "prompt.get": "POST", "resource.read": "POST",
}

func managedPath(path string) bool {
	return strings.HasPrefix(path, "/") && !strings.ContainsAny(path, "?#\\%") &&
		!strings.Contains(path, "//") && !strings.Contains(path, "/.") &&
		(path == "/" || !strings.HasSuffix(path, "/"))
}

func NewManagedHostAdapter(options ManagedHostOptions) (*ManagedHostAdapter, error) {
	if options.Runtime == nil || options.Host == nil || options.Identity.Name == "" || options.Identity.Version == "" {
		return nil, errors.New("managed Host requires identity, runtime and trusted Host")
	}
	if (options.Runs == nil) != (options.Carrier == nil) || (options.Runs != nil && !options.Runs.IsFor(options.Runtime)) {
		return nil, errors.New("managed run service and carrier must share this runtime")
	}
	switch carrier := options.Carrier.(type) {
	case nil:
	case ManagedChannelCarrier:
		if carrier.Current == nil {
			return nil, errors.New("protected channel callback required")
		}
	case ManagedHeaderCarrier:
		if carrier.Approve == nil {
			return nil, errors.New("current header approval callback required")
		}
	default:
		return nil, errors.New("unsupported managed run carrier")
	}
	if options.MCPPath == "" {
		options.MCPPath = "/mcp"
	}
	if !managedPath(options.MCPPath) {
		return nil, errors.New("exact managed MCP path required")
	}
	if options.MaxBodyBytes == 0 {
		options.MaxBodyBytes = 1 << 20
	}
	if options.MaxResponseBytes == 0 {
		options.MaxResponseBytes = 8 << 20
	}
	if options.MaxBodyBytes < 1 || options.MaxResponseBytes < 1 {
		return nil, errors.New("positive managed Host byte bounds required")
	}
	options.Navigation = slices.Clone(options.Navigation)
	paths := map[string]bool{options.MCPPath: true}
	for _, mount := range options.Navigation {
		if !managedPath(mount.Path) || paths[mount.Path] || navigationMethods[mount.Operation] == "" {
			return nil, errors.New("unique explicit managed navigation mounts required")
		}
		paths[mount.Path] = true
	}
	ctx, cancel := context.WithCancel(context.Background())
	h := &ManagedHostAdapter{options: options, ctx: ctx, cancel: cancel}
	h.server = mcp.NewServer(&mcp.Implementation{Name: options.Identity.Name, Version: options.Identity.Version,
		Title: options.Identity.Title, Description: options.Identity.Description},
		&mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{
			Tools: &mcp.ToolCapabilities{}, Prompts: &mcp.PromptCapabilities{},
			Resources: &mcp.ResourceCapabilities{}, Completions: &mcp.CompletionCapabilities{},
			Experimental: map[string]any{ManagedProfileMeta: ManagedHostProfile},
		}})
	for _, name := range []contexture.GatewayName{contexture.DiscoverGatewayName, contexture.InspectGatewayName,
		contexture.OpenGatewayName, contexture.InvokeReadOnlyGatewayName, contexture.InvokeGatewayName} {
		schema := gatewaySchema(name)
		schema["additionalProperties"] = false
		h.server.AddTool(&mcp.Tool{Name: string(name), InputSchema: schema}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return nil, errors.New("managed receiving dispatch required")
		})
	}
	h.server.AddReceivingMiddleware(h.middleware)
	h.http = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return h.server },
		&mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, MaxRequestBodyBytes: options.MaxBodyBytes,
			PropagateRequestCancellation: true})
	return h, nil
}

// oneHostDispatch joins work even when a broken trusted port returns early.
func oneHostDispatch(port func(func(context.Context, ManagedHostScope) error) error,
	action func(context.Context, ManagedHostScope) error) error {
	var mu sync.Mutex
	called, returned, repeated := false, false, false
	done := make(chan struct{})
	var dispatched error
	outer := port(func(ctx context.Context, scope ManagedHostScope) error {
		mu.Lock()
		if returned || called {
			repeated = true
			mu.Unlock()
			return managed.ErrDenied
		}
		called = true
		mu.Unlock()
		err := action(ctx, ManagedHostScope{Selection: slices.Clone(scope.Selection),
			PublicationSelection: slices.Clone(scope.PublicationSelection)})
		mu.Lock()
		dispatched = err
		close(done)
		mu.Unlock()
		return err
	})
	mu.Lock()
	returned = true
	didCall := called
	early := false
	if didCall {
		select {
		case <-done:
		default:
			early = true
		}
	}
	mu.Unlock()
	if didCall {
		<-done
	}
	mu.Lock()
	defer mu.Unlock()
	if !didCall || repeated || early {
		return errors.Join(outer, dispatched, managed.ErrDenied)
	}
	return errors.Join(outer, dispatched)
}

func (h *ManagedHostAdapter) begin(ctx context.Context) (context.Context, func(), error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return nil, nil, managed.ErrClosed
	}
	h.active.Add(1)
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(h.ctx, cancel)
	return ctx, func() { stop(); cancel(); h.active.Done() }, nil
}

func (h *ManagedHostAdapter) dispatch(ctx context.Context, input ManagedHostInput,
	action func(*managed.ManagedRequest) (any, error)) (result any, err error) {
	ctx, finish, err := h.begin(ctx)
	if err != nil {
		return nil, err
	}
	defer finish()
	err = oneHostDispatch(func(call func(context.Context, ManagedHostScope) error) error {
		return h.options.Host.Enter(ctx, input, call)
	}, func(hostCtx context.Context, scope ManagedHostScope) error {
		if hostCtx == nil {
			return managed.ErrDenied
		}
		identity, err := h.options.Runtime.CurrentIdentity(hostCtx)
		if err != nil {
			return err
		}
		var run *managed.RunLeaseContext
		header := ""
		if input.Request != nil {
			header = input.Request.Header.Get(ManagedRunHeader)
		}
		switch carrier := h.options.Carrier.(type) {
		case ManagedChannelCarrier:
			if header != "" {
				return managed.ErrDenied
			}
			run, err = carrier.Current(hostCtx, input)
		case ManagedHeaderCarrier:
			if header != "" {
				if input.Transport == "stdio" || !managedToken.MatchString(header) {
					return managed.ErrDenied
				}
				run, err = carrier.Approve(hostCtx, header, input)
				if err == nil && run == nil {
					return managed.ErrDenied
				}
			}
		default:
			if header != "" {
				return managed.ErrDenied
			}
		}
		if err != nil {
			return err
		}
		work := func(q *managed.ManagedRequest) error {
			if err := q.Attenuate(scope); err != nil {
				return err
			}
			if err := q.AssertHostMount(h.options.MCPPath); err != nil {
				return err
			}
			value, err := action(q)
			if err != nil {
				return err
			}
			bytes, err := json.Marshal(value)
			if err != nil {
				return err
			}
			if len(bytes) > h.options.MaxResponseBytes {
				return errors.New("managed response exceeds byte budget")
			}
			err = oneHostDispatch(func(call func(context.Context, ManagedHostScope) error) error {
				return h.options.Host.Fence(hostCtx, input, call)
			}, func(fenceCtx context.Context, currentScope ManagedHostScope) error {
				if fenceCtx == nil {
					return managed.ErrDenied
				}
				current, err := h.options.Runtime.CurrentIdentity(fenceCtx)
				if err != nil || current.ActorID != identity.ActorID || current.TenantID != identity.TenantID {
					return managed.ErrDenied
				}
				anchored, err := h.options.Runtime.CurrentIdentity(hostCtx)
				if err != nil || current != anchored {
					return managed.ErrDenied
				}
				if err := q.Attenuate(currentScope); err != nil {
					return err
				}
				if run != nil {
					if err := h.options.Runs.CheckCurrent(fenceCtx, run); err != nil {
						return err
					}
				}
				return q.ValidateReturn()
			})
			if err == nil {
				result = value
			}
			return err
		}
		if run != nil {
			return h.options.Runs.Request(hostCtx, run, work)
		}
		return h.options.Runtime.Request(hostCtx, scope, work)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func managedEnvelope(q *managed.ManagedRequest, value any) map[string]any {
	c := q.Coordinates()
	return map[string]any{"graph_id": c.GraphID, "revision": c.Revision, "generation_uuid": c.GenerationUUID,
		"boot_id": c.BootID, "target_id": c.TargetID, "instance_id": c.InstanceID, "locale": c.Locale, "value": value}
}

func (h *ManagedHostAdapter) middleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
		if method == "initialize" || method == "ping" || strings.HasPrefix(method, "notifications/") {
			return next(ctx, method, request)
		}
		meta := request.GetParams().GetMeta()
		if meta[ManagedProfileMeta] != ManagedHostProfile {
			return nil, errors.New("explicit local managed Host profile required")
		}
		for key := range meta {
			if key != ManagedProfileMeta && key != ManagedReferenceMeta {
				return nil, errors.New("unsupported managed metadata carrier")
			}
		}
		httpRequest, _ := ctx.Value(managedHTTPRequestKey{}).(*http.Request)
		input := ManagedHostInput{Transport: "stdio", Request: httpRequest, Method: method}
		if httpRequest != nil {
			input.Transport = "http"
		}
		value, err := h.dispatch(ctx, input, func(q *managed.ManagedRequest) (any, error) {
			return h.mcpResult(ctx, method, request, q, next)
		})
		if err != nil {
			if method == "tools/call" {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "managed request refused"}}}, nil
			}
			return nil, errors.New("managed request refused")
		}
		result, ok := value.(mcp.Result)
		if !ok {
			return nil, errors.New("invalid managed SDK result")
		}
		return result, nil
	}
}

func (h *ManagedHostAdapter) mcpResult(ctx context.Context, method string, request mcp.Request,
	q *managed.ManagedRequest, next mcp.MethodHandler) (mcp.Result, error) {
	ref, _ := request.GetParams().GetMeta()[ManagedReferenceMeta].(string)
	switch method {
	case "tools/list":
		if request.(*mcp.ListToolsRequest).Params.Cursor != "" {
			return nil, managed.ErrDenied
		}
		return next(ctx, method, request)
	case "tools/call":
		params := request.(*mcp.CallToolRequest).Params
		operation := ""
		switch contexture.GatewayName(params.Name) {
		case contexture.DiscoverGatewayName:
			operation = "discover"
		case contexture.InspectGatewayName:
			operation = "inspect"
		case contexture.OpenGatewayName:
			operation = "open"
		case contexture.InvokeReadOnlyGatewayName:
			operation = "invoke_read_only"
		case contexture.InvokeGatewayName:
			operation = "invoke"
		default:
			return nil, managed.ErrDenied
		}
		value, err := managedNavigation(q, operation, params.Arguments)
		if err != nil {
			return nil, err
		}
		envelope := managedEnvelope(q, value)
		bytes, err := json.Marshal(envelope)
		if err != nil {
			return nil, err
		}
		return &mcp.CallToolResult{StructuredContent: envelope, Meta: mcp.Meta(envelope),
			Content: []mcp.Content{&mcp.TextContent{Text: string(bytes)}}}, nil
	case "prompts/list":
		if request.(*mcp.ListPromptsRequest).Params.Cursor != "" {
			return nil, managed.ErrDenied
		}
		publications, err := q.ListPublications("prompt")
		if err != nil {
			return nil, err
		}
		prompts := []*mcp.Prompt{}
		for _, p := range publications {
			ref, err := q.Reference(p.OpensID)
			if err != nil {
				return nil, err
			}
			prompts = append(prompts, &mcp.Prompt{Name: p.Name, Description: p.Description, Meta: mcp.Meta{ManagedReferenceMeta: ref}})
		}
		return &mcp.ListPromptsResult{Prompts: prompts}, nil
	case "prompts/get":
		params := request.(*mcp.GetPromptRequest).Params
		if len(params.Arguments) != 0 {
			return nil, managed.ErrDenied
		}
		value, err := q.Prompt(params.Name, ref)
		if err != nil {
			return nil, err
		}
		bytes, err := json.Marshal(value.JSON())
		if err != nil {
			return nil, err
		}
		return &mcp.GetPromptResult{Meta: mcp.Meta(managedEnvelope(q, nil)),
			Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: string(bytes)}}}}, nil
	case "resources/list":
		if request.(*mcp.ListResourcesRequest).Params.Cursor != "" {
			return nil, managed.ErrDenied
		}
		publications, err := q.ListPublications("resource")
		if err != nil {
			return nil, err
		}
		resources := []*mcp.Resource{}
		for _, p := range publications {
			ref, err := q.Reference(p.OpensID)
			if err != nil {
				return nil, err
			}
			resources = append(resources, &mcp.Resource{Name: p.Name, URI: p.URI, Description: p.Description,
				MIMEType: p.MIMEType, Meta: mcp.Meta{ManagedReferenceMeta: ref}})
		}
		return &mcp.ListResourcesResult{Resources: resources}, nil
	case "resources/read":
		params := request.(*mcp.ReadResourceRequest).Params
		text, err := q.ReadResource(params.URI, ref)
		if err != nil {
			return nil, err
		}
		return &mcp.ReadResourceResult{Meta: mcp.Meta(managedEnvelope(q, nil)),
			Contents: []*mcp.ResourceContents{{URI: params.URI, Text: text}}}, nil
	case "resources/templates/list":
		if request.(*mcp.ListResourceTemplatesRequest).Params.Cursor != "" {
			return nil, managed.ErrDenied
		}
		return &mcp.ListResourceTemplatesResult{ResourceTemplates: []*mcp.ResourceTemplate{}}, nil
	case "completion/complete":
		return &mcp.CompleteResult{Completion: mcp.CompletionResultDetails{Values: []string{}}}, nil
	default:
		return nil, errors.New("unsupported managed MCP operation")
	}
}

// ConnectStdio defaults to official SDK OS stdio. IOTransport is also official
// SDK framing, useful for embedding with owned pipes; no emulated SDK is used.
func (h *ManagedHostAdapter) ConnectStdio(ctx context.Context, transport mcp.Transport) (*mcp.ServerSession, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing {
		return nil, managed.ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if transport == nil {
		transport = &mcp.StdioTransport{}
	}
	// Connection cancellation belongs to the Host drain, not the SDK's eager
	// connection close. Entered native operations still use cancellable contexts.
	session, err := h.server.Connect(context.WithoutCancel(ctx), transport, nil)
	if err != nil {
		return nil, err
	}
	h.sessions = append(h.sessions, session)
	h.sessionCancellation = append(h.sessionCancellation, context.AfterFunc(ctx, func() { _ = h.Close() }))
	return session, nil
}

func (h *ManagedHostAdapter) ServeStdio(ctx context.Context) error {
	session, err := h.ConnectStdio(context.Background(), nil)
	if err != nil {
		return err
	}
	wait := make(chan error, 1)
	go func() { wait <- session.Wait() }()
	select {
	case err = <-wait:
	case <-ctx.Done():
		err = ctx.Err()
	}
	return errors.Join(err, h.Close())
}

// Listen binds literal loopback only. Handler is the separately mountable face.
func (h *ManagedHostAdapter) Listen(port int) (string, error) {
	if port < 0 || port > 65535 {
		return "", errors.New("invalid loopback port")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closing || h.listener != nil {
		return "", managed.ErrClosed
	}
	listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return "", err
	}
	h.listener = listener
	h.httpServer = &http.Server{Handler: h.Handler()}
	h.httpDone = make(chan struct{})
	go func() {
		err := h.httpServer.Serve(listener)
		h.mu.Lock()
		defer h.mu.Unlock()
		if !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			h.httpErr = err
		}
		close(h.httpDone)
	}()
	return "http://" + listener.Addr().String(), nil
}

// Close stops admission, cancels work and joins leases before closing transports.
// Caller owns runs, coordinator and Journal; close those only after this returns.
func (h *ManagedHostAdapter) Close() error {
	h.mu.Lock()
	if h.done != nil {
		done := h.done
		h.mu.Unlock()
		<-done
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.closeErr
	}
	h.closing = true
	h.done = make(chan struct{})
	h.cancel()
	listener, httpServer, httpDone := h.listener, h.httpServer, h.httpDone
	sessions := slices.Clone(h.sessions)
	stops := slices.Clone(h.sessionCancellation)
	h.mu.Unlock()
	for _, stop := range stops {
		stop()
	}
	if listener != nil {
		_ = listener.Close()
	}
	h.active.Wait()
	var err error
	if httpServer != nil {
		err = httpServer.Shutdown(context.Background())
		if errors.Is(err, net.ErrClosed) {
			err = nil
		}
		<-httpDone
		h.mu.Lock()
		err = errors.Join(err, h.httpErr)
		h.mu.Unlock()
	}
	for _, session := range sessions {
		err = errors.Join(err, session.Close())
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closeErr = err
	close(h.done)
	return err
}
