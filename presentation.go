package contexture

import "github.com/CarterShi01/contexture-mcp-go/core/presentation"

type (
	Presentation               = presentation.Presentation
	PresentationOptions        = presentation.Options
	PresentationCatalog        = presentation.PresentationCatalog
	PresentationCatalogOptions = presentation.CatalogOptions
	PresentationSource         = presentation.Source
	NodePresentation           = presentation.NodePresentation
	NodePresentationOptions    = presentation.NodeOptions
	GatewayPresentation        = presentation.GatewayPresentation
	GatewayPresentationOptions = presentation.GatewayOptions
)

// NewPresentation creates one immutable set of human labels.
func NewPresentation(options PresentationOptions) (*Presentation, error) {
	return presentation.NewPresentation(options)
}

// NewNodePresentation creates routing copy without changing node identity.
func NewNodePresentation(options NodePresentationOptions) (*NodePresentation, error) {
	return presentation.NewNodePresentation(options)
}

// NewGatewayPresentation creates human titles for fixed gateway machine names.
func NewGatewayPresentation(options GatewayPresentationOptions) (*GatewayPresentation, error) {
	return presentation.NewGatewayPresentation(options)
}

// NewPresentationCatalog creates an explicitly defaulted immutable locale catalog.
func NewPresentationCatalog(options PresentationCatalogOptions) (*PresentationCatalog, error) {
	return presentation.NewPresentationCatalog(options)
}

// NormalizeLocale normalizes the current reference's practical locale tags.
func NormalizeLocale(locale string) (string, error) { return presentation.NormalizeLocale(locale) }

// ResolvePresentation selects neutral/default/parent/exact copy without mutating it.
func ResolvePresentation(source PresentationSource, locale *string) (string, *Presentation, error) {
	return presentation.ResolvePresentation(source, locale)
}
