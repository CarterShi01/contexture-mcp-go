package demo

import (
	"context"
	"fmt"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

type podInput struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
}

type logsInput struct {
	Namespace string `json:"namespace"`
	Pod       string `json:"pod"`
	Previous  *bool  `json:"previous,omitempty" default:"false"`
}

type deploymentInput struct {
	Namespace  string `json:"namespace"`
	Deployment string `json:"deployment"`
}

func requirePod(input podInput) error {
	if input.Namespace == Namespace && input.Pod == Pod {
		return nil
	}
	return fmt.Errorf("No pod %q in namespace %q. This demo serves a single fixed incident: pod %q in namespace %q.", input.Pod, input.Namespace, Pod, Namespace)
}

func requireDeployment(input deploymentInput) error {
	if input.Namespace == Namespace && input.Deployment == Deployment {
		return nil
	}
	return fmt.Errorf("No deployment %q in namespace %q. This demo serves a single fixed incident: deployment %q in namespace %q.", input.Deployment, input.Namespace, Deployment, Namespace)
}

// GetPodStatus returns the maintained read-only Pod status Tool declaration.
func GetPodStatus() contexture.Node {
	return mustTool(contexture.NewTool("get_pod_status", "Return the current phase, container state, and restart count of a Pod.", true, func(_ context.Context, input podInput) (PodStatus, error) {
		return FixedPodStatus, requirePod(input)
	}))
}

// GetPodLogs returns the maintained read-only Pod logs Tool declaration.
func GetPodLogs() contexture.Node {
	return mustTool(contexture.NewToolWithSchema("get_pod_logs", "Return the recent container logs for a Pod.", true, map[string]any{
		"type": "object", "properties": map[string]any{
			"namespace": map[string]any{"type": "string"},
			"pod":       map[string]any{"type": "string"},
			"previous":  map[string]any{"type": "boolean", "default": false},
		}, "required": []any{"namespace", "pod"},
	}, func(_ context.Context, input logsInput) (string, error) {
		return PodLogs, requirePod(podInput{Namespace: input.Namespace, Pod: input.Pod})
	}))
}

// GetPodEvents returns the maintained read-only Pod events Tool declaration.
func GetPodEvents() contexture.Node {
	return mustTool(contexture.NewTool("get_pod_events", "Return the Kubernetes events recorded against a Pod.", true, func(_ context.Context, input podInput) ([]PodEvent, error) {
		return append([]PodEvent(nil), PodEvents...), requirePod(input)
	}))
}

// GetRolloutStatus returns the maintained read-only Deployment rollout Tool declaration.
func GetRolloutStatus() contexture.Node {
	return mustTool(contexture.NewTool("get_rollout_status", "Return the current and previous revision of a Deployment's rollout.", true, func(_ context.Context, input deploymentInput) (RolloutStatus, error) {
		return FixedRolloutStatus, requireDeployment(input)
	}))
}

// RollBackDeployment returns the maintained writing Deployment rollback Tool declaration.
func RollBackDeployment() contexture.Node {
	return mustTool(contexture.NewTool("roll_back_deployment", "Restore a Deployment's previous revision, replacing its running Pods.", false, func(_ context.Context, input deploymentInput) (string, error) {
		if err := requireDeployment(input); err != nil {
			return "", err
		}
		return fmt.Sprintf("Rolled %s/%s back from revision %d to %d (%s). The failing Pods have been replaced, so their logs and events are no longer available.", input.Namespace, input.Deployment, FixedRolloutStatus.CurrentRevision, FixedRolloutStatus.PreviousRevision, FixedRolloutStatus.PreviousImage), nil
	}))
}

func mustTool(tool *contexture.Tool, err error) contexture.Node {
	if err != nil {
		panic(err)
	}
	return tool
}
