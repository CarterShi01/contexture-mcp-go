package server

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/CarterShi01/contexture-mcp-go/managed"
)

func TestManagedHostNativeOneShotTrustedDispatch(t *testing.T) {
	for _, mode := range []string{"missing", "duplicate", "swallowed-error", "late"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			var late func(context.Context, ManagedHostScope) error
			businessError := errors.New("business failed")
			err := oneHostDispatch(func(call func(context.Context, ManagedHostScope) error) error {
				switch mode {
				case "missing":
					return nil
				case "duplicate":
					_ = call(context.Background(), ManagedHostScope{})
					return call(context.Background(), ManagedHostScope{})
				case "swallowed-error":
					_ = call(context.Background(), ManagedHostScope{})
					return nil
				case "late":
					late = call
					return nil
				}
				return nil
			}, func(context.Context, ManagedHostScope) error {
				calls.Add(1)
				if mode == "swallowed-error" {
					return businessError
				}
				return nil
			})
			if err == nil {
				t.Fatal("broken trusted dispatch accepted")
			}
			if mode == "swallowed-error" && !errors.Is(err, businessError) {
				t.Fatal("business error lost")
			}
			if mode == "late" {
				if !errors.Is(late(context.Background(), ManagedHostScope{}), managed.ErrDenied) || calls.Load() != 0 {
					t.Fatal("retained callback executed")
				}
			}
			if calls.Load() > 1 {
				t.Fatal("duplicate business dispatch")
			}
		})
	}
}

func TestManagedHostNativeEarlyTrustedReturnJoinsRealWork(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- oneHostDispatch(func(call func(context.Context, ManagedHostScope) error) error {
			go func() { _ = call(context.Background(), ManagedHostScope{}) }()
			<-entered
			return nil
		}, func(context.Context, ManagedHostScope) error { close(entered); <-release; return nil })
	}()
	<-entered
	select {
	case <-done:
		t.Fatal("early return abandoned business work")
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if !errors.Is(<-done, managed.ErrDenied) {
		t.Fatal("asynchronous trusted dispatch accepted")
	}
}
