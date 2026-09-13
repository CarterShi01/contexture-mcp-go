package foundation

// PackageName is the stable name of the Contexture framework package. It is
// framework metadata, not the name an application publishes to an MCP host.
const PackageName = "contexture"

// PackageVersion is the Go binding release version. It is deliberately
// distinct from SpecificationVersion: one binding release targets one pinned
// Contexture contract, while an application has its own host identity.
const PackageVersion = "1.0.1"

// ReferenceSeparator separates one segment of a Contexture reference from
// the next. References are paths; empty segments are normalized by Index
// lookup rather than becoming address components.
const ReferenceSeparator = "/"

// GatewayName identifies one immutable Contexture system Tool. It belongs in
// shared foundation because both the model and MCP primitive declaration use
// the same closed vocabulary without depending on one another.
type GatewayName string

// The five fixed model-facing entry points. Business capabilities travel in
// their payloads and are never registered as top-level MCP tools.
const (
	DiscoverGatewayName       GatewayName = "contexture_discover"
	InspectGatewayName        GatewayName = "contexture_inspect"
	OpenGatewayName           GatewayName = "contexture_open"
	InvokeReadOnlyGatewayName GatewayName = "contexture_invoke_read_only"
	InvokeGatewayName         GatewayName = "contexture_invoke"
)
