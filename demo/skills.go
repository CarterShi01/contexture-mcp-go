package demo

import contexture "github.com/CarterShi01/contexture-mcp-go"

const diagnosisRef = "kubernetes-platform/incident-response/diagnose-crash-loop-backoff"

func diagnoseCrashLoopBackOff() contexture.Node {
	return &contexture.Skill{
		Name: "diagnose-crash-loop-backoff", Description: "Find why a Pod restarts repeatedly, before proposing any remediation.",
		Instructions: "Establish the cause from evidence, in this order.\n\n1. Call get_pod_status.\n2. Call get_pod_logs before forming a hypothesis.\n3. Call get_pod_events for the observed exit code.\n4. Call crash_loop_runbook and report the smallest safe next action.",
	}
}

func rollBackAFailedRelease() contexture.Node {
	return &contexture.Skill{
		Name: "roll-back-a-failed-release", Description: "Decide whether to roll a release back, and what to capture first.", Uses: []string{diagnosisRef},
		Instructions: "A rollback destroys the evidence it was called for. Read rollback_policy, inspect rollout status, establish the cause through the listed diagnosis procedure, and only then call roll_back_deployment.",
	}
}
