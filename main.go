package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/docopt/docopt-go"
	"github.com/oklog/ulid/v2"

	"git.mills.io/saltyim/ratchet/client"
	"git.mills.io/saltyim/ratchet/interactive"
	"git.mills.io/saltyim/ratchet/session"
	"git.mills.io/saltyim/ratchet/xdg"
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
		return doOffer(ctx, opts)

	case opts.Send:
		return doSend(ctx, opts)

	case opts.Recv:
		return doRecv(ctx, opts)

	case opts.Close:
		return doClose(ctx, opts)

	case opts.Chat:
		me, key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		sm, close, err := session.NewSessionManager(opts.State, me, key)
		if err != nil {
			return err
		}
		defer close()

		c, err := client.NewClient(sm, me)
		if err != nil {
			return err
		}
		c.BaseCTX = func() context.Context { return ctx }

		return interactive.New(c).Run(ctx, me, opts.Them)

	case opts.UI:
		p := tea.NewProgram(initialModel())
		_, err := p.Run()
		return err

	default:
		log(usage)
	}

	return nil
}

func log(a ...any) {
	fmt.Fprintf(os.Stderr, "\033[90m%s\033[0m\n", fmt.Sprint(a...))
}

func toULID(b []byte) ulid.ULID {
	var id ulid.ULID
	copy(id[:], b)
	return id
}
