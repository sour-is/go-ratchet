// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
// SPDX-License-Identifier: BSD-3-Clause
package driver_msgbus

import (
	"git.mills.io/prologic/msgbus"
	mb "git.mills.io/prologic/msgbus/client"
	"go.salty.im/saltyim"

	"go.salty.im/ratchet/client"
)

func WithMsgbus(pos int64) client.Option {
	return fn(func(c *client.Client) {
		addr := c.Me()
		uri, inbox := saltyim.SplitInbox(addr.Endpoint().String())
		bus := mb.NewClient(uri, nil)
		sub := bus.Subscribe(inbox, pos, hdlr(c.Input))

		client.WithDriver(sub).ApplyClient(c)
	})
}

type fn func(*client.Client)

func (fn fn) ApplyClient(c *client.Client) {
	fn(c)
}

func hdlr(fn func(client.OnInput) error) msgbus.HandlerFunc {
	return func(msg *msgbus.Message) error {
		_ = fn(client.OnInput{Position: msg.ID, Payload: string(msg.Payload)})
		return nil
	}
}
