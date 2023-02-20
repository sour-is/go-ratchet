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
	"github.com/sour-is/xochimilco"
	"github.com/sour-is/xochimilco/cmd/ratchet/locker"
	"go.mills.io/salty"
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
	BaseCTX func() context.Context

	sm   *locker.Locked[SessionManager]
	addr saltyim.Addr
	bus  *client.Client
	sub  *client.Subscriber
	hdlr map[command][]HandlerFn
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
		hdlr: make(map[command][]HandlerFn),
	}
	cl.sub = cl.bus.Subscribe(inbox, pos, cl.msgbusHandler)

	return cl, nil
}

func (c *Client) Run(ctx context.Context) error {
	return c.sub.Run(ctx)
}

type HandlerFn func(ctx context.Context, sessionID []byte, them string, msg string)
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
	OnOtherReceived
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

			return c.dispatch(ctx, OnOfferSent, session.LocalUUID, them, "")
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

			return c.dispatch(ctx, OnSessionStarted, session.LocalUUID, them, "")
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

		return c.dispatch(ctx, OnMessageSent, session.LocalUUID, them, input)
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

		err = c.dispatch(ctx, OnSessionClosed, session.LocalUUID, them, "")
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
		return c.dispatch(ctx, OnSaltySent, nil, them, msg)	
	})
}

func (c *Client) Handle(cmd command, fn HandlerFn) {
	c.hdlr[cmd] = append(c.hdlr[cmd], fn)
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

			msg, _, err := salty.Decrypt(sm.Identity(), []byte(input))
			if err != nil {
				return err
			}

			return c.dispatch(ctx, OnSaltyReceived, nil, "", string(msg))
		})
	}

	if !(strings.HasPrefix(input, "!RAT!") && strings.HasSuffix(input, "!CHT!")) {
		return c.dispatch(ctx, OnOtherReceived, nil, "", input)
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

		var sess *Session

		// offer messages have a nick embeded in the payload.
		if offer, ok := msg.(interface {
			Nick() string
		}); ok {
			sess, err = sm.New(offer.Nick())
			if err != nil {
				return fmt.Errorf("get session: %w", err)
			}
			err = c.dispatch(ctx, OnOfferReceived, msg.ID(), offer.Nick(), "")
			if err != nil {
				return err
			}
		} else {
			sess, err = sm.Get(id)
			if errors.Is(err, os.ErrNotExist) {
				log("no sesson", id)
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
			log("GOT: closing session...")
			err = sm.Delete(sess)
			if err != nil {
				return err
			}

			return c.dispatch(ctx, OnSessionClosed, msg.ID(), sess.Name, "")
		case isEstablished:
			log("GOT: session established with ", sess.Name, "...", sess.Endpoint)
			return c.dispatch(ctx, OnSessionStarted, msg.ID(), sess.Name, "")
		}

		return c.dispatch(ctx, OnMessageReceived, msg.ID(), sess.Name, string(plaintext))
	})
}

func (c *Client) dispatch(ctx context.Context, cmd command, sessionID []byte, them string, msg string) error {
	hdlrs := c.hdlr[cmd]

	wg, ctx := errgroup.WithContext(ctx)

	for i := range hdlrs {
		hdlr := hdlrs[i]
		wg.Go(func() error {
			hdlr(ctx, sessionID, them, msg)
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
