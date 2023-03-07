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
		sub := bus.Subscribe(inbox, pos, hdlr(c.Handler))

		client.WithDriver(sub)
	})
}

type fn func(*client.Client)

func (fn fn) ApplyClient(c *client.Client) {
	fn(c)
}

func hdlr(fn func(int64, string) error) msgbus.HandlerFunc {
	return func(msg *msgbus.Message) error {
		_ = fn(msg.ID, string(msg.Payload))
		return nil
	}
}
