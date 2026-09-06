package demo

import contexture "github.com/CarterShi01/contexture-mcp-go"

func kubernetesPlatform() contexture.Node {
	return &contexture.Role{
		Name: "kubernetes-platform", Description: "Operate a Kubernetes platform: diagnose incidents, and reverse releases.",
		Instructions: "Route to the specialism the task belongs to, and open only that one. Diagnose before remediating.",
		Children: []contexture.Factory{
			func() contexture.Node {
				return &contexture.Role{Name: "incident-response", Description: "Diagnose unhealthy Kubernetes workloads from cluster evidence.", Instructions: "Work from evidence before naming a cause.", Skills: []contexture.Factory{diagnoseCrashLoopBackOff}, Tools: []contexture.Factory{getPodStatus, getPodLogs, getPodEvents, crashLoopRunbook}}
			},
			func() contexture.Node {
				return &contexture.Role{Name: "deployment-ops", Description: "Inspect and reverse Kubernetes releases that have gone wrong.", Instructions: "Remediation follows diagnosis and never replaces it.", Skills: []contexture.Factory{rollBackAFailedRelease}, Tools: []contexture.Factory{getRolloutStatus, rollBackDeployment, rollbackPolicy}}
			},
		},
	}
}
