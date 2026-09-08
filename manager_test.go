package contexture_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

func TestControllerManagerRegistersOwnedRootsInKindAndRegistrationOrder(t *testing.T) {
	manager := contexture.NewControllerManager()
	role, err := manager.RegisterRole(func() *contexture.Role {
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect."}
	})
	if err != nil || role.Name != "operations" {
		t.Fatalf("RegisterRole = %#v, %v", role, err)
	}
	if _, err := manager.RegisterTool(func() *contexture.Tool {
		tool, buildErr := contexture.NewTool("status", "Read status.", true, func(context.Context, managerInput) (string, error) { return "ok", nil })
		if buildErr != nil {
			panic(buildErr)
		}
		return tool
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RegisterSkill(func() *contexture.Skill {
		return &contexture.Skill{Name: "diagnose", Description: "Diagnose.", Instructions: "Read status."}
	}); err != nil {
		t.Fatal(err)
	}
	if got := namesOf(manager.Roles()); !reflect.DeepEqual(got, []string{"operations"}) {
		t.Fatalf("Roles = %#v", got)
	}
	if got := namesOf(manager.Skills()); !reflect.DeepEqual(got, []string{"diagnose"}) {
		t.Fatalf("Skills = %#v", got)
	}
	if got := namesOf(manager.Tools()); !reflect.DeepEqual(got, []string{"status"}) {
		t.Fatalf("Tools = %#v", got)
	}
	if got := namesOf(manager.Roots()); !reflect.DeepEqual(got, []string{"operations", "diagnose", "status"}) {
		t.Fatalf("Roots must use Role/Skill/Tool grouping order, got %#v", got)
	}
	generic := contexture.NewControllerManager()
	root, err := contexture.RegisterRoot(generic, func() contexture.Node {
		return &contexture.Skill{Name: "dynamic", Description: "Dynamic.", Instructions: "Read."}
	})
	if err != nil || root.Kind() != contexture.SkillKind || !reflect.DeepEqual(namesOf(generic.Skills()), []string{"dynamic"}) {
		t.Fatalf("RegisterRoot dispatch = %#v, %v", root, err)
	}
}

func TestControllerManagerRegistrationConstructsOnceAndOwnsSnapshots(t *testing.T) {
	manager := contexture.NewControllerManager()
	built := 0
	registered, err := manager.RegisterRole(func() *contexture.Role {
		built++
		return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect."}
	})
	if err != nil || built != 1 {
		t.Fatalf("registration construction = %#v, built=%d, err=%v", registered, built, err)
	}
	registered.Name = "mutated-return-value"
	application, err := manager.Application("manager")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RegisterSkill(func() *contexture.Skill {
		return &contexture.Skill{Name: "later", Description: "Later.", Instructions: "Read."}
	}); err != nil {
		t.Fatal(err)
	}
	first, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	second, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	if built != 1 {
		t.Fatalf("Application/Compile rebuilt the manager factory %d times", built)
	}
	for _, index := range []*contexture.Index{first, second} {
		if _, err := index.Find("operations"); err != nil {
			t.Fatalf("manager snapshot was changed by caller mutation: %v", err)
		}
		if _, err := index.Find("later"); !errors.Is(err, contexture.ErrNodeNotFound) {
			t.Fatalf("later registration changed an existing Application: %v", err)
		}
	}
	left, _ := first.Find("operations")
	right, _ := second.Find("operations")
	if left == right {
		t.Fatal("manager compilations reused mutable node identity")
	}
}

func TestControllerManagerRejectsCrossKindNamesSharedNodesCyclesAndWrongGroups(t *testing.T) {
	manager := contexture.NewControllerManager()
	if _, err := manager.RegisterTool(func() *contexture.Tool {
		tool, buildErr := contexture.NewTool("shared", "Shared.", true, func(context.Context, managerInput) (string, error) { return "", nil })
		if buildErr != nil {
			panic(buildErr)
		}
		return tool
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RegisterRole(func() *contexture.Role {
		return &contexture.Role{Name: "shared", Description: "Clash.", Instructions: "Inspect."}
	}); !errors.Is(err, contexture.ErrDuplicate) || !strings.Contains(err.Error(), "first segment") {
		t.Fatalf("cross-kind root duplicate = %v", err)
	}

	shared, err := contexture.NewTool("status", "Status.", true, func(context.Context, managerInput) (string, error) { return "", nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := contexture.NewControllerManager().RegisterRole(func() *contexture.Role {
		return &contexture.Role{Name: "root", Description: "Root.", Instructions: "Inspect.", Children: []contexture.Factory{
			func() contexture.Node {
				return &contexture.Role{Name: "left", Description: "Left.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return shared }}}
			},
			func() contexture.Node {
				return &contexture.Role{Name: "right", Description: "Right.", Instructions: "Inspect.", Tools: []contexture.Factory{func() contexture.Node { return shared }}}
			},
		}}
	}); !errors.Is(err, contexture.ErrDuplicate) || !strings.Contains(err.Error(), "root/left/status") || !strings.Contains(err.Error(), "root/right/status") {
		t.Fatalf("shared node = %v", err)
	}

	outer := &contexture.Role{Name: "outer", Description: "Outer.", Instructions: "Inspect."}
	inner := &contexture.Role{Name: "inner", Description: "Inner.", Instructions: "Inspect."}
	outer.Children = []contexture.Factory{func() contexture.Node { return inner }}
	inner.Children = []contexture.Factory{func() contexture.Node { return outer }}
	if _, err := contexture.NewControllerManager().RegisterRole(func() *contexture.Role { return outer }); !errors.Is(err, contexture.ErrContainmentCycle) || !strings.Contains(err.Error(), "contains itself") {
		t.Fatalf("cycle = %v", err)
	}

	if _, err := contexture.NewControllerManager().RegisterRole(func() *contexture.Role {
		return &contexture.Role{Name: "wrong-group", Description: "Wrong.", Instructions: "Inspect.", Skills: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "nested", Description: "Nested.", Instructions: "Inspect."}
		}}}
	}); !errors.Is(err, contexture.ErrInvalidDeclaration) || !strings.Contains(err.Error(), "skill group") {
		t.Fatalf("wrong group = %v", err)
	}

	if _, err := contexture.NewControllerManager().RegisterRole(func() *contexture.Role {
		return &contexture.Role{Name: "duplicate-address", Description: "Duplicate.", Instructions: "Inspect.", Tools: []contexture.Factory{
			func() contexture.Node {
				tool, _ := contexture.NewTool("status", "First.", true, func(context.Context, managerInput) (string, error) { return "", nil })
				return tool
			},
			func() contexture.Node {
				tool, _ := contexture.NewTool("status", "Second.", true, func(context.Context, managerInput) (string, error) { return "", nil })
				return tool
			},
		}}
	}); !errors.Is(err, contexture.ErrDuplicate) || !strings.Contains(err.Error(), "duplicate-address/status") {
		t.Fatalf("duplicate address = %v", err)
	}
}

func TestControllerManagerRebindsOnlyFutureApplicationLifetimes(t *testing.T) {
	firstChannels := &managerChannels{}
	secondChannels := &managerChannels{}
	manager := contexture.NewControllerManagerWithChannels(firstChannels)
	if _, err := manager.RegisterTool(func() *contexture.Tool {
		tool, buildErr := contexture.NewTool("status", "Read.", true, func(context.Context, managerInput) (string, error) { return "ok", nil })
		if buildErr != nil {
			panic(buildErr)
		}
		return tool
	}); err != nil {
		t.Fatal(err)
	}
	capturedApplication, err := manager.Application("captured-before-rebind")
	if err != nil {
		t.Fatal(err)
	}
	manager.RebindChannels(secondChannels)
	before, err := contexture.Compile(capturedApplication)
	if err != nil {
		t.Fatal(err)
	}
	after, err := manager.Compile("after")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		index *contexture.Index
		want  *managerChannels
	}{{before, firstChannels}, {after, secondChannels}} {
		runtime, err := contexture.NewRuntime(test.index, contexture.AllRoots(), contexture.AllRoots(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.Serve(context.Background(), func(context.Context) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if test.want.opens != 1 || test.want.closes != 1 {
			t.Fatalf("compiled lifecycle used the wrong Channels: %#v", test.want)
		}
	}
}

type ordinaryChannelHandle struct {
	name       string
	openCalls  int
	closeCalls int
}

func (handle *ordinaryChannelHandle) Open(context.Context, contexture.CleanupRegistrar) error {
	handle.openCalls++
	return nil
}

func (handle *ordinaryChannelHandle) Close(context.Context) error {
	handle.closeCalls++
	return nil
}

func TestControllerManagerPassesOrdinaryChannelHandlesWithoutLifecycle(t *testing.T) {
	first := &ordinaryChannelHandle{name: "first"}
	second := &ordinaryChannelHandle{name: "second"}
	manager := contexture.NewControllerManagerWithChannelHandle(first)
	tool, err := contexture.NewTool("status", "Read.", true, func(ctx context.Context, _ managerInput) (string, error) {
		return contexture.CurrentChannels(ctx).(*ordinaryChannelHandle).name, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RegisterTool(func() *contexture.Tool { return tool }); err != nil {
		t.Fatal(err)
	}
	oldApplication, err := manager.Application("ordinary-old")
	if err != nil {
		t.Fatal(err)
	}
	manager.RebindChannelHandle(second)
	oldIndex, err := contexture.Compile(oldApplication)
	if err != nil {
		t.Fatal(err)
	}
	newIndex, err := manager.Compile("ordinary-new")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		index *contexture.Index
		want  string
	}{{oldIndex, "first"}, {newIndex, "second"}} {
		runtime, err := contexture.NewRuntime(test.index, contexture.AllRoots(), contexture.AllRoots(), nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := runtime.Serve(context.Background(), func(ctx context.Context) error {
			value, callErr := runtime.InvokeReadOnly(ctx, "status", nil, contexture.AllRoots())
			if callErr != nil || value != test.want {
				t.Fatalf("ordinary handle invocation = %#v, %v; want %q", value, callErr, test.want)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if first.openCalls != 0 || first.closeCalls != 0 || second.openCalls != 0 || second.closeCalls != 0 {
		t.Fatalf("ordinary lookalike entered lifecycle: first=%#v second=%#v", first, second)
	}
}

func TestControllerManagerRejectsTypedNilLifecycleWhenProducingApplication(t *testing.T) {
	var channels *managerChannels
	manager := contexture.NewControllerManagerWithChannels(channels)
	if _, err := manager.RegisterSkill(func() *contexture.Skill {
		return &contexture.Skill{Name: "read", Description: "Read.", Instructions: "Read."}
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Application("typed-nil"); !errors.Is(err, contexture.ErrInvalidDeclaration) {
		t.Fatalf("typed-nil manager Channels = %v", err)
	}
}

func TestControllerManagerRejectsInvalidNodeFactsAtTypedRegistration(t *testing.T) {
	tests := []struct {
		name     string
		register func(*contexture.ControllerManager) error
	}{
		{"role blank name", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterRole(func() *contexture.Role {
				return &contexture.Role{Name: " ", Description: "Role.", Instructions: "Inspect."}
			})
			return err
		}},
		{"role slash name", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterRole(func() *contexture.Role {
				return &contexture.Role{Name: "bad/name", Description: "Role.", Instructions: "Inspect."}
			})
			return err
		}},
		{"role blank description", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterRole(func() *contexture.Role {
				return &contexture.Role{Name: "role", Description: " ", Instructions: "Inspect."}
			})
			return err
		}},
		{"role blank instructions", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterRole(func() *contexture.Role {
				return &contexture.Role{Name: "role", Description: "Role.", Instructions: " "}
			})
			return err
		}},
		{"role duplicate uses", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterRole(func() *contexture.Role {
				return &contexture.Role{Name: "role", Description: "Role.", Instructions: "Inspect.", Uses: []string{"other", "other"}}
			})
			return err
		}},
		{"skill blank name", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterSkill(func() *contexture.Skill {
				return &contexture.Skill{Name: " ", Description: "Skill.", Instructions: "Inspect."}
			})
			return err
		}},
		{"skill slash name", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterSkill(func() *contexture.Skill {
				return &contexture.Skill{Name: "bad/name", Description: "Skill.", Instructions: "Inspect."}
			})
			return err
		}},
		{"skill blank description", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterSkill(func() *contexture.Skill {
				return &contexture.Skill{Name: "skill", Description: " ", Instructions: "Inspect."}
			})
			return err
		}},
		{"skill blank instructions", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterSkill(func() *contexture.Skill {
				return &contexture.Skill{Name: "skill", Description: "Skill.", Instructions: " "}
			})
			return err
		}},
		{"skill duplicate uses", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterSkill(func() *contexture.Skill {
				return &contexture.Skill{Name: "skill", Description: "Skill.", Instructions: "Inspect.", Uses: []string{"other", "other"}}
			})
			return err
		}},
		{"tool blank name", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterTool(func() *contexture.Tool { return &contexture.Tool{Name: " ", Description: "Tool."} })
			return err
		}},
		{"tool slash name", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterTool(func() *contexture.Tool { return &contexture.Tool{Name: "bad/name", Description: "Tool."} })
			return err
		}},
		{"tool blank description", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterTool(func() *contexture.Tool { return &contexture.Tool{Name: "tool", Description: " "} })
			return err
		}},
		{"tool duplicate uses", func(manager *contexture.ControllerManager) error {
			_, err := manager.RegisterTool(func() *contexture.Tool {
				return &contexture.Tool{Name: "tool", Description: "Tool.", Uses: []string{"other", "other"}}
			})
			return err
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.register(contexture.NewControllerManager()); !errors.Is(err, contexture.ErrInvalidDeclaration) {
				t.Fatalf("typed registration error = %v, want ErrInvalidDeclaration", err)
			}
		})
	}
}

type managerInput struct{}

type managerChannels struct {
	contexture.ChannelsLifecycle
	opens, closes int
}

func (channels *managerChannels) Open(context.Context, contexture.CleanupRegistrar) error {
	channels.opens++
	return nil
}
func (channels *managerChannels) Close(context.Context) error {
	channels.closes++
	return nil
}

func namesOf(nodes any) []string {
	result := []string{}
	switch typed := any(nodes).(type) {
	case []*contexture.Role:
		for _, node := range typed {
			result = append(result, node.Name)
		}
	case []*contexture.Skill:
		for _, node := range typed {
			result = append(result, node.Name)
		}
	case []*contexture.Tool:
		for _, node := range typed {
			result = append(result, node.Name)
		}
	case []contexture.Node:
		for _, node := range typed {
			result = append(result, node.NodeName())
		}
	}
	return result
}
