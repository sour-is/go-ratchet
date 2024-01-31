// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
// SPDX-License-Identifier: BSD-3-Clause
package interactive

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/oklog/ulid/v2"
	"go.salty.im/ratchet/client"
	"go.salty.im/ratchet/session/mem"
)

type service struct {
	prompt string
	*client.Client
	*mem.MemSession
}

func New(c *client.Client) *service {
	return &service{Client: c, MemSession: mem.NewMemSession(c)}
}

func (svc *service) Run(ctx context.Context, me, them string) error {
	ctx2, cancel := context.WithCancel(ctx)
	go svc.Interactive(ctx, me, them, cancel)
	return svc.Client.Run(ctx2)
}

func (svc *service) Interactive(ctx context.Context, me, them string, quit func()) {
	client.On(svc.Client, func(ctx context.Context, args client.OnOfferSent) {
		fmt.Print(CLEAR_LINE, formatMsg(me, args), "\n", svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnOfferReceived) {
		fmt.Print("\n", CLEAR_LINE, formatMsg(me, args), "\n", svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnSessionStarted) {
		fmt.Print("\n", CLEAR_LINE, formatMsg(me, args), "\n", svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnSessionClosed) {
		if them == args.Them {
			svc.setPrompt(me, "")
		}
		fmt.Print("\n", CLEAR_LINE, formatMsg(me, args), "\n", svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnMessageReceived) {
		fmt.Print("\n", CLEAR_LINE, formatMsg(me, args), "\n", svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnMessageSent) {
		fmt.Print(CLEAR_LINE, formatMsg(me, args), "\n")
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnSaltySent) {
		fmt.Print(CLEAR_LINE, formatMsg(me, args), "\n")
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnSaltyTextReceived) {
		fmt.Print("\n", CLEAR_LINE, formatMsg(me, args), "\n", svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnSaltyEventReceived) {
		fmt.Print("\n", CLEAR_LINE, formatMsg(me, args), "\n", svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnReceived) {
		fmt.Print("\n", CLEAR_LINE, formatMsg(me, args), "\n", svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args error) {
		fmt.Print(CLEAR_LINE, formatMsg(me, args), "\n", svc.prompt)
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

		if strings.HasPrefix(input, "/log") {
			logname := ""

			if strings.HasPrefix(input, "/log ") {
				logname = strings.TrimPrefix(input, "/log ")
			}

			if logname == "" {
				if them != "" {
					logname = "user:" + them
				} else {
					logname = "system"
				}
			}

			log, err := svc.ReadLog(ctx, logname, -1, -20)
			if err != nil {
				fmt.Println(err)
			}
			fmt.Println("\nLOG:", logname)
			for _, msg := range log {
				fmt.Println(formatMsg(me, msg))
			}
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
		if strings.HasPrefix(input, "/quit") {
			quit()
			return
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
		return svc.Use(ctx, func(ctx context.Context, sm client.SessionManager) error {
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

	log, err := svc.ReadLog(ctx, "user:"+*them, -1, -20)
	if err != nil {
		return err
	}

	for _, msg := range log {
		fmt.Println(formatMsg(me, msg))
	}
	svc.setPrompt(me, *them)

	_, err = svc.Chat(ctx, *them)
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

	*them = ""
	svc.setPrompt(me, "")
	fmt.Printf("\033[1A\r\033[2K<%s> %s\n", me, input)
	return svc.Close(ctx, target)
}
func (svc *service) doDefault(ctx context.Context, me string, them *string, input string) error {
	// fmt.Printf("\033[1A\r\033[2K<\033[31m%s\033[0m> %s\n", me, input)
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

func getTime(u ulid.ULID) time.Time {
	return time.UnixMilli(int64(u.Time()))
}

func log(a ...any) {
	fmt.Fprintf(os.Stderr, "\033[90m%s\033[0m\n", fmt.Sprint(a...))
}

func formatMsg(me string, msg any) string {
	switch msg := msg.(type) {
	case client.OnOfferSent:
		return fmt.Sprintf("%s::: offer sent %s :::%s", COLOR_GREY, msg.Them, RESET_COLOR)
	case client.OnOfferReceived:
		return fmt.Sprintf("%s::: offer from %s :::%s", COLOR_GREY, msg.Them, RESET_COLOR)
	case client.OnSessionStarted:
		return fmt.Sprintf("%s::: session started with %s :::%s", COLOR_GREY, msg.Them, RESET_COLOR)
	case client.OnSessionClosed:
		return fmt.Sprintf("%s::: session closed with %s :::%s", COLOR_GREY, msg.Them, RESET_COLOR)
	case client.OnMessageReceived:
		return fmt.Sprintf("%s%s <%s%s%s> %s%s", COLOR_GREY, getTime(msg.ID).Format("15:04:05"), COLOR_RED, msg.Them, COLOR_GREY, RESET_COLOR, msg.Msg.LiteralText())
	case client.OnMessageSent:
		return fmt.Sprintf("%s%s <%s%s%s> %s%s", COLOR_GREY, getTime(msg.ID).Format("15:04:05"), COLOR_RED, me, COLOR_GREY, RESET_COLOR, msg.Msg.LiteralText())
	case client.OnSaltySent:
		return fmt.Sprintf("%s%s <%s%s%s> %s%s", COLOR_GREY, msg.Msg.Timestamp.DateTime().Format("15:04:05"), COLOR_BLUE, me, COLOR_GREY, RESET_COLOR, msg.Msg.LiteralText())
	case client.OnSaltyTextReceived:
		return fmt.Sprintf("%s%s <%s%s%s> %s%s", COLOR_GREY, msg.Msg.Timestamp.DateTime().Format("15:04:05"), COLOR_BLUE, msg.Msg.User, COLOR_GREY, RESET_COLOR, msg.Msg.LiteralText())
	case client.OnSaltyEventReceived:
		return fmt.Sprintf("%s::: salty: %s(%s)%s", COLOR_GREY, msg.Event.Command, strings.Join(msg.Event.Args, ", "), RESET_COLOR)
	case client.OnReceived:
		return fmt.Sprintf("%s::: unknown message: %s%s", COLOR_GREY, msg.Raw, RESET_COLOR)
	default:
		return fmt.Sprint(msg)
	}
}

const (
	CLEAR_LINE  = "\033[1A\033[2K\r"
	COLOR_GREY  = "\033[90m"
	COLOR_RED   = "\033[31m"
	COLOR_BLUE  = "\033[34m"
	RESET_COLOR = "\033[0m"
)
