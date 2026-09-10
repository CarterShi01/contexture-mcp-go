package demo

import contexture "github.com/CarterShi01/contexture-mcp-go"

// IncidentResponse returns the maintained diagnosis role declaration.
func IncidentResponse() contexture.Node {
	return &contexture.Role{Name: "incident-response", Description: "Diagnose unhealthy Kubernetes workloads from cluster evidence.", Instructions: `Work from evidence, never from the shape of the question. Select the skill that
matches the reported symptom, follow its procedure, and collect tool output
before naming a cause. Report the root cause and the smallest safe next action.`, Skills: []contexture.Factory{diagnoseCrashLoopBackOff}, Tools: []contexture.Factory{GetPodStatus, GetPodLogs, GetPodEvents, crashLoopRunbook}}
}

// DeploymentOps returns the maintained release-remediation role declaration.
func DeploymentOps() contexture.Node {
	return &contexture.Role{Name: "deployment-ops", Description: "Inspect and reverse Kubernetes releases that have gone wrong.", Instructions: `Remediation follows diagnosis and never replaces it. Read the policy, establish
what the previous revision would restore, and say what evidence a rollback
destroys before proposing one. Anything that changes the cluster is run through
contexture_invoke, where a host can put a human in front of it.`, Skills: []contexture.Factory{rollBackAFailedRelease}, Tools: []contexture.Factory{GetRolloutStatus, RollBackDeployment, rollbackPolicy}}
}

// KubernetesPlatform returns the maintained root role declaration.
func KubernetesPlatform() contexture.Node {
	return &contexture.Role{
		Name: "kubernetes-platform", Description: "Operate a Kubernetes platform: diagnose incidents, and reverse releases.",
		Instructions: `Route to the specialism the task belongs to, and open only that one. Diagnose
before remediating: incident-response establishes a cause from evidence, and
deployment-ops reverses a release once the cause is known.`,
		Children: []contexture.Factory{
			IncidentResponse,
			DeploymentOps,
		},
	}
}
