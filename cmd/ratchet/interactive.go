package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
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
	sp := strings.Fields(input)
	// handle show list of open sessions
	if len(sp) <= 1 {
		return svc.sm.Use(ctx, func(ctx context.Context, sm SessionManager) error {
			log("usage: /chat|close username")
			for _, p := range sm.Sessions() {
				log("sess: ", p.Name)
			}
			return nil
		})
	}

	if me == sp[1] {
		return fmt.Errorf("cant racthet with self")
	}

	*them = sp[1]
	svc.setPrompt(me, *them)

	established, err := svc.Client.Chat(ctx, *them)
	if err == nil {
		return err
	}
	if !established {
		fmt.Printf("\033[1A\r\033[2K**%s** offer chat...\n", me)
		return nil
	}
	return nil
}
func (svc *service) doClose(ctx context.Context, me string, them *string, input string) error {
	sp := strings.Fields(input)

	target := *them

	if len(sp) > 1 {
		target = sp[1]
	}

	if target == "" {
		return nil
	}

	svc.setPrompt(me, "")
	fmt.Printf("\033[1A\r\033[2K<%s> %s\n", me, input)
	return svc.Client.Close(ctx, target)
}
func (svc *service) doDefault(ctx context.Context, me string, them *string, input string) error {
	fmt.Printf("\033[1A\r\033[2K<%s> %s\n", me, input)
	return svc.Client.Send(ctx, *them, input)
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
