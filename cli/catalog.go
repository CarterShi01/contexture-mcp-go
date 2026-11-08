package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/debug"
	"github.com/CarterShi01/contexture-mcp-go/introspection"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

type catalogArguments struct {
	refs    []string
	options introspection.CatalogOptions
	json    bool
	host    string
	port    int
	target  string
}

func parseCatalogArguments(arguments []string, isDebug bool) (catalogArguments, error) {
	parsed := catalogArguments{
		options: introspection.CatalogOptions{View: introspection.ModelView, Detail: introspection.SummaryDetail},
		host:    "127.0.0.1", port: 8787,
	}
	if isDebug {
		parsed.options.View, parsed.options.Detail = introspection.ApplicationView, introspection.DefinitionDetail
	}
	selectors := []string{}
	for len(arguments) > 0 {
		option := arguments[0]
		arguments = arguments[1:]
		if option == "--" {
			parsed.refs = append(parsed.refs, arguments...)
			break
		}
		if !strings.HasPrefix(option, "-") {
			parsed.refs = append(parsed.refs, option)
			continue
		}
		if option == "--json" && !isDebug {
			parsed.json = true
			continue
		}
		if len(arguments) == 0 || strings.HasPrefix(arguments[0], "--") {
			return parsed, fmt.Errorf("Catalog option %s needs a value", option)
		}
		value := arguments[0]
		arguments = arguments[1:]
		switch option {
		case "--target":
			// This CLI is statically linked. No imports or module expressions
			// are evaluated; name or "application" identifies its one owner.
			if value == "" {
				return parsed, fmt.Errorf("Catalog target must name the statically linked application")
			}
			parsed.target = value
		case "--view":
			switch introspection.CatalogView(value) {
			case introspection.ApplicationView, introspection.ModelView, introspection.SurfaceView:
				parsed.options.View = introspection.CatalogView(value)
			default:
				return parsed, fmt.Errorf("unknown Catalog view")
			}
		case "--detail":
			switch introspection.CatalogDetail(value) {
			case introspection.SummaryDetail, introspection.DefinitionDetail:
				parsed.options.Detail = introspection.CatalogDetail(value)
			default:
				return parsed, fmt.Errorf("unknown Catalog detail")
			}
		case "--locale":
			parsed.options.Locale = &value
		case "--select":
			selectors = append(selectors, value)
		case "--host":
			if !isDebug || value == "" {
				return parsed, fmt.Errorf("--host is a debug-only literal loopback host")
			}
			parsed.host = value
		case "--port":
			if !isDebug {
				return parsed, fmt.Errorf("--port is debug-only")
			}
			port, err := strconv.Atoi(value)
			if err != nil || port < 0 || port > 65535 {
				return parsed, fmt.Errorf("debug port must be an integer between zero and 65535")
			}
			parsed.port = port
		default:
			return parsed, fmt.Errorf("unknown Catalog option %s", option)
		}
	}
	if (parsed.options.View == introspection.SurfaceView) != (len(selectors) != 0) {
		return parsed, fmt.Errorf("only surface Catalogs require one or more --select refs")
	}
	if len(selectors) != 0 {
		selection, err := contexture.OnlySurfaces(selectors...)
		if err != nil {
			return parsed, err
		}
		parsed.options.SurfaceSelection = &selection
	}
	if isDebug && len(parsed.refs) != 0 {
		return parsed, fmt.Errorf("debug does not accept positional refs")
	}
	return parsed, nil
}

func runCatalog(ctx context.Context, application *server.RuntimeApplication, arguments []string, stdout, stderr io.Writer, isDebug bool) int {
	parsed, err := parseCatalogArguments(arguments, isDebug)
	if err != nil {
		return fail(stderr, 2, err.Error())
	}
	if parsed.target != "" && parsed.target != "application" && parsed.target != application.Index.Name() {
		return fail(stderr, 2, "--target must name this statically linked application; module import strings are not supported")
	}
	catalog, err := application.Catalog(parsed.options)
	if err != nil {
		return fail(stderr, 1, err.Error())
	}
	if isDebug {
		ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
		defer stop()
		debugServer, err := debug.New(catalog)
		if err != nil {
			return fail(stderr, 1, err.Error())
		}
		address, err := debugServer.Start(ctx, debug.StartOptions{Host: parsed.host, Port: parsed.port})
		if err != nil {
			return fail(stderr, 1, err.Error())
		}
		_, _ = fmt.Fprintln(stdout, "Contexture Node Catalog: "+address.URL)
		<-ctx.Done()
		if err := address.Close(); err != nil {
			return fail(stderr, 1, err.Error())
		}
		return 0
	}
	indent := 2
	if len(parsed.refs) != 0 {
		nodes := make([]introspection.NodeDescriptor, len(parsed.refs))
		jsonNodes := make([]any, len(parsed.refs))
		for i, ref := range parsed.refs {
			nodes[i], err = catalog.Get(ref)
			if err != nil {
				return fail(stderr, 1, err.Error())
			}
			jsonNodes[i], err = nodes[i].AsJSON()
			if err != nil {
				return fail(stderr, 1, err.Error())
			}
		}
		if parsed.json {
			text, err := introspection.FormatJSON(jsonNodes, introspection.JSONOptions{Indent: &indent})
			if err != nil {
				return fail(stderr, 1, err.Error())
			}
			_, _ = fmt.Fprintln(stdout, text)
		} else {
			for _, node := range nodes {
				_, _ = fmt.Fprintf(stdout, "%-5s  %s  — %s\n", node.Kind, node.Ref, node.Description)
			}
		}
		return 0
	}
	snapshot := catalog.Snapshot()
	if parsed.json {
		text, err := snapshot.ToJSON(&indent)
		if err != nil {
			return fail(stderr, 1, err.Error())
		}
		_, _ = fmt.Fprintln(stdout, text)
		return 0
	}
	facts := snapshot.Application()
	_, _ = fmt.Fprintf(stdout, "%s  %s  %s/%s\n", facts.Name, facts.Mode, facts.View, facts.Detail)
	_, _ = fmt.Fprintln(stdout, "digest  "+snapshot.ContentDigest())
	for _, node := range snapshot.Nodes() {
		_, _ = fmt.Fprintf(stdout, "%s%-5s  %s  — %s\n", strings.Repeat("  ", strings.Count(node.Ref, "/")), node.Kind, node.Ref, node.Description)
	}
	return 0
}
