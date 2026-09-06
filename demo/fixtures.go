// Package demo provides Contexture's maintained, self-contained reference application.
package demo

const (
	Namespace  = "prod"
	Pod        = "payments-api-7d9c"
	Deployment = "payments-api"
)

// PodStatus is the fixed unhealthy workload this demo investigates.
type PodStatus struct {
	Namespace      string `json:"namespace"`
	Pod            string `json:"pod"`
	Phase          string `json:"phase"`
	ContainerState string `json:"container_state"`
	RestartCount   int    `json:"restart_count"`
	Ready          bool   `json:"ready"`
	Image          string `json:"image"`
}

var FixedPodStatus = PodStatus{Namespace: Namespace, Pod: Pod, Phase: "Running", ContainerState: "CrashLoopBackOff", RestartCount: 14, Ready: false, Image: "registry.internal/payments-api:1.8.2"}

var PodLogs = "2026-08-19T09:12:04Z INFO  payments-api starting, build 1.8.2\n" +
	"2026-08-19T09:12:04Z INFO  loading configuration from environment\n" +
	"2026-08-19T09:12:04Z ERROR ConfigurationError: required environment variable DB_URL is missing\n" +
	"2026-08-19T09:12:04Z FATAL startup aborted after configuration error"

// PodEvent records one fixture event reported by Kubernetes.
type PodEvent struct {
	Type    string `json:"type"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Count   int    `json:"count"`
}

var PodEvents = []PodEvent{
	{Type: "Normal", Reason: "Pulled", Message: "Container image already present on machine", Count: 15},
	{Type: "Normal", Reason: "Created", Message: "Created container payments-api", Count: 15},
	{Type: "Warning", Reason: "BackOff", Message: "Back-off restarting failed container", Count: 14},
	{Type: "Warning", Reason: "Unhealthy", Message: "Container exited with code 1", Count: 14},
}

// RolloutStatus describes the corresponding failed deployment rollout.
type RolloutStatus struct {
	Namespace         string `json:"namespace"`
	Deployment        string `json:"deployment"`
	CurrentRevision   int    `json:"current_revision"`
	PreviousRevision  int    `json:"previous_revision"`
	CurrentImage      string `json:"current_image"`
	PreviousImage     string `json:"previous_image"`
	UpdatedReplicas   int    `json:"updated_replicas"`
	AvailableReplicas int    `json:"available_replicas"`
	RolledOutAt       string `json:"rolled_out_at"`
}

var FixedRolloutStatus = RolloutStatus{Namespace: Namespace, Deployment: Deployment, CurrentRevision: 9, PreviousRevision: 8, CurrentImage: "registry.internal/payments-api:1.8.2", PreviousImage: "registry.internal/payments-api:1.8.1", UpdatedReplicas: 3, AvailableReplicas: 0, RolledOutAt: "2026-08-19T09:11:47Z"}

var CrashLoopRunbook = `# Runbook: CrashLoopBackOff

A container that starts and exits repeatedly. Kubernetes backs off between
restarts, so the symptom is visible long before the cause is.

## Order of investigation

1. **Status first.** A high ` + "`restart_count`" + ` with ` + "`ready: false`" + ` confirms the
   loop rather than a slow start.
2. **Logs before events.** The container's own output names the failure.
   Events describe what the kubelet observed, which is one level removed.
3. **Correlate the exit code.** Exit code 1 is an application-level failure:
   the process ran and then rejected its own state. Exit code 137 would mean
   it was killed, usually for memory, which is a different runbook.

## Common causes, in the order they are usually found

| Evidence in logs | Cause | Remediation |
| --- | --- | --- |
| ` + "`ConfigurationError`" + `, missing variable | Config or Secret not projected into the Pod | Add the key to the ConfigMap or Secret, then roll out |
| Connection refused to a dependency | Dependency unavailable, or wrong address | Fix the address, or wait for the dependency |
| Panic or uncaught exception on startup | Application defect | Roll back to the last good image |
| OOMKilled, exit 137 | Memory limit too low | Raise the limit |

## Do not

Restarting the Pod does not repair a configuration error; it produces restart
15. Identify the cause before recommending any change.
`

var RollbackPolicy = `# Policy: rolling back a failed release

A rollback is a remediation, not a diagnosis. It restores the previous revision
and destroys the evidence of why the current one failed.

Before rolling back:

- Establish the cause from the workload's own output. A rollback that follows a
  guess teaches nothing and will be needed again on the next release.
- Capture logs and events first. The failing Pods are replaced immediately.
- Check whether the cause is in the image at all. A missing environment
  variable, a bad ConfigMap, or an absent Secret follows the previous revision
  back and reappears.

After rolling back, the incident is not closed. The release that failed is
still the release that will be re-attempted.
`
