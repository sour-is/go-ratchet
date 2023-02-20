package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"go.yarn.social/lextwt"
)

type service struct {
	prompt string
	*Client
}

func (svc *service) Run(ctx context.Context, me, them string) error {
	go svc.Interactive(ctx, me, them)
	return svc.Client.Run(ctx)
}

func (svc *service) Interactive(ctx context.Context, me, them string) {
	svc.Handle(OnOfferSent, func(ctx context.Context, sessionID []byte, them, msg string) {
		fmt.Printf("\n\033[1A\r\033[2K\033[90m::: offer sent %s :::\033[0m\n", them)
		fmt.Printf(svc.prompt)
	})
	svc.Handle(OnOfferReceived, func(ctx context.Context, sessionID []byte, them, msg string) {
		fmt.Printf("\n\033[1A\r\033[2K\033[90m::: offer from %s :::\033[0m\n", them)
		fmt.Printf(svc.prompt)
	})
	svc.Handle(OnSessionStarted, func(ctx context.Context, sessionID []byte, them, msg string) {
		fmt.Printf("\n\033[1A\r\033[2K\033[90m::: session started with %s :::\033[0m\n", them)
		fmt.Printf(svc.prompt)
	})
	svc.Handle(OnSessionClosed, func(ctx context.Context, sessionID []byte, target, msg string) {
		fmt.Printf("\n\033[1A\r\033[2K\033[90m::: session closed with %s :::\033[0m\n", target)
		if them == target {
			svc.setPrompt(me, "")
		}
		fmt.Printf(svc.prompt)
	})
	svc.Handle(OnMessageReceived, func(ctx context.Context, sessionID []byte, them, msg string) {
		fmt.Printf("\n\033[1A\r\033[2K%s <\033[31m%s\033[0m> %s\n", getTime(sessionID).Format("15:04:05"), them, msg)
		fmt.Printf(svc.prompt)
	})
	svc.Handle(OnMessageSent, func(ctx context.Context, sessionID []byte, them, msg string) {
		// fmt.Printf("\n\033[1A\r\033[2K<\033[31m%s\033[0m> %s\n", me, msg)
		// fmt.Printf(svc.prompt)
	})
	svc.Handle(OnSaltySent, func(ctx context.Context, sessionID []byte, them, msg string) {
		fmt.Printf("\n\033[1A\r\033[2K%s <\033[34m%s\033[0m> %s\n", time.Now().Format("15:04:05"), them, msg)
	})
	svc.Handle(OnSaltyReceived, func(ctx context.Context, _ []byte, _, msg string) {
		s, err := lextwt.ParseSalty(msg)
		if err != nil {
			return
		}
		switch s := s.(type) {
		case *lextwt.SaltyEvent:
			fmt.Printf("\n\033[1A\r\033[2K\033[90m::: salty: %s(%s)\033[0m\n", s.Command, strings.Join(s.Args, ", "))
		case *lextwt.SaltyText:
			fmt.Printf("\n\033[1A\r\033[2K%s <\033[34m%s\033[0m> %s\n", s.Timestamp.DateTime().Format("15:04:05"), s.User, s.LiteralText())
		}

		fmt.Printf(svc.prompt)
	})
	svc.Handle(OnOtherReceived, func(ctx context.Context, sessionID []byte, them, msg string) {
		fmt.Printf("\n\033[1A\r\033[2K\033[90m::: unknown message: %s\033[0m\n", msg)
		fmt.Printf(svc.prompt)
	})

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
		if strings.HasPrefix(input, "/salty") {
			target, msg, _ := strings.Cut(strings.TrimPrefix(input, "/salty "), " ")
			err = svc.SendSalty(ctx, target, msg)
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

	_, err := svc.Chat(ctx, *them)
	if err == nil {
		return err
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
	return svc.Close(ctx, target)
}
func (svc *service) doDefault(ctx context.Context, me string, them *string, input string) error {
	fmt.Printf("\033[1A\r\033[2K<\033[31m%s\033[0m> %s\n", me, input)
	return svc.Send(ctx, *them, input)
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

func getTime(b []byte) time.Time {
	u := toULID(b)
	return time.UnixMilli(int64(u.Time()))
}
