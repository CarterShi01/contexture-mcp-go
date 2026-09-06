package contexture_test

import (
	"context"
	"errors"
	"reflect"
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
