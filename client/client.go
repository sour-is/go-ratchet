// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
// SPDX-License-Identifier: BSD-3-Clause
package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/keys-pub/keys"
	"github.com/oklog/ulid/v2"
	"go.mills.io/salty"
	"go.salty.im/ratchet/locker"
	"go.salty.im/ratchet/session"
	"go.salty.im/ratchet/xochimilco"
	"go.salty.im/saltyim"
	"go.yarn.social/lextwt"
	"golang.org/x/sync/errgroup"
)

type SessionManager interface {
	Identity() *keys.EdX25519Key
	ByName(name string) ulid.ULID
	New(them string) (*session.Session, error)
	Get(id ulid.ULID) (*session.Session, error)
	Put(sess *session.Session) error
	Delete(sess *session.Session) error
	Sessions() []session.Pair[string, ulid.ULID]
}

type (
	Addr   = saltyim.Addr
	Event  = lextwt.SaltyEvent
	User   = lextwt.SaltyUser
	Msg    = lextwt.SaltyText
	Pubkey = keys.EdX25519PublicKey
)

type Client struct {
	BaseCTX func() context.Context

	sm   *locker.Locked[SessionManager]
	addr Addr

	driver Driver

	on map[any][]any
}
type Option interface {
	ApplyClient(*Client)
}

type Driver interface{ Run(context.Context) error }

type withDriver struct {
	Driver
}

func WithDriver(d Driver) withDriver {
	return withDriver{d}
}

func (d withDriver) ApplyClient(c *Client) {
	c.driver = d.Driver
}

func New(sm SessionManager, me string, opts ...Option) (*Client, error) {
	addr, err := saltyim.LookupAddr(me)
	if err != nil {
		return nil, fmt.Errorf("lookup addr: %w", err)
	}

	c := &Client{
		sm:     locker.New(sm),
		addr:   addr,
		driver: nilDriver{},
		on:     make(map[any][]any),
	}

	for _, o := range opts {
		o.ApplyClient(c)
	}

	On(c, c.handleSaltPack)
	On(c, c.handleRatchet)
	On(c, c.handleOther)

	return c, nil
}

func (c *Client) Run(ctx context.Context) error {
	return c.driver.Run(ctx)
}
func (c *Client) Me() saltyim.Addr {
	return c.addr
}

func On[T any](c *Client, fn func(context.Context, T)) {
	var id T
	c.on[id] = append(c.on[id], fn)
}

func dispatch[T any](ctx context.Context, c *Client, args T) error {
	var id T
	hdlrs := c.on[id]

	wg, ctx := errgroup.WithContext(ctx)

	for i := range hdlrs {
		hdlr := hdlrs[i].(func(context.Context, T))
		wg.Go(func() error {
			hdlr(ctx, args)
			return nil
		})
	}
	return wg.Wait()
}

type OnInput struct {
	Position int64
	Payload  string
}
type OnOfferSent struct {
	ID   ulid.ULID
	Them string
	Raw  string
}
type OnOfferReceived struct {
	ID         ulid.ULID
	Them       string
	PendingAck string
}
type OnSessionStarted struct {
	ID   ulid.ULID
	Them string
}
type OnMessageReceived struct {
	ID   ulid.ULID
	Them string
	Raw  string
	Msg  *Msg
}
type OnEventReceived struct {
	ID   ulid.ULID
	Them string
	Raw  string
	Msg  *Event
}
type OnMessageSent struct {
	ID     ulid.ULID
	Them   string
	Raw    string
	Msg    *Msg
	Sealed string
}
type OnCloseSent struct {
	ID     ulid.ULID
	Them   string
	Sealed string
}
type OnSessionClosed struct {
	ID   ulid.ULID
	Them string
}
type OnSaltyTextReceived struct {
	Pubkey *Pubkey
	Msg    *Msg
}
type OnSaltyEventReceived struct {
	Pubkey *Pubkey
	Event  *Event
}
type OnSaltySent struct {
	Them string
	Addr saltyim.Addr
	Msg  *Msg
	Raw  string
}
type OnReceived struct {
	Raw string // raw is string to be hashable.
}

func (c *Client) Use(ctx context.Context, fn func(context.Context, SessionManager) error) error {
	return c.sm.Use(ctx, fn)
}

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

			return dispatch(ctx, c, OnOfferSent{
				ID:   toULID(session.LocalUUID),
				Them: them,
				Raw:  msg,
			})
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

			session.PendingAck = ""
			err = sm.Put(session)
			if err != nil {
				return err
			}
			established = true

			return dispatch(ctx, c, OnSessionStarted{toULID(session.LocalUUID), them})
		}

		return err
	})
}
func (c *Client) Send(ctx context.Context, them, text string, events ...*Event) error {
	return c.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		session, err := sm.Get(sm.ByName(them))
		if err != nil {
			return err
		}

		msgID := toULID(encTime(session.LocalUUID))

		msg := lextwt.NewSaltyText(
			lextwt.NewDateTime(ulid.Time(msgID.Time()), ""),
			lextwt.NewSaltyUser(c.Me().User(), c.Me().Domain()),
			toElems(lextwt.NewText(text), events)...,
		)

		data, err := session.Send([]byte(msg.Literal()))
		if err != nil {
			return err
		}

		err = c.sendMsg(session, data)
		if err != nil {
			return err
		}

		err = sm.Put(session)
		if err != nil {
			return err
		}

		return dispatch(ctx, c, OnMessageSent{
			ID:     msgID,
			Them:   them,
			Raw:    msg.Literal(),
			Msg:    msg,
			Sealed: data,
		})
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

		err = dispatch(ctx, c, OnCloseSent{
			ID:     toULID(session.LocalUUID),
			Them:   them,
			Sealed: msg,
		})
		if err != nil {
			return err
		}

		err = dispatch(ctx, c, OnSessionClosed{toULID(session.LocalUUID), them})
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
func (c *Client) SendSalty(ctx context.Context, them, text string, events ...*Event) error {
	addr, err := saltyim.LookupAddr(them)
	if err != nil {
		return err
	}

	return c.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		msg := lextwt.NewSaltyText(
			lextwt.NewDateTime(time.Now(), ""),
			lextwt.NewSaltyUser(addr.User(), addr.Domain()),
			toElems(lextwt.NewText(text), events)...,
		)

		b, err := salty.Encrypt(sm.Identity(), []byte(msg.Literal()), []string{addr.Key().ID().String()})
		if err != nil {
			return fmt.Errorf("error encrypting message to %s: %w", addr, err)
		}

		err = saltyim.Send(addr.Endpoint().String(), string(b), addr.Cap())
		if err != nil {
			return err
		}

		return dispatch(ctx, c, OnSaltySent{
			Them: them,
			Addr: addr,
			Raw:  msg.Literal(),
			Msg:  msg,
		})
	})
}

func (c *Client) Context() (context.Context, context.CancelFunc) {
	ctx := context.Background()
	if c.BaseCTX != nil {
		ctx = c.BaseCTX()
	}
	return context.WithCancel(ctx)
}

func (c *Client) Input(in OnInput) error {
	ctx, cancel := c.Context()
	defer cancel()

	return dispatch(ctx, c, in)
}

func (c *Client) handleSaltPack(ctx context.Context, in OnInput) {
	input := string(in.Payload)

	if !strings.HasPrefix(input, "BEGIN SALTPACK ENCRYPTED MESSAGE.") {
		return
	}

	err := c.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		// Update session manager position in stream if supported.
		if s, ok := sm.(interface{ SetPosition(int64) }); ok {
			s.SetPosition(in.Position + 1)
		}

		text, key, err := salty.Decrypt(sm.Identity(), []byte(in.Payload))
		if err != nil {
			return err
		}

		msg, err := lextwt.ParseSalty(string(text))
		if err != nil {
			return err
		}

		switch msg := msg.(type) {
		case *lextwt.SaltyEvent:
			return dispatch(ctx, c, OnSaltyEventReceived{key, msg})

		case *lextwt.SaltyText:
			return dispatch(ctx, c, OnSaltyTextReceived{key, msg})

		}

		return nil
	})

	if err != nil {
		dispatch(ctx, c, err)
	}
}

func (c *Client) handleRatchet(ctx context.Context, in OnInput) {
	input := string(in.Payload)

	if !(strings.HasPrefix(input, "!RAT!") && strings.HasSuffix(input, "!CHT!")) {
		return
	}

	id, xmsg, err := readMsg(input)
	if err != nil {
		err = fmt.Errorf("reading msg: %w", err)
		dispatch(ctx, c, err)

		return
	}

	err = c.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		// Update session manager position in stream if supported.
		if s, ok := sm.(interface{ SetPosition(int64) }); ok {
			s.SetPosition(in.Position + 1)
		}

		// unseal message if required.
		if sealed, ok := xmsg.(interface {
			Unseal(priv, pub *[32]byte) (m xochimilco.Msg, err error)
		}); ok {
			xmsg, err = sealed.Unseal(
				sm.Identity().X25519Key().Bytes32(),
				sm.Identity().X25519Key().PublicKey().Bytes32(),
			)
			if err != nil {
				return err
			}
		}

		var sess *session.Session

		// offer messages have a nick embeded in the payload.
		if offer, ok := xmsg.(interface {
			Nick() string
		}); ok {
			sess, err = sm.New(offer.Nick())
			if err != nil {
				return fmt.Errorf("get session: %w", err)
			}
		} else {
			sess, err = sm.Get(id)
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("get session: %w", err)
			}
		}

		if sess == nil {
			return nil
		}

		isEstablished, isClosed, plaintext, err := sess.ReceiveMsg(xmsg)
		if err != nil {
			return fmt.Errorf("session receive: %w", err)
		}

		if sess.PendingAck != "" {
			err = dispatch(ctx, c, OnOfferReceived{
				ID:         toULID(xmsg.ID()),
				Them:       sess.Name,
				PendingAck: sess.PendingAck,
			})
			if err != nil {
				return err
			}
		}

		err = sm.Put(sess)
		if err != nil {
			return err
		}

		switch {
		case isClosed:
			err = sm.Delete(sess)
			if err != nil {
				return err
			}

			return dispatch(ctx, c, OnSessionClosed{toULID(xmsg.ID()), sess.Name})
		case isEstablished:
			return dispatch(ctx, c, OnSessionStarted{toULID(xmsg.ID()), sess.Name})
		}

		msg, _ := lextwt.ParseSalty(string(plaintext))

		switch msg := msg.(type) {
		case *Msg:
			return dispatch(ctx, c, OnMessageReceived{
				ID:   toULID(xmsg.ID()),
				Them: sess.Name,
				Raw:  string(plaintext),
				Msg:  msg,
			})

		case *Event:
			return dispatch(ctx, c, OnEventReceived{
				ID:   toULID(xmsg.ID()),
				Them: sess.Name,
				Raw:  string(plaintext),
				Msg:  msg,
			})

		}

		return nil
	})

	if err != nil {
		dispatch(ctx, c, err)
	}
}

func (c *Client) handleOther(ctx context.Context, in OnInput) {
	input := string(in.Payload)

	if strings.HasPrefix(input, "!RAT!") && strings.HasSuffix(input, "!CHT!") {
		return
	}

	if strings.HasPrefix(input, "BEGIN SALTPACK ENCRYPTED MESSAGE.") {
		return
	}

	dispatch(ctx, c, OnReceived{string(in.Payload)})
}

func (c *Client) sendMsg(session *session.Session, msg string) error {
	_, err := http.DefaultClient.Post(session.Endpoint, "text/plain", strings.NewReader(msg))
	if err != nil {
		return err
	}
	return nil
}
func readMsg(input string) (id ulid.ULID, msg xochimilco.Msg, err error) {
	msg, err = xochimilco.Parse(input)
	if err != nil {
		return
	}

	copy(id[:], msg.ID())
	return
}
func toULID(b []byte) ulid.ULID {
	var id ulid.ULID
	copy(id[:], b)
	return id
}

func toElems(e lextwt.Elem, events []*Event) []lextwt.Elem {
	lis := make([]lextwt.Elem, 0, len(events)+1)
	lis = append(lis, e)
	for i := range events {
		lis = append(lis, events[i])
	}
	return lis
}

type nilDriver struct{}

func (nilDriver) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func encTime(in []byte) []byte {
	u := ulid.ULID{}
	copy(u[:], in)
	_ = u.SetTime(ulid.Now())
	return u[:]
}
