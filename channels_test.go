package contexture_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	contexture "github.com/CarterShi01/contexture-mcp-go"
)

var errOpen = errors.New("open failed")
var errServe = errors.New("serve failed")
var errClose = errors.New("close failed")
var errCleanup = errors.New("cleanup failed")

type testChannels struct {
	events []string
	open   error
	close  error
}

func (channels *testChannels) Open(_ context.Context, registrar contexture.CleanupRegistrar) error {
	channels.events = append(channels.events, "open")
	registrar.Defer(func(context.Context) error {
		channels.events = append(channels.events, "cleanup:first")
		return nil
	})
	registrar.Defer(func(context.Context) error {
		channels.events = append(channels.events, "cleanup:second")
		return errCleanup
	})
	return channels.open
}

func (channels *testChannels) Close(context.Context) error {
	channels.events = append(channels.events, "close")
	return channels.close
}

func TestWithChannelsClosesBeforeReverseCleanup(t *testing.T) {
	channels := &testChannels{}
	value, err := contexture.WithChannels(context.Background(), channels, func(context.Context) (string, error) {
		channels.events = append(channels.events, "serve")
		return "served", nil
	})
	if !errors.Is(err, errCleanup) || value != "" {
		t.Fatalf("WithChannels = %q, %v", value, err)
	}
	want := []string{"open", "serve", "close", "cleanup:second", "cleanup:first"}
	if !reflect.DeepEqual(channels.events, want) {
		t.Fatalf("events = %#v, want %#v", channels.events, want)
	}
}

func TestWithChannelsPreservesPrimaryErrorsAndUnwindsPartialOpen(t *testing.T) {
	channels := &testChannels{open: errOpen}
	_, err := contexture.WithChannels(context.Background(), channels, func(context.Context) (string, error) {
		t.Fatal("serve must not run after Open failure")
		return "", nil
	})
	if !errors.Is(err, errOpen) || !errors.Is(err, errCleanup) {
		t.Fatalf("Open failure = %v", err)
	}
	if got, want := channels.events, []string{"open", "cleanup:second", "cleanup:first"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("events = %#v, want %#v", got, want)
	}

	channels = &testChannels{close: errClose}
	_, err = contexture.WithChannels(context.Background(), channels, func(context.Context) (string, error) {
		channels.events = append(channels.events, "serve")
		return "", errServe
	})
	if !errors.Is(err, errServe) || !errors.Is(err, errClose) || !errors.Is(err, errCleanup) {
		t.Fatalf("combined failure = %v", err)
	}
}

func TestRuntimeServesWithApplicationChannels(t *testing.T) {
	channels := &testChannels{}
	application, err := contexture.DeclareApplication(contexture.ApplicationDeclaration{
		Name:     "lifecycle",
		Channels: channels,
		Roots: []contexture.Factory{func() contexture.Node {
			return &contexture.Role{Name: "operations", Description: "Operate.", Instructions: "Inspect."}
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	index, err := contexture.Compile(application)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := contexture.NewRuntime(index, contexture.AllRoots(), contexture.AllRoots(), nil)
	if err != nil {
		t.Fatal(err)
	}
	err = runtime.Serve(context.Background(), func(context.Context) error {
		channels.events = append(channels.events, "serve")
		return nil
	})
	if !errors.Is(err, errCleanup) {
		t.Fatalf("Runtime.Serve error = %v, want cleanup error", err)
	}
	want := []string{"open", "serve", "close", "cleanup:second", "cleanup:first"}
	if !reflect.DeepEqual(channels.events, want) {
		t.Fatalf("events = %#v, want %#v", channels.events, want)
	}
}

type panicChannels struct {
	events       []string
	openPanic    any
	closePanic   any
	cleanupPanic any
}

func (channels *panicChannels) Open(_ context.Context, registrar contexture.CleanupRegistrar) error {
	channels.events = append(channels.events, "open")
	registrar.Defer(func(context.Context) error {
		channels.events = append(channels.events, "cleanup:first")
		return nil
	})
	registrar.Defer(func(context.Context) error {
		channels.events = append(channels.events, "cleanup:second")
		if channels.cleanupPanic != nil {
			panic(channels.cleanupPanic)
		}
		return nil
	})
	if channels.openPanic != nil {
		panic(channels.openPanic)
	}
	return nil
}

func (channels *panicChannels) Close(context.Context) error {
	channels.events = append(channels.events, "close")
	if channels.closePanic != nil {
		panic(channels.closePanic)
	}
	return nil
}

func recovered(t *testing.T, call func()) any {
	t.Helper()
	var value any
	func() {
		defer func() { value = recover() }()
		call()
	}()
	if value == nil {
		t.Fatal("call did not panic")
	}
	return value
}

func TestWithChannelsOpenPanicUnwindsWithoutCloseAndPreservesOriginal(t *testing.T) {
	original, teardown := errors.New("open panic"), errors.New("cleanup panic")
	channels := &panicChannels{openPanic: original, cleanupPanic: teardown}
	got := recovered(t, func() {
		_, _ = contexture.WithChannels(context.Background(), channels, func(context.Context) (string, error) {
			t.Fatal("serve ran after Open panic")
			return "", nil
		})
	})
	if got != original {
		t.Fatalf("panic = %#v, want original %#v", got, original)
	}
	if want := []string{"open", "cleanup:second", "cleanup:first"}; !reflect.DeepEqual(channels.events, want) {
		t.Fatalf("events = %#v, want %#v", channels.events, want)
	}
}

func TestWithChannelsServePanicClosesThenUnwindsAndPreservesOriginal(t *testing.T) {
	original, closePanic, cleanupPanic := errors.New("serve panic"), errors.New("close panic"), errors.New("cleanup panic")
	channels := &panicChannels{closePanic: closePanic, cleanupPanic: cleanupPanic}
	got := recovered(t, func() {
		_, _ = contexture.WithChannels(context.Background(), channels, func(context.Context) (string, error) {
			channels.events = append(channels.events, "serve")
			panic(original)
		})
	})
	if got != original {
		t.Fatalf("panic = %#v, want original %#v", got, original)
	}
	if want := []string{"open", "serve", "close", "cleanup:second", "cleanup:first"}; !reflect.DeepEqual(channels.events, want) {
		t.Fatalf("events = %#v, want %#v", channels.events, want)
	}
}

type retainedRegistrarChannels struct{ registrar contexture.CleanupRegistrar }

func (channels *retainedRegistrarChannels) Open(_ context.Context, registrar contexture.CleanupRegistrar) error {
	channels.registrar = registrar
	return nil
}
func (*retainedRegistrarChannels) Close(context.Context) error { return nil }

func TestWithChannelsRejectsCleanupRegistrationOutsideOpen(t *testing.T) {
	channels := &retainedRegistrarChannels{}
	if _, err := contexture.WithChannels(context.Background(), channels, func(context.Context) (struct{}, error) { return struct{}{}, nil }); err != nil {
		t.Fatal(err)
	}
	got := recovered(t, func() { channels.registrar.Defer(func(context.Context) error { return nil }) })
	if message, ok := got.(string); !ok || message != "Contexture cleanup registration is outside the Channels.Open lifecycle" {
		t.Fatalf("late cleanup panic = %#v", got)
	}
}

type concurrentRegistrarChannels struct{ cleaned atomic.Int32 }

func (channels *concurrentRegistrarChannels) Open(_ context.Context, registrar contexture.CleanupRegistrar) error {
	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			registrar.Defer(func(context.Context) error { channels.cleaned.Add(1); return nil })
		}()
	}
	group.Wait()
	return nil
}
func (*concurrentRegistrarChannels) Close(context.Context) error { return nil }

func TestWithChannelsConcurrentOpenRegistrationsAreRaceSafe(t *testing.T) {
	channels := &concurrentRegistrarChannels{}
	if _, err := contexture.WithChannels(context.Background(), channels, func(context.Context) (struct{}, error) { return struct{}{}, nil }); err != nil {
		t.Fatal(err)
	}
	if channels.cleaned.Load() != 32 {
		t.Fatalf("cleanup count = %d", channels.cleaned.Load())
	}
}
