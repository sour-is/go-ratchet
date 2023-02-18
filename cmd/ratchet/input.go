package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/keys-pub/keys"
	"github.com/oklog/ulid/v2"
	"github.com/sour-is/xochimilco"
	"go.mills.io/saltyim"
)

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

func fetchKey(to string) (saltyim.Addr, error) {
	log("fetch key: ", to)
	addr, err := saltyim.LookupAddr(to)
	if err != nil {
		return nil, err
	}
	log(addr.Endpoint())

	return addr, nil
}
