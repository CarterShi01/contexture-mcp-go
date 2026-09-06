package demo

import contexture "github.com/CarterShi01/contexture-mcp-go"

// Application returns the maintained lazy Contexture reference declaration.
func Application() (*contexture.Application, error) {
	return contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "contexture-demo", Roots: []contexture.Factory{kubernetesPlatform},
		Prompts: []contexture.PromptDeclaration{{Opens: "kubernetes-platform/deployment-ops/roll-back-a-failed-release", Name: "roll-back-a-release", Description: "Put the rollback procedure in context: what to capture before a release is reversed, and what reversing it destroys."}},
		Resources: []contexture.ResourceDeclaration{
			{Opens: "kubernetes-platform/incident-response/crash_loop_runbook", URI: "contexture://runbooks/crash-loop-backoff", Description: "How to diagnose a container that keeps restarting, and what not to do.", MIMEType: "text/markdown"},
			{Opens: "kubernetes-platform/deployment-ops/rollback_policy", URI: "contexture://runbooks/rollback-policy", Description: "When a rollback is the right remediation, and what it costs.", MIMEType: "text/markdown"},
		},
	})
}
