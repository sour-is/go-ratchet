package locker

import (
	"context"
	"fmt"
)

type Locked[T any] struct {
	state chan T
}

// New creates a new locker for the given value.
func New[T any](initial T) *Locked[T] {
	s := &Locked[T]{}
	s.state = make(chan T, 1)
	s.state <- initial
	return s
}

// Use will call the function with the locked value
func (s *Locked[T]) Use(ctx context.Context, fn func(context.Context, T) error) error {
	if s == nil {
		return fmt.Errorf("locker not initialized")
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	select {
	case state := <-s.state:
		defer func() { s.state <- state }()
		return fn(ctx, state)
	case <-ctx.Done():
		return ctx.Err()
	}
}
