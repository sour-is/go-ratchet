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
	"github.com/sour-is/xochimilco/cmd/ratchet/client"
)

type service struct {
	prompt string
	*client.Client
}

func New(c *client.Client) *service {
	return &service{Client: c}
}

func (svc *service) Run(ctx context.Context, me, them string) error {
	go svc.Interactive(ctx, me, them)
	return svc.Client.Run(ctx)
}

func (svc *service) Interactive(ctx context.Context, me, them string) {
	client.On(svc.Client, func(ctx context.Context, args client.OnOfferSent) {
		fmt.Printf("\n\033[1A\r\033[2K\033[90m::: offer sent %s :::\033[0m\n", args.Them)
		fmt.Printf(svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnOfferReceived) {
		fmt.Printf("\n\033[1A\r\033[2K\033[90m::: offer from %s :::\033[0m\n", args.Them)
		fmt.Printf(svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnSessionStarted) {
		fmt.Printf("\n\033[1A\r\033[2K\033[90m::: session started with %s :::\033[0m\n", args.Them)
		fmt.Printf(svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnSessionClosed) {
		fmt.Printf("\n\033[1A\r\033[2K\033[90m::: session closed with %s :::\033[0m\n", args.Them)
		if them == args.Them {
			svc.setPrompt(me, "")
		}
		fmt.Printf(svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnMessageReceived) {
		fmt.Printf("\n\033[1A\r\033[2K%s <\033[31m%s\033[0m> %s\n", getTime(args.ID).Format("15:04:05"), args.Them, args.Msg)
		fmt.Printf(svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnMessageSent) {
		fmt.Printf("\033[1A\r\033[2K%s <\033[31m%s\033[0m> %s\n", time.Now().Format("15:04:05"), me, args.Msg)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnSaltySent) {
		fmt.Printf("\033[1A\r\033[2K%s <\033[34m%s\033[0m> %s\n", time.Now().Format("15:04:05"), me, args.Msg)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnSaltyTextReceived) {
		fmt.Printf("\n\033[1A\r\033[2K%s <\033[34m%s\033[0m> %s\n", args.Msg.Timestamp.DateTime().Format("15:04:05"), args.Msg.User, args.Msg.LiteralText())
		fmt.Printf(svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnSaltyEventReceived) {
		fmt.Printf("\n\033[1A\r\033[2K\033[90m::: salty: %s(%s)\033[0m\n", args.Event.Command, strings.Join(args.Event.Args, ", "))
		fmt.Printf(svc.prompt)
	})
	client.On(svc.Client, func(ctx context.Context, args client.OnOtherReceived) {
		fmt.Printf("\n\033[1A\r\033[2K\033[90m::: unknown message: %s\033[0m\n", args.Raw)
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
