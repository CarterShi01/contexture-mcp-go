package server

import (
	"fmt"

	"github.com/CarterShi01/contexture-mcp-go/debug"
	"github.com/CarterShi01/contexture-mcp-go/introspection"
)

// Catalog describes this exact static compiled Index. It is a trusted
// application read model and is never added to the MCP gateway.
func (application *RuntimeApplication) Catalog(options introspection.CatalogOptions) (*introspection.NodeCatalog, error) {
	if application == nil || application.Index == nil {
		return nil, fmt.Errorf("compiled runtime application required")
	}
	return introspection.New(application.Index, options)
}

func (application *DisclosureApplication) Catalog(options introspection.CatalogOptions) (*introspection.NodeCatalog, error) {
	if application == nil || application.Index == nil {
		return nil, fmt.Errorf("compiled disclosure application required")
	}
	return introspection.New(application.Index, options)
}

// Debug builds a loopback HTTP adapter over one immutable static Catalog.
func (application *RuntimeApplication) Debug(options introspection.CatalogOptions) (*debug.Server, error) {
	catalog, err := application.Catalog(options)
	if err != nil {
		return nil, err
	}
	return debug.New(catalog)
}

func (application *DisclosureApplication) Debug(options introspection.CatalogOptions) (*debug.Server, error) {
	catalog, err := application.Catalog(options)
	if err != nil {
		return nil, err
	}
	return debug.New(catalog)
}
