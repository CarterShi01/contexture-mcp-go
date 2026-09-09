package demo

import contexture "github.com/CarterShi01/contexture-mcp-go"
import "github.com/CarterShi01/contexture-mcp-go/server"

func RollBackARelease() contexture.PromptDeclaration {
	return contexture.PromptDeclaration{Opens: "kubernetes-platform/deployment-ops/roll-back-a-failed-release", Name: "roll-back-a-release", Description: "Put the rollback procedure in context: what to capture before a release is reversed, and what reversing it destroys."}
}

func CrashLoopRunbookDocument() contexture.ResourceDeclaration {
	return contexture.ResourceDeclaration{Opens: "kubernetes-platform/incident-response/crash_loop_runbook", URI: "contexture://runbooks/crash-loop-backoff", Description: "How to diagnose a container that keeps restarting, and what not to do.", MIMEType: "text/markdown"}
}

func RollbackPolicyDocument() contexture.ResourceDeclaration {
	return contexture.ResourceDeclaration{Opens: "kubernetes-platform/deployment-ops/rollback_policy", URI: "contexture://runbooks/rollback-policy", Description: "When a rollback is the right remediation, and what it costs.", MIMEType: "text/markdown"}
}

// Application returns the maintained lazy Contexture reference declaration.
func Application() (*contexture.Application, error) {
	return contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name: "contexture-demo", Roots: []contexture.Factory{KubernetesPlatform},
		Prompts: []contexture.PromptDeclaration{RollBackARelease()},
		Resources: []contexture.ResourceDeclaration{
			CrashLoopRunbookDocument(), RollbackPolicyDocument(),
		},
	})
}

// Build compiles the same maintained Application used by the demo CLI.
func Build() (*server.ApplicationServer, error) {
	application, err := Application()
	if err != nil {
		return nil, err
	}
	return server.BuildServer(application)
}
