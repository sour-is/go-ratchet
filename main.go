// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"go.sour.is/ev/driver/streamer"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/docopt/docopt-go"
	"go.sour.is/ev"
	diskstore "go.sour.is/ev/driver/disk-store"
	memstore "go.sour.is/ev/driver/mem-store"
	"go.sour.is/ev/event"
	"go.sour.is/pkg/xdg"
	"go.uber.org/multierr"
	"golang.org/x/sync/errgroup"

	"go.salty.im/ratchet/cli"
	"go.salty.im/ratchet/client"
	driver_msgbus "go.salty.im/ratchet/client/driver-msgbus"
	"go.salty.im/ratchet/interactive"
	"go.salty.im/ratchet/session"
	"go.salty.im/ratchet/ui"
)

var usage = `Ratchet Chat.
Usage:
  ratchet [options] recv
  ratchet [options] (offer|send|close) <them>
  ratchet [options] chat [<them>]
  ratchet [options] ui

Args:
  <them>             Receiver acct name to use in offer.

Options:
  --key <key>        Sender private key [default: ` + xdg.Get(xdg.EnvConfigHome, "racthet/$USER.key") + `]
  --state <state>    Session state path [default: ` + xdg.Get(xdg.EnvStateHome, "racthet") + `]
  --log <logs>       Log storage path   [default: ` + xdg.Get(xdg.EnvDataHome, "ratchet") + `]
  --msg <msg>        Msg to read in.    [default to read Standard Input]
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
	Log      string `docopt:"--log"`
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
		return cli.Offer(ctx, opts.Key, opts.State, opts.Them)

	case opts.Send:
		input, err := readInput(opts)
		if err != nil {
			return err
		}

		return cli.Send(ctx, opts.Key, opts.State, opts.Them, input)

	case opts.Recv:
		input, err := readInput(opts)
		if err != nil {
			return err
		}
		return cli.Recv(ctx, opts.Key, opts.State, opts.Them, input)

	case opts.Close:
		return cli.Close(ctx, opts.Key, opts.State, opts.Them)

	case opts.Chat:
		me, key, err := cli.ReadSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		sm, close, err := session.NewSessionManager(opts.State, me, key)
		if err != nil {
			return err
		}
		defer close()

		c, err := client.New(sm, me, driver_msgbus.WithMsgbus(sm.Position()))
		if err != nil {
			return err
		}
		c.BaseCTX = func() context.Context { return ctx }

		return interactive.New(c).Run(ctx, me, opts.Them)

	case opts.UI:
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		me, key, err := cli.ReadSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		sm, close, err := session.NewSessionManager(opts.State, me, key)
		if err != nil {
			return err
		}
		defer close()

		c, err := client.New(sm, me, driver_msgbus.WithMsgbus(sm.Position()))
		if err != nil {
			return err
		}
		c.BaseCTX = func() context.Context { return ctx }

		wg, _ := errgroup.WithContext(ctx)

		wg.Go(func() error { return c.Run(ctx) })

		m := ui.InitialModel(c, opts.Them)
		p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion())

		wg.Go(func() error {
			defer cancel()
			_, err = p.Run()
			return err
		})

		return wg.Wait()

	default:
		log(usage)
	}

	return nil
}

func log(a ...any) {
	fmt.Fprintf(os.Stderr, "\033[90m%s\033[0m\n", fmt.Sprint(a...))
}

func readInput(opts opts) (msg string, err error) {
	var r io.ReadCloser

	if opts.MsgStdin {
		r = os.Stdin
	} else if opts.MsgFile != "" {
		r, err = os.Open(opts.MsgFile)
		if err != nil {
			return
		}
	} else {
		return strings.TrimSpace(opts.Msg), nil
	}

	msg, err = bufio.NewReader(r).ReadString('\n')
	if err != nil {
		err = fmt.Errorf("read input: %w", err)
		return
	}

	return strings.TrimSpace(msg), nil
}

func setupChatlog(ctx context.Context, path string) (*chatLog, error) {
	// setup eventstore
	err := multierr.Combine(
		ev.Init(ctx),
		event.Init(ctx),
		diskstore.Init(ctx),
		memstore.Init(ctx),
	)
	if err != nil {
		return nil, err
	}

	es, err := ev.Open(
		ctx,
		path,
		streamer.New(ctx),
	)

	return &chatLog{es}, err
}

type chatLog struct {
	ev *ev.EventStore
}
