package model

import (
	"context"
	"errors"
)

// CleanupRegistrar records cleanups for resources acquired by Channels.Open.
// A Channel must register a cleanup immediately after acquiring its resource.
type CleanupRegistrar interface {
	Defer(func(context.Context) error)
}

// Channels owns application dependencies that must live around one serving scope.
// Open must not retain the registrar beyond the call.
type Channels interface {
	Open(context.Context, CleanupRegistrar) error
	Close(context.Context) error
}

// WithChannels opens dependencies around serve and unwinds acquired resources in
// reverse order. Close runs only after a successful Open, while dependencies are
// still live. A cleanup error supplements, never replaces, the primary error.
func WithChannels[T any](ctx context.Context, channels Channels, serve func(context.Context) (T, error)) (T, error) {
	var zero T
	if serve == nil {
		return zero, errors.New("Contexture serving function must not be nil")
	}
	if channels == nil {
		return serve(ctx)
	}

	cleanups := []func(context.Context) error{}
	registrar := cleanupRegistrar{cleanups: &cleanups}
	opened := false
	result, primary := channelsResult(ctx, channels, registrar, serve, &opened)
	if opened {
		if err := channels.Close(ctx); err != nil {
			primary = combineErrors(primary, err)
		}
	}
	for position := len(cleanups) - 1; position >= 0; position-- {
		if err := cleanups[position](ctx); err != nil {
			primary = combineErrors(primary, err)
		}
	}
	if primary != nil {
		return zero, primary
	}
	return result, nil
}

func channelsResult[T any](ctx context.Context, channels Channels, registrar CleanupRegistrar, serve func(context.Context) (T, error), opened *bool) (T, error) {
	var zero T
	if err := channels.Open(ctx, registrar); err != nil {
		return zero, err
	}
	*opened = true
	return serve(ctx)
}

type cleanupRegistrar struct {
	cleanups *[]func(context.Context) error
}

func (registrar cleanupRegistrar) Defer(cleanup func(context.Context) error) {
	if cleanup != nil {
		*registrar.cleanups = append(*registrar.cleanups, cleanup)
	}
}

func combineErrors(primary, cleanup error) error {
	if primary == nil {
		return cleanup
	}
	return errors.Join(primary, cleanup)
}
