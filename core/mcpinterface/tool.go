package mcpinterface

import "github.com/CarterShi01/contexture-mcp-go/core/foundation"

// GatewayName identifies one immutable Contexture system Tool.
type GatewayName = foundation.GatewayName

const (
	DiscoverGatewayName       = foundation.DiscoverGatewayName
	OpenGatewayName           = foundation.OpenGatewayName
	InvokeReadOnlyGatewayName = foundation.InvokeReadOnlyGatewayName
	InvokeGatewayName         = foundation.InvokeGatewayName
)
