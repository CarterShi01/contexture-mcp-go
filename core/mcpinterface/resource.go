package mcpinterface

// ResourceDeclaration publishes an argument-free, read-only Tool by URI.
type ResourceDeclaration struct {
	Name        string
	Opens       string
	URI         string
	Description string
	MIMEType    string
}
