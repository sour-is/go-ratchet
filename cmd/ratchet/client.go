package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"git.mills.io/prologic/msgbus"
	"git.mills.io/prologic/msgbus/client"
	"github.com/keys-pub/keys"
	"github.com/oklog/ulid/v2"
	"github.com/sour-is/xochimilco/cmd/ratchet/locker"
	"go.mills.io/saltyim"
	"golang.org/x/sync/errgroup"
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
	hdlr map[command][]HandlerFn
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
	hdlr := make(map[command][]HandlerFn)

	return &Client{locker.New(sm), addr, bus, sub, hdlr}, nil
}

func (c *Client) Run(ctx context.Context) error {
	return c.sub.Run(ctx)
}

type HandlerFn func(ctx context.Context, sessID ulid.ULID, them string, msg string)
type command uint8

const (
	_ command = iota
	OnOfferSent
	OnOfferReceived
	OnSessionStarted
	OnMessageReceived
	OnMessageSent
	OnSessionClosed
	OnSaltyReceived
	OnSaltySent
)

func (c *Client) Chat(ctx context.Context, them string) (bool, error) {
	established := false
	return established, c.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		session, err := sm.Get(sm.ByName(them))

		// handle initiating a new chat
		if err != nil && errors.Is(err, os.ErrNotExist) {
			session, err = sm.New(them)
			if err != nil {
				return err
			}
			msg, err := session.Offer()
			if err != nil {
				return err
			}

			err = c.sendMsg(session, msg)
			if err != nil {
				return err
			}
			err = sm.Put(session)
			if err != nil {
				return err
			}

			defer func() {
				err = c.dispatch(ctx, OnOfferSent, sm.ByName(them), them, "")
			}()

			return err
		}
		if err != nil {
			return err
		}

		// handle a pending ack from offer.
		if len(session.PendingAck) > 0 {
			err = c.sendMsg(session, session.PendingAck)
			if err != nil {
				return err
			}

			defer func() {
				err = c.dispatch(ctx, OnSessionStarted, sm.ByName(them), them, "")
			}()

			established = true
			session.PendingAck = ""
			return sm.Put(session)
		}

		return err
	})
}

func (c *Client) Send(ctx context.Context, them, msg string) error {
	return c.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		session, err := sm.Get(sm.ByName(them))
		if err != nil {
			return err
		}

		msg, err := session.Send([]byte(msg))
		if err != nil {
			return err
		}

		err = c.sendMsg(session, msg)
		if err != nil {
			return err
		}

		defer func() {
			err = c.dispatch(ctx, OnMessageSent, sm.ByName(them), them, msg)
		}()

		return sm.Put(session)
	})
}

func (c *Client) Close(ctx context.Context, them string) error {
	return c.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		session, err := sm.Get(sm.ByName(them))
		if err != nil {
			return err
		}

		msg, err := session.Close()
		if err != nil {
			return err
		}

		err = c.sendMsg(session, msg)
		if err != nil {
			return err
		}

		err = c.dispatch(ctx, OnSessionClosed, sm.ByName(them), them, "")
		if err != nil {
			return err
		}

		err = sm.Delete(session)
		if err != nil {
			return err
		}

		return err
	})
}

func (c *Client) SendSalty(ctx context.Context, them, msg string) error { return nil }

func (c *Client) Handle(cmd command, fn HandlerFn) {
	c.hdlr[cmd] = append(c.hdlr[cmd], fn)
}

func (c *Client) dispatch(ctx context.Context, cmd command, sessID ulid.ULID, them string, msg string) error {
	hdlrs := c.hdlr[cmd]

	wg, ctx := errgroup.WithContext(ctx)

	for i := range hdlrs {
		hdlr := hdlrs[i]
		wg.Go(func() error {
			hdlr(ctx, sessID, them, msg)
			return nil
		})
	}
	return wg.Wait()
}
func (c *Client) sendMsg(session *Session, msg string) error {
	_, err := http.DefaultClient.Post(session.Endpoint, "text/plain", strings.NewReader(msg))
	if err != nil {
		return err
	}
	return nil
}
