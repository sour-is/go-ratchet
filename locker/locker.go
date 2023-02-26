// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
//
// SPDX-License-Identifier: BSD-3-Clause

// Value Locker implements a lock queue where functions that wish to use a resource
// are placed in a queue to order utilization and prevent races. The function can 
// cancel its position using the context.
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
