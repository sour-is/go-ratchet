package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"github.com/docopt/docopt-go"
	"go.mills.io/saltyim"

	"github.com/sour-is/xochimilco"
	"github.com/sour-is/xochimilco/cmd/ratchet/xdg"
)

var usage = `Rachet Chat.
usage:
  ratchet [options] [gen|jwt|offer|ack|send|recv|close]

Options:
  --me <to>        My acct name
  --key <key>      From key [default: ` + xdg.Get(xdg.EnvConfigHome, "rachet/$USER.key") + `]
  --them <user>    Their acct name [default: $USER@$DOMAIN]
  --data <state>   Session state path [default: ` + xdg.Get(xdg.EnvDataHome, "rachet") + `]
  --msg <msg>         Msg to read in. [default: stdin]
  --force, -f      Force recreate key for gen
`

type opts struct {
	Gen   bool `docopt:"gen"`
	JWT   bool `docopt:"jwt"`
	Offer bool `docopt:"offer"`
	Ack   bool `docopt:"ack"`
	Send  bool `docopt:"send"`
	Recv  bool `docopt:"recv"`
	Close bool `docopt:"close"`

	Me    string `docopt:"--me"`
	Key   string `docopt:"--key"`
	Them  string `docopt:"--them"`
	Data  string `docopt:"--data"`
	Msg   string `docopt:"--msg"`
	Force bool   `docopt:"--force"`
}

func main() {
	o, err := docopt.ParseDoc(usage)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	var opts opts
	o.Bind(&opts)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	go func() {
		<-ctx.Done()
		defer cancel() // restore interrupt function
	}()

	if err := run(opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(opts opts) error {
	// fmt.Printf("%#v\n", opts)

	os.Setenv("DOMAIN", "sour.is")

	acct := os.ExpandEnv(opts.Them)
	_ = acct

	switch {
	case opts.Gen:

	case opts.Offer:
		key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return err
		}

		fmt.Fprintf(os.Stderr, "from key %x %x\n", key.Public(), key)
		theirKey, err := fetchKey(opts.Them)
		if err != nil {
			return fmt.Errorf("fetching key: %w", err)
		}
		fmt.Fprintf(os.Stderr, "to key %x\n", theirKey)

		sess := &xochimilco.Session{
			IdentityKey: key,
		}

		offerMsg, err := sess.Offer()
		if err != nil {
			return err
		}

		fmt.Println(offerMsg)
		return writeSession(filepath.Join(opts.Data, dataFile(opts.Me, opts.Them)), sess)

	case opts.Ack:
		key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		themKey, err := fetchKey(opts.Them)
		if err != nil {
			return fmt.Errorf("fetching key: %w", err)
		}

		sess := &xochimilco.Session{
			IdentityKey: key,
			VerifyPeer: func(peer ed25519.PublicKey) (valid bool) {
				return bytes.Equal(peer, themKey)
			},
		}

		var r io.ReadCloser
		if opts.Msg == "stdin" {
			r = os.Stdin
		} else {
			r, err = os.Open(opts.Msg)
			if err != nil {
				return err
			}
		}

		offerMsg, err := bufio.NewReader(r).ReadString('\n')
		if err != nil {
			return fmt.Errorf("reading offer from stdin: %w", err)
		}
		offerMsg = strings.TrimSpace(offerMsg)
		fmt.Fprintln(os.Stderr, "msg: ", offerMsg)

		ackMsg, err := sess.Acknowledge(string(offerMsg))
		if err != nil {
			return fmt.Errorf("creating ack: %w", err)
		}

		fmt.Println(ackMsg)
		return writeSession(filepath.Join(opts.Data, dataFile(opts.Me, opts.Them)), sess)

	case opts.Send:
		key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		toKey, err := fetchKey(opts.Them)
		if err != nil {
			return fmt.Errorf("fetch key: %w", err)
		}

		sess, err := readSession(filepath.Join(opts.Data, dataFile(opts.Me, opts.Them)))
		if err != nil {
			return fmt.Errorf("read session: %w", err)
		}

		sess.IdentityKey = key
		sess.VerifyPeer = func(peer ed25519.PublicKey) (valid bool) {
			return peer.Equal(toKey)
		}

		var r io.ReadCloser
		if opts.Msg == "stdin" {
			r = os.Stdin
		} else {
			r, err = os.Open(opts.Msg)
			if err != nil {
				return err
			}
		}

		msg, err := bufio.NewReader(r).ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("read input: %w", err)
		}

		dataMsg, err := sess.Send(msg)
		if err != nil {
			return fmt.Errorf("send: %w", err)
		}

		fmt.Println(dataMsg)
		return writeSession(filepath.Join(opts.Data, dataFile(opts.Me, opts.Them)), sess)

	case opts.Recv:

		toKey, err := fetchKey(opts.Them)
		if err != nil {
			return fmt.Errorf("fetch key: %w", err)
		}

		sess, err := readSession(filepath.Join(opts.Data, dataFile(opts.Me, opts.Them)))
		if err != nil {
			return fmt.Errorf("read session: %w", err)
		}

		sess.VerifyPeer = func(peer ed25519.PublicKey) (valid bool) {
			return bytes.Equal(peer, toKey)
		}

		var r io.ReadCloser
		if opts.Msg == "stdin" {
			r = os.Stdin
		} else {
			r, err = os.Open(opts.Msg)
			if err != nil {
				return err
			}
		}

		msg, err := bufio.NewReader(r).ReadString('\n')
		if err != nil {
			return fmt.Errorf("read string: %w", err)
		}
		msg = strings.TrimSpace(msg)
		fmt.Fprintln(os.Stderr, "read:", msg)

		isEstablished, isClosed, dataMsg, err := sess.Receive(msg)
		if err != nil {
			return fmt.Errorf("session receive: %w", err)
		}

		if isClosed {
			fmt.Fprintln(os.Stdout, "closing session...")
			return os.Remove(filepath.Join(opts.Data, dataFile(opts.Me, opts.Them)))
		}

		if isEstablished {
			fmt.Fprintln(os.Stdout, "session established...")
		}

		fmt.Println("GOT: ", string(dataMsg))

		err = writeSession(filepath.Join(opts.Data, dataFile(opts.Me, opts.Them)), sess)
		if err != nil {
			return fmt.Errorf("write session: %w", err)
		}

		return nil

	case opts.Close:
		sess, err := readSession(filepath.Join(opts.Data, dataFile(opts.Me, opts.Them)))
		if err != nil {
			return fmt.Errorf("read session: %w", err)
		}

		closeMsg, err := sess.Close()
		if err != nil {
			return fmt.Errorf("session close: %w", err)
		}
		closeMsg = strings.TrimSpace(closeMsg)

		fmt.Println(closeMsg)
		fmt.Fprintln(os.Stdout, "closing session...")
		return os.Remove(filepath.Join(opts.Data, dataFile(opts.Me, opts.Them)))
	}

	return nil
}

func enc(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
func dec(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	return base64.RawURLEncoding.DecodeString(s)
}

func mkKeyfile(keyfile string, force bool) error {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Dir(keyfile), 0700)
	if err != nil {
		return err
	}

	_, err = os.Stat(keyfile)
	if !os.IsNotExist(err) {
		if force {
			fmt.Println("removing keyfile", keyfile)
			err = os.Remove(keyfile)
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("the keyfile %s exists. use --force", keyfile)
		}
	}

	fp, err := os.OpenFile(keyfile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	fmt.Fprint(fp, "# pub: ", enc(pub), "\n", enc(priv))

	return fp.Close()
}

func readKeyfile(keyfile string) (ed25519.PrivateKey, error) {
	fd, err := os.Stat(keyfile)
	if err != nil {
		return nil, err
	}

	if fd.Mode()&0066 != 0 {
		return nil, fmt.Errorf("permissions are too weak")
	}

	f, err := os.Open(keyfile)
	scan := bufio.NewScanner(f)

	var key ed25519.PrivateKey
	for scan.Scan() {
		txt := scan.Text()
		if strings.HasPrefix(txt, "#") {
			continue
		}
		if strings.TrimSpace(txt) == "" {
			continue
		}

		txt = strings.TrimPrefix(txt, "# priv: ")
		b, err := dec(txt)
		if err != nil {
			return nil, err
		}
		key = b
	}

	return key, err
}
func readSaltyIdentity(keyfile string) (ed25519.PrivateKey, error) {
	fd, err := os.Stat(keyfile)
	if err != nil {
		return nil, err
	}

	if fd.Mode()&0066 != 0 {
		return nil, fmt.Errorf("permissions are too weak")
	}

	f, err := os.Open(keyfile)
	if err != nil {
		return nil, err
	}

	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	addr, err := saltyim.GetIdentity(saltyim.WithIdentityBytes(b))
	if err != nil {
		return nil, err
	}

	return addr.Key().Private(), nil
}

func fetchKey(to string) (ed25519.PrivateKey, error) {
	fmt.Fprintln(os.Stderr, "fetch key: ", to)
	addr, err := saltyim.LookupAddr(to)
	if err != nil {
		return nil, err
	}

	return addr.Key().Bytes(), nil
}

func dataFile(from, to string) string {
	h := fnv.New128a()
	fmt.Fprint(h, from, to)
	return enc(h.Sum(nil))
}

func writeSession(filename string, sess *xochimilco.Session) error {
	fmt.Fprintln(os.Stderr, "write session: ", filename)

	err := os.MkdirAll(filepath.Dir(filename), 0700)
	if err != nil {
		return err
	}

	fp, err := os.OpenFile(filename, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}

	b, err := sess.MarshalBinary()
	if err != nil {
		return err
	}

	_, err = fp.Write(b)
	if err != nil {
		return err
	}
	return fp.Close()
}

func readSession(filename string) (*xochimilco.Session, error) {
	fmt.Fprintln(os.Stderr, "read session: ", filename)

	fd, err := os.Stat(filename)
	if err != nil {
		return nil, fmt.Errorf("stat: %w", err)
	}

	if fd.Mode()&0066 != 0 {
		return nil, fmt.Errorf("permissions are too weak")
	}

	f, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("open %w", err)
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("read %d bytes: %w", len(b), err)
	}

	sess := &xochimilco.Session{}
	err = sess.UnmarshalBinary(b)
	return sess, err
}
