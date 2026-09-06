package demo

import (
	"context"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func crashLoopRunbook() contexture.Node {
	return mustTool(contexture.NewTool("crash_loop_runbook", "How to diagnose a container that keeps restarting, and what not to do.", true, func(context.Context, struct{}) (string, error) {
		return CrashLoopRunbook, nil
	}))
}

func rollbackPolicy() contexture.Node {
	return mustTool(contexture.NewTool("rollback_policy", "When a rollback is the right remediation, and what it costs.", true, func(context.Context, struct{}) (string, error) {
		return RollbackPolicy, nil
	}))
}
