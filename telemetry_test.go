package contexture_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

type telemetryInput struct {
	Value string `json:"value"`
}

func TestTelemetryAggregatesOnlyActualRoleSkillOpensAndToolInvocations(t *testing.T) {
	collector := contexture.NewMemoryTelemetry()
	var runtime *contexture.Runtime
	status, err := contexture.NewTool("status", "Read status.", true, func(ctx context.Context, input telemetryInput) (string, error) {
		if contexture.CurrentTelemetry(ctx) != collector {
			return "", errors.New("Tool did not receive the compiled telemetry collector")
		}
		return input.Value, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	failing, err := contexture.NewTool("fail", "Fail.", true, func(context.Context, telemetryInput) (string, error) { return "", errors.New("fixture failure") })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "telemetry", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Read status."}
		}}, Tools: []contexture.Factory{func() contexture.Node { return status }, func() contexture.Node { return failing }}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	disclosure, err := contexture.NewDisclosureWithTelemetry(index, contexture.AllRoots(), collector)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err = contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), collector)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := disclosure.Discover(contexture.AllRoots()); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{"operations", "operations/diagnose", "operations/status"} {
		if _, err := disclosure.Open(ref, contexture.AllRoots()); err != nil {
			t.Fatalf("Open(%q) = %v", ref, err)
		}
	}
	if value, err := runtime.InvokeReadOnly(context.Background(), "operations/status", json.RawMessage(`{"value":"ok"}`), contexture.AllRoots()); err != nil || value != "ok" {
		t.Fatalf("status invocation = %#v, %v", value, err)
	}
	if _, err := runtime.InvokeReadOnly(context.Background(), "operations/fail", json.RawMessage(`{"value":"x"}`), contexture.AllRoots()); err == nil {
		t.Fatal("failing Tool succeeded")
	}
	for ref, want := range map[string][2]int{
		"operations":          {1, 0},
		"operations/diagnose": {1, 0},
		"operations/status":   {1, 0},
		"operations/fail":     {1, 1},
	} {
		usage := collector.Usage(ref)
		if [2]int{usage.CallCount, usage.ErrorCount} != want || usage.LastUsedAt == "" {
			t.Fatalf("Usage(%q) = %#v, want counts %#v and timestamp", ref, usage, want)
		}
	}
	if usage := collector.Usage("unseen"); usage.Ref != "unseen" || usage.CallCount != 0 || usage.ErrorCount != 0 || usage.LastUsedAt != "" {
		t.Fatalf("unseen usage = %#v", usage)
	}
	first, second := collector.Events(), collector.Events()
	if len(first) != 4 || len(second) != len(first) {
		t.Fatalf("Events must be non-destructive snapshots: %d then %d", len(first), len(second))
	}
}

func TestTelemetryConcurrentAggregationAndExporterFailureDoNotChangeCalls(t *testing.T) {
	collector := contexture.NewMemoryTelemetry()
	tool, err := contexture.NewTool("status", "Read.", true, func(context.Context, telemetryInput) (string, error) { return "ok", nil })
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "concurrent-telemetry", Roots: []contexture.Factory{func() contexture.Node { return tool }}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), collector)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	errs := make(chan error, 32)
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := runtime.InvokeReadOnly(context.Background(), "status", json.RawMessage(`{"value":"x"}`), contexture.AllRoots())
			errs <- err
		}()
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if usage := collector.Usage("status"); usage.CallCount != 32 || usage.ErrorCount != 0 {
		t.Fatalf("concurrent usage = %#v", usage)
	}
	broken, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), brokenUsageTelemetry{})
	if err != nil {
		t.Fatal(err)
	}
	if value, err := broken.InvokeReadOnly(context.Background(), "status", json.RawMessage(`{"value":"x"}`), contexture.AllRoots()); err != nil || value != "ok" {
		t.Fatalf("broken exporter changed business result = %#v, %v", value, err)
	}
	panicking, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), panickingTelemetry{})
	if err != nil {
		t.Fatal(err)
	}
	if value, err := panicking.InvokeReadOnly(context.Background(), "status", json.RawMessage(`{"value":"x"}`), contexture.AllRoots()); err != nil || value != "ok" {
		t.Fatalf("panicking exporter changed business result = %#v, %v", value, err)
	}
}

func TestTelemetryExporterFailurePreservesDisclosurePayloadsAndBusinessFailure(t *testing.T) {
	businessFailure := errors.New("business failure must survive telemetry")
	failing, err := contexture.NewTool("fail", "Fail.", true, func(context.Context, telemetryInput) (string, error) {
		return "", businessFailure
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{Name: "telemetry-isolation", Roots: []contexture.Factory{func() contexture.Node {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node {
			return &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Read."}
		}}, Tools: []contexture.Factory{func() contexture.Node { return failing }}}
	}}})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := contexture.NewDisclosure(index, contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	wantRole, err := baseline.Open("operations", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	wantSkill, err := baseline.Open("operations/diagnose", contexture.AllRoots())
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"error", "panic"} {
		t.Run(mode, func(t *testing.T) {
			exporter := &observingFailureTelemetry{panicOnRecord: mode == "panic"}
			disclosure, err := contexture.NewDisclosureWithTelemetry(index, contexture.AllRoots(), exporter)
			if err != nil {
				t.Fatal(err)
			}
			gotRole, err := disclosure.Open("operations", contexture.AllRoots())
			if err != nil || !reflect.DeepEqual(gotRole, wantRole) {
				t.Fatalf("Role Open payload changed by telemetry %s: %#v, %v", mode, gotRole, err)
			}
			gotSkill, err := disclosure.Open("operations/diagnose", contexture.AllRoots())
			if err != nil || !reflect.DeepEqual(gotSkill, wantSkill) {
				t.Fatalf("Skill Open payload changed by telemetry %s: %#v, %v", mode, gotSkill, err)
			}
			runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), exporter)
			if err != nil {
				t.Fatal(err)
			}
			_, err = runtime.InvokeReadOnly(context.Background(), "operations/fail", json.RawMessage(`{"value":"x"}`), contexture.AllRoots())
			if !errors.Is(err, businessFailure) {
				t.Fatalf("Tool failure was replaced by telemetry %s: %v", mode, err)
			}
			if got, want := exporter.events, []contexture.CallEvent{{Ref: "operations"}, {Ref: "operations/diagnose"}, {Ref: "operations/fail", Failed: true}}; !reflect.DeepEqual(got, want) {
				t.Fatalf("telemetry %s did not observe expected uses: %#v", mode, got)
			}
		})
	}
}

func TestReportTelemetryIsPublicFailureIsolatedAggregate(t *testing.T) {
	collector := contexture.NewMemoryTelemetry()
	contexture.ReportTelemetry(collector, "host/refresh", true)
	if usage := collector.Usage("host/refresh"); usage.CallCount != 1 || usage.ErrorCount != 1 || usage.LastUsedAt == "" {
		t.Fatalf("public report aggregate = %#v", usage)
	}
	contexture.ReportTelemetry(brokenUsageTelemetry{}, "host/broken", false)
	contexture.ReportTelemetry(panickingTelemetry{}, "host/panicking", false)
}

type brokenUsageTelemetry struct{}

func (brokenUsageTelemetry) Record(contexture.CallEvent) error {
	return errors.New("exporter unavailable")
}
func (brokenUsageTelemetry) Usage(ref string) contexture.NodeUsage {
	return contexture.NodeUsage{Ref: ref}
}

type panickingTelemetry struct{}

func (panickingTelemetry) Record(contexture.CallEvent) error { panic("exporter panic") }
func (panickingTelemetry) Usage(ref string) contexture.NodeUsage {
	return contexture.NodeUsage{Ref: ref}
}

type observingFailureTelemetry struct {
	events        []contexture.CallEvent
	panicOnRecord bool
}

func (telemetry *observingFailureTelemetry) Record(event contexture.CallEvent) error {
	telemetry.events = append(telemetry.events, event)
	if telemetry.panicOnRecord {
		panic("exporter panic")
	}
	return errors.New("exporter unavailable")
}

func (telemetry *observingFailureTelemetry) Usage(ref string) contexture.NodeUsage {
	return contexture.NodeUsage{Ref: ref}
}
