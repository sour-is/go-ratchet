// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
//
// SPDX-License-Identifier: BSD-3-Clause
package locker_test

import (
	"context"
	"testing"

	"github.com/matryer/is"
	"go.salty.im/ratchet/locker"
)

type config struct {
	Value   string
	Counter int
}

func TestLocker(t *testing.T) {
	is := is.New(t)

	value := locker.New(&config{})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := value.Use(ctx, func(ctx context.Context, c *config) error {
		c.Value = "one"
		c.Counter++
		return nil
	})
	is.NoErr(err)

	var cp *config
	err = value.Use(context.Background(), func(ctx context.Context, c *config) error {
		cp = &config{
			Value:   c.Value,
			Counter: c.Counter,
		}
		return nil
	})

	is.NoErr(err)
	is.Equal(cp.Value, "one")
	is.Equal(cp.Counter, 1)

	wait := make(chan struct{})

	go value.Use(ctx, func(ctx context.Context, c *config) error {
		c.Value = "two"
		c.Counter++
		close(wait)
		return nil
	})

	<-wait
	cancel()

	err = value.Use(ctx, func(ctx context.Context, c *config) error {
		c.Value = "three"
		c.Counter++
		return nil
	})
	is.True(err != nil)

	err = value.Use(context.Background(), func(ctx context.Context, c *config) error {
		cp = &config{
			Value:   c.Value,
			Counter: c.Counter,
		}
		return nil
	})

	is.NoErr(err)
	is.Equal(cp.Value, "two")
	is.Equal(cp.Counter, 2)
}
