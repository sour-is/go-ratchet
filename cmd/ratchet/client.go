package main

import (
	"context"
	"fmt"

	"git.mills.io/prologic/msgbus"
	"git.mills.io/prologic/msgbus/client"
	"github.com/keys-pub/keys"
	"github.com/oklog/ulid/v2"
	"github.com/sour-is/xochimilco/cmd/ratchet/locker"
	"go.mills.io/saltyim"
)

type SessionManager interface {
	Identity() *keys.EdX25519Key
	ByName(name string) ulid.ULID
	New(them string) (*Session, error)
	Get(id ulid.ULID) (*Session, error)
	Put(sess *Session) error
	Delete(sess *Session) error
	Sessions() []pair[string, ulid.ULID]
}

type Client struct {
	sm   *locker.Locked[SessionManager]
	addr saltyim.Addr
	bus  *client.Client
	sub  *client.Subscriber
}

func NewClient(sm SessionManager, me string, handleFn func(in *msgbus.Message) error) (*Client, error) {
	addr, err := saltyim.LookupAddr(me)
	if err != nil {
		return nil, fmt.Errorf("lookup addr: %w", err)
	}

	var pos int64 = -1
	if p, ok := sm.(interface{ Position() int64 }); ok {
		pos = p.Position()
	}

	uri, inbox := saltyim.SplitInbox(addr.Endpoint().String())
	bus := client.NewClient(uri, nil)
	sub := bus.Subscribe(inbox, pos, handleFn)

	return &Client{locker.New(sm), addr, bus, sub}, nil
}

func (c *Client) Run(ctx context.Context) error {
	return c.sub.Run(ctx)
}
