package model

import (
	"context"
	"errors"
	"reflect"
	"sync"
)

// CleanupRegistrar records cleanups for resources acquired by Channels.Open.
// A Channel must register a cleanup immediately after acquiring its resource.
// Registrations after Open returns panic: retaining the registrar would make
// cleanup order and ownership unknowable.
type CleanupRegistrar interface {
	Defer(func(context.Context) error)
}

// ChannelHandle is any application-owned deployment dependency captured by an
// imperative ControllerManager. Only a value implementing Channels participates
// in lifecycle; every other value is passed to Tool invocations unchanged.
type ChannelHandle = any

// ChannelsLifecycle is an explicit marker embedded by lifecycle-owning channel
// implementations. A structural Open/Close lookalike without this marker stays
// an ordinary ChannelHandle.
type ChannelsLifecycle struct{}

// contextureChannelsLifecycle seals explicit lifecycle ownership to types that
// embed ChannelsLifecycle. External structural lookalikes cannot declare this
// package-private method themselves.
func (ChannelsLifecycle) contextureChannelsLifecycle() {}

// Channels owns application dependencies that must live around one serving scope.
// Open must not retain the registrar beyond the call.
type Channels interface {
	contextureChannelsLifecycle()
	Open(context.Context, CleanupRegistrar) error
	Close(context.Context) error
}

// WithChannels opens dependencies around serve and unwinds acquired resources
// in reverse order. Close runs only after a successful Open, while dependencies
// are still live. A cleanup error supplements, never replaces, the primary
// returned error.
//
// Panic paths retain the original panic: an Open panic runs registered cleanup
// without Close; a serve panic runs Close then cleanup. Panics raised while
// tearing down never replace the original panic, and remaining cleanup still
// runs. Without an original panic, a teardown panic is re-panicked after all
// cleanup has been attempted.
func WithChannels[T any](ctx context.Context, channels Channels, serve func(context.Context) (T, error)) (result T, primary error) {
	var zero T
	if serve == nil {
		return zero, errors.New("Contexture serving function must not be nil")
	}
	if channels == nil {
		return serve(ctx)
	}
	if nilChannels(channels) {
		return zero, errors.Join(ErrInvalidDeclaration, errors.New("Contexture Channels lifecycle must not be typed nil"))
	}

	registrar := &cleanupRegistrar{active: true}
	opened := false
	defer func() {
		originalPanic := recover()
		cleanups := registrar.finish()
		var teardownPanic any
		if opened {
			closeErr, closePanic := callLifecycle(func() error { return channels.Close(ctx) })
			if closePanic != nil {
				teardownPanic = closePanic
			} else if closeErr != nil {
				primary = combineErrors(primary, closeErr)
			}
		}
		for position := len(cleanups) - 1; position >= 0; position-- {
			cleanupErr, cleanupPanic := callLifecycle(func() error { return cleanups[position](ctx) })
			if cleanupPanic != nil {
				if teardownPanic == nil {
					teardownPanic = cleanupPanic
				}
				continue
			}
			if cleanupErr != nil {
				primary = combineErrors(primary, cleanupErr)
			}
		}
		if originalPanic != nil {
			panic(originalPanic)
		}
		if teardownPanic != nil {
			panic(teardownPanic)
		}
		if primary != nil {
			result = zero
		}
	}()

	err := channels.Open(ctx, registrar)
	// The registrar is valid only during Open. This is deliberately before
	// serve, so a retained registrar cannot add cleanup after acquisition.
	registrar.deactivate()
	if err != nil {
		return zero, err
	}
	opened = true
	return serve(ctx)
}

func nilChannels(channels Channels) bool {
	if channels == nil {
		return false
	}
	value := reflect.ValueOf(channels)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

// callLifecycle executes one teardown step without allowing its panic to skip
// subsequent steps. Its caller decides which panic is authoritative.
func callLifecycle(call func() error) (err error, panicValue any) {
	defer func() { panicValue = recover() }()
	return call(), nil
}

type cleanupRegistrar struct {
	mu       sync.Mutex
	active   bool
	cleanups []func(context.Context) error
}

func (registrar *cleanupRegistrar) Defer(cleanup func(context.Context) error) {
	if cleanup == nil {
		return
	}
	registrar.mu.Lock()
	defer registrar.mu.Unlock()
	if !registrar.active {
		panic("Contexture cleanup registration is outside the Channels.Open lifecycle")
	}
	registrar.cleanups = append(registrar.cleanups, cleanup)
}

func (registrar *cleanupRegistrar) finish() []func(context.Context) error {
	registrar.mu.Lock()
	defer registrar.mu.Unlock()
	registrar.active = false
	cleanups := append([]func(context.Context) error(nil), registrar.cleanups...)
	registrar.cleanups = nil
	return cleanups
}

func (registrar *cleanupRegistrar) deactivate() {
	registrar.mu.Lock()
	defer registrar.mu.Unlock()
	registrar.active = false
}

func combineErrors(primary, cleanup error) error {
	if primary == nil {
		return cleanup
	}
	return errors.Join(primary, cleanup)
}
