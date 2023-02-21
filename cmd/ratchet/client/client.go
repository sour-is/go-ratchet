package client

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
	"github.com/sour-is/xochimilco"
	"github.com/sour-is/xochimilco/cmd/ratchet/locker"
	"github.com/sour-is/xochimilco/cmd/ratchet/session"
	"go.mills.io/salty"
	"go.mills.io/saltyim"
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

type Client struct {
	BaseCTX func() context.Context

	sm   *locker.Locked[SessionManager]
	addr saltyim.Addr
	bus  *client.Client
	sub  *client.Subscriber

	on map[any][]any
}

func NewClient(sm SessionManager, me string) (*Client, error) {
	addr, err := saltyim.LookupAddr(me)
	if err != nil {
		return nil, fmt.Errorf("lookup addr: %w", err)
	}

	var pos int64 = -1
	if p, ok := sm.(interface{ Position() int64 }); ok {
		pos = p.Position()
	}

	uri, inbox := saltyim.SplitInbox(addr.Endpoint().String())

	cl := &Client{
		sm:   locker.New(sm),
		addr: addr,
		bus:  client.NewClient(uri, nil),
		on:   make(map[any][]any),
	}
	cl.sub = cl.bus.Subscribe(inbox, pos, cl.msgbusHandler)

	return cl, nil
}

func (c *Client) Run(ctx context.Context) error {
	return c.sub.Run(ctx)
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

type OnOfferSent struct {
	ID   ulid.ULID
	Them string
}
type OnOfferReceived struct {
	ID   ulid.ULID
	Them string
}
type OnSessionStarted struct {
	ID   ulid.ULID
	Them string
}
type OnMessageReceived struct {
	ID   ulid.ULID
	Them string
	Msg  string
}
type OnMessageSent struct {
	ID     ulid.ULID
	Them   string
	Msg    string
	Sealed string
}
type OnSessionClosed struct {
	ID   ulid.ULID
	Them string
}
type OnSaltyTextReceived struct {
	Pubkey *keys.EdX25519PublicKey
	Msg    *lextwt.SaltyText
}
type OnSaltyEventReceived struct {
	Pubkey *keys.EdX25519PublicKey
	Event  *lextwt.SaltyEvent
}
type OnSaltySent struct {
	Them string
	Addr saltyim.Addr
	Msg  string
}
type OnOtherReceived struct {
	Raw string
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

			return dispatch(ctx, c, OnOfferSent{toULID(session.LocalUUID), them})
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

			err = sm.Put(session)
			if err != nil {
				return err
			}
			established = true
			session.PendingAck = ""

			return dispatch(ctx, c, OnSessionStarted{toULID(session.LocalUUID), them})
		}

		return err
	})
}
func (c *Client) Send(ctx context.Context, them, input string) error {
	return c.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		session, err := sm.Get(sm.ByName(them))
		if err != nil {
			return err
		}

		msg, err := session.Send([]byte(input))
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

		return dispatch(ctx, c, OnMessageSent{toULID(session.LocalUUID), them, input, msg})
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
func (c *Client) SendSalty(ctx context.Context, them, msg string) error {
	addr, err := saltyim.LookupAddr(them)
	if err != nil {
		return err
	}

	return c.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		b, err := salty.Encrypt(sm.Identity(), saltyim.PackMessage(c.addr, msg), []string{addr.Key().ID().String()})
		if err != nil {
			return fmt.Errorf("error encrypting message to %s: %w", addr, err)
		}

		err = saltyim.Send(addr.Endpoint().String(), string(b), addr.Cap())
		if err != nil {
			return err
		}

		return dispatch(ctx, c, OnSaltySent{them, addr, msg})
	})
}

func (c *Client) Context() (context.Context, context.CancelFunc) {
	ctx := context.Background()
	if c.BaseCTX != nil {
		ctx = c.BaseCTX()
	}
	return context.WithCancel(ctx)
}

func (c *Client) msgbusHandler(in *msgbus.Message) error {
	ctx, cancel := c.Context()
	defer cancel()

	input := string(in.Payload)

	if strings.HasPrefix(input, "BEGIN SALTPACK ENCRYPTED MESSAGE.") {
		return c.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
			// Update session manager position in stream if supported.
			if pos, ok := sm.(interface{ SetPosition(int64) }); ok {
				pos.SetPosition(in.ID + 1)
			}

			text, key, err := salty.Decrypt(sm.Identity(), []byte(input))
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
	}

	if !(strings.HasPrefix(input, "!RAT!") && strings.HasSuffix(input, "!CHT!")) {
		return dispatch(ctx, c, OnOtherReceived{input})
	}

	id, msg, err := readMsg(input)
	if err != nil {
		return fmt.Errorf("reading msg: %w", err)
	}

	return c.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		// Update session manager position in stream if supported.
		if pos, ok := sm.(interface{ SetPosition(int64) }); ok {
			pos.SetPosition(in.ID + 1)
		}

		// unseal message if required.
		if sealed, ok := msg.(interface {
			Unseal(priv, pub *[32]byte) (m xochimilco.Msg, err error)
		}); ok {
			msg, err = sealed.Unseal(
				sm.Identity().X25519Key().Bytes32(),
				sm.Identity().X25519Key().PublicKey().Bytes32(),
			)
			if err != nil {
				return err
			}
		}

		var sess *session.Session

		// offer messages have a nick embeded in the payload.
		if offer, ok := msg.(interface {
			Nick() string
		}); ok {
			sess, err = sm.New(offer.Nick())
			if err != nil {
				return fmt.Errorf("get session: %w", err)
			}
			err = dispatch(ctx, c, OnOfferReceived{toULID(msg.ID()), offer.Nick()})
			if err != nil {
				return err
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

		isEstablished, isClosed, plaintext, err := sess.ReceiveMsg(msg)
		if err != nil {
			return fmt.Errorf("session receive: %w", err)
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

			return dispatch(ctx, c, OnSessionClosed{toULID(msg.ID()), sess.Name})
		case isEstablished:
			return dispatch(ctx, c, OnSessionStarted{toULID(msg.ID()), sess.Name})
		}

		return dispatch(ctx, c, OnMessageReceived{toULID(msg.ID()), sess.Name, string(plaintext)})
	})
}

func (c *Client) sendMsg(session *session.Session, msg string) error {
	_, err := http.DefaultClient.Post(session.Endpoint, "text/plain", strings.NewReader(msg))
	if err != nil {
		return err
	}
	return nil
}
func readMsg(input string) (id ulid.ULID, msg xochimilco.Msg, err error) {
	// log(input)

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
