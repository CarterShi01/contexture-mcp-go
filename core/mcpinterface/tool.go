package mcpinterface

// GatewayName identifies one immutable Contexture system Tool.
type GatewayName string

const (
	DiscoverGatewayName       GatewayName = "contexture_discover"
	OpenGatewayName           GatewayName = "contexture_open"
	InvokeReadOnlyGatewayName GatewayName = "contexture_invoke_read_only"
	InvokeGatewayName         GatewayName = "contexture_invoke"
)
