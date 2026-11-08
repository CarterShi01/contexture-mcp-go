package server_test

import (
	"context"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
	"github.com/CarterShi01/contexture-mcp-go/introspection"
	"github.com/CarterShi01/contexture-mcp-go/server"
)

func TestStaticCompiledApplicationCatalogAndDebugDoNotChangeGateway(t *testing.T) {
	app, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "static", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "ops", Description: "ops", Instructions: "ops", Tools: []contexture.Factory{func() contexture.Node {
			tool, err := contexture.NewTool("read", "read", true, func(context.Context, struct{}) (bool, error) { t.Fatal("business handler invoked"); return false, nil })
			if err != nil {
				panic(err)
			}
			return tool
		}}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := server.CompileApplication(app)
	if err != nil {
		t.Fatal(err)
	}
	structural, err := server.CompileDisclosureApplication(app)
	if err != nil {
		t.Fatal(err)
	}
	for _, bound := range []bool{true, false} {
		var catalog *introspection.NodeCatalog
		if bound {
			catalog, err = runtime.Catalog(introspection.CatalogOptions{View: introspection.ModelView, Detail: introspection.DefinitionDetail})
		} else {
			catalog, err = structural.Catalog(introspection.CatalogOptions{View: introspection.ModelView, Detail: introspection.DefinitionDetail})
		}
		if err != nil {
			t.Fatal(err)
		}
		tool, err := catalog.Get("ops/read")
		if err != nil || tool.Tool.ExecutionAvailable != bound {
			t.Fatal("container binding mismatch", err)
		}
	}
	debug, err := runtime.Debug(introspection.CatalogOptions{View: introspection.ApplicationView, Detail: introspection.DefinitionDetail})
	if err != nil || debug == nil {
		t.Fatal(err)
	}
	debug, err = structural.Debug(introspection.CatalogOptions{View: introspection.ApplicationView})
	if err != nil || debug == nil {
		t.Fatal(err)
	}
	gateway, err := runtime.Gateway()
	if err != nil {
		t.Fatal(err)
	}
	if gateway == nil || len(contexture.GatewayToolNames()) != 5 {
		t.Fatal("Catalog changed fixed gateway")
	}
	if _, err := (*server.RuntimeApplication)(nil).Catalog(introspection.CatalogOptions{View: introspection.ModelView}); err == nil {
		t.Fatal("nil runtime accepted")
	}
	if _, err := (*server.DisclosureApplication)(nil).Debug(introspection.CatalogOptions{View: introspection.ModelView}); err == nil {
		t.Fatal("nil structural accepted")
	}
}
