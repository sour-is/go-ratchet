// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
// SPDX-License-Identifier: BSD-3-Clause

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/keys-pub/keys"
	"github.com/oklog/ulid/v2"
	"go.salty.im/ratchet/session"
	"go.salty.im/ratchet/xochimilco"
	"go.salty.im/saltyim"
)

func doOffer(ctx context.Context, opts opts) error {
	me, key, err := readSaltyIdentity(opts.Key)
	if err != nil {
		return fmt.Errorf("reading keyfile: %w", err)
	}

	sm, close, err := session.NewSessionManager(opts.State, me, key)
	if err != nil {
		return err
	}
	defer close()

	sess, err := sm.New(opts.Them)
	if err != nil {
		return fmt.Errorf("read session: %w", err)
	}
	msg, err := sess.OfferSealed(sess.PeerKey.X25519PublicKey().Bytes32())
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
}

func doSend(ctx context.Context, opts opts) error {
	me, key, err := readSaltyIdentity(opts.Key)
	if err != nil {
		return fmt.Errorf("reading keyfile: %w", err)
	}

	sm, close, err := session.NewSessionManager(opts.State, me, key)
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
}

func doRecv(ctx context.Context, opts opts) error {
	me, key, err := readSaltyIdentity(opts.Key)
	if err != nil {
		return fmt.Errorf("reading keyfile: %w", err)
	}

	sm, close, err := session.NewSessionManager(opts.State, me, key)
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
		Unseal(priv, pub *[32]byte) (xochimilco.Msg, error)
	}); ok {
		msg, err = sealed.Unseal(
			key.X25519Key().Bytes32(),
			key.PublicKey().X25519PublicKey().Bytes32(),
		)
		if err != nil {
			return err
		}
	}

	var sess *session.Session
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
		if len(sess.PendingAck) > 0 {
			fmt.Println(sess.PendingAck)
			if opts.Post {
				addr, err := saltyim.LookupAddr(opts.Them)
				if err != nil {
					return err
				}

				_, err = http.DefaultClient.Post(addr.Endpoint().String(), "text/plain", strings.NewReader(sess.PendingAck))
				if err != nil {
					return err
				}
			}
		}

	default:
		log("GOT: ", sess.Name, ">", string(plaintext))
	}

	return nil
}

func doClose(ctx context.Context, opts opts) error {
	me, key, err := readSaltyIdentity(opts.Key)
	if err != nil {
		return fmt.Errorf("reading keyfile: %w", err)
	}

	sm, close, err := session.NewSessionManager(opts.State, me, key)
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

func readMsg(input string) (id ulid.ULID, msg xochimilco.Msg, err error) {
	// log(input)

	msg, err = xochimilco.Parse(input)
	if err != nil {
		return
	}

	copy(id[:], msg.ID())

	return
}

func readSaltyIdentity(keyfile string) (string, *keys.EdX25519Key, error) {
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
