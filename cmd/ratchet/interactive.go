package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"git.mills.io/prologic/msgbus"
	"github.com/sour-is/xochimilco"
)

type service struct {
	BaseCTX func() context.Context
	prompt  string
	*Client
}

func (svc *service) Run(ctx context.Context, me, them string) error {
	go svc.Interactive(ctx, me, them)
	return svc.Client.Run(ctx)
}
func (svc *service) Context() (context.Context, context.CancelFunc) {
	ctx := context.Background()
	if svc.BaseCTX != nil {
		ctx = svc.BaseCTX()
	}
	return context.WithCancel(ctx)
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

		// Update session manager position in stream if supported.
		if pos, ok := sm.(interface{ SetPosition(int64) }); ok {
			pos.SetPosition(in.ID + 1)
		}

		if sealed, ok := msg.(interface {
			Unseal([]byte) (xochimilco.Msg, error)
		}); ok {
			joined := make([]byte, 64)
			copy(joined, sm.Identity().X25519Key().Private())
			copy(joined[32:], sm.Identity().X25519Key().Public())
			msg, err = sealed.Unseal(joined)
			if err != nil {
				return err
			}
		}

		// offer messages have a nick embeded in the payload.
		if offer, ok := msg.(interface {
			Nick() string
		}); ok {
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
			fmt.Printf(svc.prompt)
		}

		return nil
	})
}

func (svc *service) Interactive(ctx context.Context, me, them string) {
	err := syscall.SetNonblock(0, true)
	if err != nil {
		log(err)
	}

	scanner := bufio.NewScanner(NewCtxReader(ctx, os.Stdin))

	svc.setPrompt(me, them)
	prompt := func() bool {
		fmt.Print(svc.prompt)
		return scanner.Scan()
	}

	var initial string
	if them != "" {
		initial = "/chat " + them
		them = ""
	}

	for initial != "" || prompt() {
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
		if initial != "" {
			log(initial)
			input = initial
			initial = ""
		}

		input = strings.TrimSpace(input)
		if input == "" {
			continue
		}

		if strings.HasPrefix(input, "/chat") {
			err = svc.doChat(ctx, me, &them, input)
			if err != nil {
				log("ERR: ", err)
			}
			continue
		}
		if strings.HasPrefix(input, "/close") {
			err = svc.doClose(ctx, me, &them, input)
			if err != nil {
				log("ERR: ", err)
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

		// handle show list of open sessions
		if len(sp) <= 1 {
			log("usage: /chat|close username")
			for _, p := range sm.Sessions() {
				log("sess: ", p.Name)
			}
			return nil
		}

		if me == sp[1] {
			return fmt.Errorf("cant racthet with self")
		}

		*them = sp[1]
		svc.setPrompt(me, *them)

		session, err := sm.Get(sm.ByName(sp[1]))

		// handle initiating a new chat
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
			err = svc.sendMsg(session, msg)
			if err != nil {
				return err
			}
			err = sm.Put(session)
			if err != nil {
				return err
			}

			return nil
		}
		if err != nil {
			return err
		}

		// handle a pending ack from offer.
		if len(session.PendingAck) > 0 {
			err = svc.sendMsg(session, session.PendingAck)
			if err != nil {
				return err
			}
			session.PendingAck = ""
			return sm.Put(session)
		}

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

		if session.Name == *them {
			*them = ""
			svc.setPrompt(me, *them)
		}

		err = sm.Delete(session)
		if err != nil {
			return err
		}

		return nil
	})
}
func (svc *service) doDefault(ctx context.Context, me string, them *string, input string) error {
	return svc.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
		var err error
		var session *Session
		session, err = sm.Get(sm.ByName(*them))
		if err != nil {
			if session == nil {
				*them = ""

				return nil
			}
			return err
		}

		fmt.Printf("\033[1A\r\033[2K<%s> %s\n", me, input)
		msg, err := session.Send([]byte(input))
		if err != nil {
			return err
		}

		err = svc.sendMsg(session, msg)
		if err != nil {
			return err
		}

		return sm.Put(session)
	})
}
func (svc *service) sendMsg(session *Session, msg string) error {
	_, err := http.DefaultClient.Post(session.Endpoint, "text/plain", strings.NewReader(msg))
	if err != nil {
		return err
	}
	return nil
}
func (svc *service) setPrompt(me, them string) {
	if them == "" {
		svc.prompt = fmt.Sprintf("[%s]> ", me)
	} else {
		svc.prompt = fmt.Sprintf("[%s -> %s]> ", me, them)
	}
}

type ctxReader struct {
	ctx context.Context
	up  io.Reader
}

func NewCtxReader(ctx context.Context, up io.Reader) io.Reader {
	return &ctxReader{ctx, up}
}
func (r *ctxReader) Read(b []byte) (int, error) {
	tick := time.NewTicker(100 * time.Millisecond)
	for {
		select {
		case <-r.ctx.Done():
			return 0, io.EOF
		case <-tick.C: // do a slow tick so its not in a hot loop.
			i, err := r.up.Read(b)
			if i > 0 {
				return i, err
			}
		}
	}
}
