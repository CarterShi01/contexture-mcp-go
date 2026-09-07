package foundation

// ModelOpenPolicy states whether a model may navigate to a Prompt target.
// The zero value permits model navigation, matching an ordinary declaration.
type ModelOpenPolicy uint8

const (
	// ModelMayOpen leaves the target reachable through both model and person doors.
	ModelMayOpen ModelOpenPolicy = iota
	// ModelReservedForPerson keeps the target card visible but reserves opening it
	// for the named Prompt or fixed person-controlled goto entry.
	ModelReservedForPerson
)

// PromptDeclaration publishes person-controlled navigation to one node. It is
// shared declaration data, not an MCP SDK primitive, so model and publication
// adapters can agree on its meaning without depending on one another.
type PromptDeclaration struct {
	Name        string
	Opens       string
	Description string
	ModelOpen   ModelOpenPolicy
}

// AllowsModelOpen reports whether model navigation may open this target.
func (declaration PromptDeclaration) AllowsModelOpen() bool {
	return declaration.ModelOpen == ModelMayOpen
}

// ResourceDeclaration publishes an argument-free, read-only Tool by URI.
// It remains data-only until a Host adapter translates it to an MCP resource.
type ResourceDeclaration struct {
	Name        string
	Opens       string
	URI         string
	Description string
	MIMEType    string
}
