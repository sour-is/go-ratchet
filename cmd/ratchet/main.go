package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"

	"github.com/docopt/docopt-go"
	"go.mills.io/saltyim"

	"github.com/sour-is/xochimilco"
	"github.com/sour-is/xochimilco/cmd/ratchet/xdg"
)

var usage = `Rachet Chat.
Usage:
  ratchet [options] recv
  ratchet [options] (offer|send|close) <them>
  ratchet [options] chat [<them>]
  ratchet [options] ui

Args:
  <them>             Receiver acct name to use in offer. 

Options:
  --key <key>        Sender private key [default: ` + xdg.Get(xdg.EnvConfigHome, "rachet/$USER.key") + `]
  --state <state>    Session state path [default: ` + xdg.Get(xdg.EnvDataHome, "rachet") + `]
  --msg <msg>        Msg to read in. [default to read Standard Input]
  --msg-file <file>  File to read input from.
  --msg-stdin        Read standard input.
  --post             Send to msgbus
`

type opts struct {
	Offer bool `docopt:"offer"`
	Send  bool `docopt:"send"`
	Recv  bool `docopt:"recv"`
	Close bool `docopt:"close"`
	Chat  bool `docopt:"chat"`
	UI    bool `docopt:"ui"`

	Them string `docopt:"<them>"`

	Key      string `docopt:"--key"`
	Session  string `docopt:"--session"`
	State    string `docopt:"--state"`
	Msg      string `docopt:"--msg"`
	MsgFile  string `docopt:"--msg-file"`
	MsgStdin bool   `docopt:"--msg-stdin"`
	Post     bool   `docopt:"--post"`
}

func main() {
	o, err := docopt.ParseDoc(usage)
	if err != nil {
		log(err)
		os.Exit(2)
	}

	var opts opts
	o.Bind(&opts)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	go func() {
		<-ctx.Done()
		defer cancel() // restore interrupt function
	}()

	if err := run(ctx, opts); err != nil {
		log(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts opts) error {
	// log(opts)

	switch {
	case opts.Offer:
		me, key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		sm, close, err := NewSessionManager(opts.State, me, key)
		if err != nil {
			return err
		}
		defer close()

		sess, err := sm.New(opts.Them)
		if err != nil {
			return fmt.Errorf("read session: %w", err)
		}
		// log("local session", toULID(sess.LocalUUID).String())
		// log("remote session", toULID(sess.RemoteUUID).String())
		msg, err := sess.OfferSealed(sess.PeerKey.X25519PublicKey().Bytes())
		if err != nil {
			return err
		}

		err = sm.Put(sess)
		if err != nil {
			return err
		}

		fmt.Println(msg)
		if opts.Post {
			addr, err := saltyim.LookupAddr(opts.Them)
			if err != nil {
				return err
			}
			_, err = http.DefaultClient.Post(addr.Endpoint().String(), "text/plain", strings.NewReader(msg))
			if err != nil {
				return err
			}
		}

		return nil

	case opts.Send:
		me, key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		sm, close, err := NewSessionManager(opts.State, me, key)
		if err != nil {
			return err
		}
		defer close()

		input, err := readInput(opts)
		if err != nil {
			return fmt.Errorf("reading input: %w", err)
		}

		sess, err := sm.Get(sm.ByName(opts.Them))
		if err != nil {
			return fmt.Errorf("read session: %w", err)
		}
		// log("me:", me, "send:", opts.Them)
		// log("local session", toULID(sess.LocalUUID).String())
		// log("remote session", toULID(sess.RemoteUUID).String())

		msg, err := sess.Send([]byte(input))
		if err != nil {
			return fmt.Errorf("send: %w", err)
		}
		err = sm.Put(sess)
		if err != nil {
			return err
		}

		fmt.Println(msg)
		if opts.Post {
			addr, err := saltyim.LookupAddr(opts.Them)
			if err != nil {
				return err
			}

			_, err = http.DefaultClient.Post(addr.Endpoint().String(), "text/plain", strings.NewReader(msg))
			if err != nil {
				return err
			}
		}

		return nil

	case opts.Recv:
		me, key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		sm, close, err := NewSessionManager(opts.State, me, key)
		if err != nil {
			return err
		}
		defer close()

		input, err := readInput(opts)
		if err != nil {
			return fmt.Errorf("reading input: %w", err)
		}
		id, msg, err := readMsg(input)
		if err != nil {
			return fmt.Errorf("reading msg: %w", err)
		}
		log("msg session", id.String())

		if sealed, ok := msg.(interface {
			Unseal([]byte) (xochimilco.Msg, error)
		}); ok {
			joined := make([]byte, 64)
			copy(joined, key.X25519Key().Private())
			copy(joined[32:], key.X25519Key().Public())
			msg, err = sealed.Unseal(joined)
			if err != nil {
				return err
			}
		}

		var sess *Session
		if offer, ok := msg.(interface {
			Nick() string
		}); ok {
			sess, err = sm.New(offer.Nick())
			if err != nil {
				return fmt.Errorf("get session: %w", err)
			}
		} else {
			sess, err = sm.Get(id)
			if err != nil {
				return fmt.Errorf("get session: %w", err)
			}
		}
		// log("local session", toULID(sess.LocalUUID).String())
		// log("remote session", toULID(sess.RemoteUUID).String())

		isEstablished, isClosed, plaintext, err := sess.ReceiveMsg(msg)
		if err != nil {
			return fmt.Errorf("session receive: %w", err)
		}
		log("(updated) remote session", toULID(sess.RemoteUUID).String())

		err = sm.Put(sess)
		if err != nil {
			return err
		}

		switch {
		case isClosed:
			log("GOT: closing session...")
			return sm.Delete(sess)
		case isEstablished:
			log("GOT: session established with ", sess.Name, "...")
			if len(plaintext) > 0 {
				fmt.Println(string(plaintext))
				if opts.Post {
					addr, err := saltyim.LookupAddr(opts.Them)
					if err != nil {
						return err
					}

					_, err = http.DefaultClient.Post(addr.Endpoint().String(), "text/plain", bytes.NewReader(plaintext))
					if err != nil {
						return err
					}
				}
			}

		default:
			log("GOT: ", sess.Name, ">", string(plaintext))
		}

		return nil

	case opts.Close:
		me, key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		sm, close, err := NewSessionManager(opts.State, me, key)
		if err != nil {
			return err
		}
		defer close()

		sess, err := sm.Get(sm.ByName(opts.Them))
		if err != nil {
			return fmt.Errorf("read session: %w", err)
		}

		msg, err := sess.Close()
		if err != nil {
			return fmt.Errorf("session close: %w", err)
		}

		err = sm.Delete(sess)
		if err != nil {
			return err
		}

		fmt.Println(msg)
		if opts.Post {
			addr, err := saltyim.LookupAddr(opts.Them)
			if err != nil {
				return err
			}

			_, err = http.DefaultClient.Post(addr.Endpoint().String(), "text/plain", strings.NewReader(msg))
			if err != nil {
				return err
			}
		}
		return nil

	case opts.Chat:
		me, key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		sm, close, err := NewSessionManager(opts.State, me, key)
		if err != nil {
			return err
		}
		defer close()

		svc := &service{BaseCTX: func() context.Context { return ctx }}
		svc.Client, err = NewClient(sm, me, svc.Handle)
		if err != nil {
			return err
		}

		return svc.Run(ctx, me, opts.Them)

	case opts.UI:

		return nil

	default:
		log(usage)
	}

	return nil
}

func log(a ...any) {
	fmt.Fprintf(os.Stderr, "\033[90m%s\033[0m\n", fmt.Sprint(a...))
}
