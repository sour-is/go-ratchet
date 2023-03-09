// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
// SPDX-License-Identifier: BSD-3-Clause

package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/keys-pub/keys"
	"go.salty.im/saltyim"

	"go.salty.im/ratchet/client"
	"go.salty.im/ratchet/session"
)

func Offer(ctx context.Context, keyfile string, state string, them string) error {
	me, key, err := ReadSaltyIdentity(keyfile)
	if err != nil {
		return fmt.Errorf("reading keyfile: %w", err)
	}

	sm, close, err := session.NewSessionManager(state, me, key)
	if err != nil {
		return err
	}
	defer close()

	c, err := client.New(sm, me)
	if err != nil {
		return err
	}
	client.On(c, func(ctx context.Context, m client.OnOfferSent) { fmt.Println(m.Raw) })

	_, err = c.Chat(ctx, them)
	return err
}

func Send(ctx context.Context, keyfile, state, them, input string) error {
	me, key, err := ReadSaltyIdentity(keyfile)
	if err != nil {
		return fmt.Errorf("reading keyfile: %w", err)
	}

	sm, close, err := session.NewSessionManager(state, me, key)
	if err != nil {
		return err
	}
	defer close()

	c, err := client.New(sm, me)
	if err != nil {
		return err
	}

	client.On(c, func(ctx context.Context, m client.OnMessageSent) { fmt.Println(m.Sealed) })

	err = c.Send(ctx, them, input)

	return err
}

func Recv(ctx context.Context, keyfile, state, them, input string) error {
	me, key, err := ReadSaltyIdentity(keyfile)
	if err != nil {
		return fmt.Errorf("reading keyfile: %w", err)
	}

	sm, close, err := session.NewSessionManager(state, me, key)
	if err != nil {
		return err
	}
	defer close()

	c, err := client.New(sm, me)
	if err != nil {
		return err
	}

	client.On(c, func(ctx context.Context, m client.OnMessageReceived) { fmt.Println(m.Msg.Literal()) })
	client.On(c, func(ctx context.Context, m client.OnOfferReceived) { fmt.Println(m.PendingAck) })
	client.On(c, func(ctx context.Context, m client.OnSessionStarted) { fmt.Println("Session Started with ", m.Them) })
	client.On(c, func(ctx context.Context, m client.OnSessionClosed) { fmt.Println("Session Closed with ", m.Them) })

	err = c.Input(client.OnInput{Position: 1, Payload: input})

	return err
}

func Close(ctx context.Context, keyfile, state, them string) error {
	me, key, err := ReadSaltyIdentity(keyfile)
	if err != nil {
		return fmt.Errorf("reading keyfile: %w", err)
	}

	sm, close, err := session.NewSessionManager(state, me, key)
	if err != nil {
		return err
	}
	defer close()

	c, err := client.New(sm, me)
	if err != nil {
		return err
	}
	client.On(c, func(ctx context.Context, m client.OnMessageSent) { fmt.Println(m.Sealed) })

	err = c.Close(ctx, them)

	return err
}

func ReadSaltyIdentity(keyfile string) (string, *keys.EdX25519Key, error) {
	fd, err := os.Stat(keyfile)
	if err != nil {
		return "", nil, err
	}

	if fd.Mode()&0066 != 0 {
		return "", nil, fmt.Errorf("permissions are too weak")
	}

	f, err := os.Open(keyfile)
	if err != nil {
		return "", nil, err
	}

	b, err := io.ReadAll(f)
	if err != nil {
		return "", nil, err
	}

	addr, err := saltyim.GetIdentity(saltyim.WithIdentityBytes(b))
	if err != nil {
		return "", nil, err
	}

	return addr.Addr().String(), addr.Key(), nil
}
