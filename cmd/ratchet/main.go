package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"

	"git.mills.io/prologic/msgbus"
	"github.com/docopt/docopt-go"
	"go.mills.io/saltyim"

	"github.com/sour-is/xochimilco/cmd/ratchet/xdg"
)

var usage = `Rachet Chat.
Usage:
  ratchet [options] recv
  ratchet [options] (offer|send|close) <them>
  ratchet [options] chat
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
		msg, err := sess.Offer()
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

		var sess *Session
		if offer, ok := msg.(interface{ Nick() string }); ok {
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

		svc := &service{ctx: ctx}
		svc.Client, err = NewClient(sm, me, svc.Handle)
		if err != nil {
			return err
		}

		go svc.Interactive(ctx, me)

		return svc.Run(ctx)

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

type service struct {
	ctx context.Context
	*Client
}

func (svc *service) Context() (context.Context, context.CancelFunc) {
	return context.WithCancel(svc.ctx)
}

func (svc *service) Handle(in *msgbus.Message) error {
	ctx, cancel := svc.Context()
	defer cancel()

	input := string(in.Payload)
	if !(strings.HasPrefix(input, "!RAT!") && strings.HasSuffix(input, "!CHT!")) {
		return nil
	}

	id, msg, err := readMsg(input)
	if err != nil {
		return fmt.Errorf("reading msg: %w", err)
	}
	// log("msg session", id.String())

	return svc.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		var sess *Session

		if offer, ok := msg.(interface{ Nick() string }); ok {
			sess, err = sm.New(offer.Nick())
			if err != nil {
				return fmt.Errorf("get session: %w", err)
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
		// log("local session", toULID(sess.LocalUUID).String())
		// log("remote session", toULID(sess.RemoteUUID).String())

		isEstablished, isClosed, plaintext, err := sess.ReceiveMsg(msg)
		if err != nil {
			return fmt.Errorf("session receive: %w", err)
		}
		// log("(updated) remote session", toULID(sess.RemoteUUID).String())

		err = sm.Put(sess)
		if err != nil {
			return err
		}

		switch {
		case isClosed:
			log("GOT: closing session...")
			return sm.Delete(sess)
		case isEstablished:
			log("GOT: session established with ", sess.Name, "...", sess.Endpoint)
		default:
			fmt.Printf("\n\033[1A\r\033[2K<%s> %s\n", sess.Name, string(plaintext))
			fmt.Printf("%s -> %s >", svc.addr, sess.Name)
		}

		return nil
	})
}
func (svc *service) Interactive(ctx context.Context, me string) {
	var them string

	scanner := bufio.NewScanner(os.Stdin)

	prompt := func() bool {
		if them == "" {
			fmt.Printf("%s -> none  > ", me)
		} else {
			fmt.Printf("%s -> %s >", me, them)
		}
		return scanner.Scan()
	}

	for prompt() {
		err := ctx.Err()
		if err != nil {
			return
		}

		err = scanner.Err()
		if err != nil {
			log(err)
			break
		}

		input := scanner.Text()

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		if strings.HasPrefix(input, "/chat") {
			err = svc.doChat(ctx, me, &them, input)
			if err != nil {
				log(err)
			}
			continue
		}
		if strings.HasPrefix(input, "/close") {
			err = svc.doClose(ctx, me, &them, input)
			if err != nil {
				log(err)
			}
			continue
		}

		if them == "" {
			log("no session")
			log("usage: /chat username")
			continue
		}

		err = svc.doDefault(ctx, me, &them, input)
		if err != nil {
			log(err)
		}
	}
}

func (svc *service) doChat(ctx context.Context, me string, them *string, input string) error {
	return svc.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		sp := strings.Fields(input)
		if len(sp) <= 1 {
			log("usage: /chat|close username")
			for _, p := range sm.Sessions() {
				log("sess: ", p.Name, p.ID)
			}
			return nil
		}

		session, err := sm.Get(sm.ByName(sp[1]))
		if err != nil && errors.Is(err, os.ErrNotExist) {
			session, err = sm.New(sp[1])
			if err != nil {
				return err
			}
			msg, err := session.Offer()
			if err != nil {
				return err
			}

			fmt.Printf("\033[1A\r\033[2K**%s** offer chat...\n", me)
			_, err = http.DefaultClient.Post(session.Endpoint, "text/plain", strings.NewReader(msg))
			if err != nil {
				return err
			}

			err = sm.Put(session)
			if err != nil {
				return err
			}

			*them = sp[1]
			return nil
		}
		if err != nil {
			return err
		}
		*them = sp[1]

		if len(session.PendingAck) > 0 {
			// log("sending ack...", session.Endpoint)
			_, err = http.DefaultClient.Post(session.Endpoint, "text/plain", strings.NewReader(session.PendingAck))
			if err != nil {
				return err
			}
			session.PendingAck = ""
		}
		err = sm.Put(session)
		if err != nil {
			return err
		}

		// log(session)
		return nil
	})
}
func (svc *service) doClose(ctx context.Context, me string, them *string, input string) error {
	return svc.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		var err error
		var session *Session

		sp := strings.Fields(input)

		if len(sp) > 1 {
			session, err = sm.Get(sm.ByName(sp[1]))
			if err != nil {
				return err
			}
		} else if *them != "" {
			session, err = sm.Get(sm.ByName(*them))
			if err != nil {
				return err
			}
		}

		if session == nil {
			return nil
		}

		msg, err := session.Close()
		if err != nil {
			return err
		}

		fmt.Printf("\033[1A\r\033[2K<%s> %s\n", me, input)
		_, err = http.DefaultClient.Post(session.Endpoint, "text/plain", strings.NewReader(msg))
		if err != nil {
			return err
		}

		err = sm.Delete(session)
		if err != nil {
			return err
		}

		*them = ""
		return nil
	})
}
func (svc *service) doDefault(ctx context.Context, me string, them *string, input string) error {
	return svc.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		var session *Session
		session, _ = sm.Get(sm.ByName(*them))

		msg, err := session.Send([]byte(input))
		if err != nil {
			return err
		}

		fmt.Printf("\033[1A\r\033[2K<%s> %s\n", me, input)
		_, err = http.DefaultClient.Post(session.Endpoint, "text/plain", strings.NewReader(msg))
		if err != nil {
			return err
		}

		err = sm.Put(session)
		if err != nil {
			return err
		}

		return nil
	})
}
