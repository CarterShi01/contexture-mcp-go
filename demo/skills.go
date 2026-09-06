package demo

import contexture "github.com/CarterShi01/contexture-mcp-go"

const diagnosisRef = "kubernetes-platform/incident-response/diagnose-crash-loop-backoff"

func diagnoseCrashLoopBackOff() contexture.Node {
	return &contexture.Skill{
		Name: "diagnose-crash-loop-backoff", Description: "Find why a Pod restarts repeatedly, before proposing any remediation.",
		Instructions: `Establish the cause from evidence, in this order.

1. Call get_pod_status. A high restart_count with ready=false confirms a
   restart loop rather than a slow or pending start.
2. Call get_pod_logs. The container's own output names the failure; read it
   before forming a hypothesis.
3. Call get_pod_events. Events tell you what the kubelet observed, including
   the exit code, which separates an application failure from a kill.
4. Call crash_loop_runbook and match the evidence you collected against its
   table of causes. The same content is optionally published to hosts at
   contexture://runbooks/crash-loop-backoff.

Then report the root cause and the single smallest next action.

Constraints:
- Do not recommend restarting or deleting the Pod before the cause is known.
  A restart does not repair a configuration error; it produces one more restart.
- Do not state any cluster state you have not read from a tool.
- Name the specific evidence, including the exit code, that supports your
  conclusion.`,
	}
}

func rollBackAFailedRelease() contexture.Node {
	return &contexture.Skill{
		Name: "roll-back-a-failed-release", Description: "Decide whether to roll a release back, and what to capture first.", Uses: []string{diagnosisRef},
		Instructions: "A rollback destroys the evidence it was called for. Work in this order.\n\n" +
			"1. Call rollback_policy before doing anything else. The same content is\n" +
			"   optionally published to hosts at contexture://runbooks/rollback-policy.\n" +
			"2. Call get_rollout_status. Compare the current and previous image: if they\n" +
			"   differ only in a tag, the cause may not be in the image at all.\n" +
			"3. Establish the cause first, by opening the procedure listed under `uses` and\n" +
			"   following it. A rollback that follows a guess will be needed again on the\n" +
			"   next release.\n" +
			"4. Only then call roll_back_deployment, and say plainly what evidence is lost.\n\n" +
			"Constraints:\n" +
			"- Do not roll back before the cause is known and the evidence is captured.\n" +
			"- A configuration fault follows the previous revision back. Say so rather than\n" +
			"  presenting a rollback as a fix.\n" +
			"- roll_back_deployment changes the cluster. It is not read-only, so it must be\n" +
			"  run through contexture_invoke and a human may be asked first.",
	}
}
