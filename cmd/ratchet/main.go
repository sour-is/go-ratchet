package main

import (
	"bufio"
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
  --to <to>        To acct name
  --key <key>      From key [default: ` + xdg.Get(xdg.EnvConfigHome, "rachet/$USER.key") + `]
  --from <user>    From acct name [default: $USER@$DOMAIN]
  --data <state>   Session state path [default: ` + xdg.Get(xdg.EnvDataHome, "rachet") + `]
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

	Key   string `docopt:"--key"`
	From  string `docopt:"--from"`
	To    string `docopt:"--to"`
	Data  string `docopt:"--data"`
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

	acct := os.ExpandEnv(opts.From)
	_ = acct

	switch {
	case opts.Gen:
		err := mkKeyfile(opts.Key, opts.Force)
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stderr, "wrote keyfile to", opts.Key)

	case opts.Offer:
		key, err := readKeyfile(opts.Key)
		if err != nil {
			return err
		}

		toKey, err := fetchKey(opts.To)
		if err != nil {
			return err
		}

		sess := &xochimilco.Session{
			IdentityKey: key,
			VerifyPeer: func(peer ed25519.PublicKey) (valid bool) {
				return peer.Equal(toKey)
			},
		}

		offerMsg, err := sess.Offer()
		if err != nil {
			return err
		}
		fmt.Println(offerMsg)

		return writeSession(filepath.Join(opts.Data, dataFile(opts.From, opts.To)), sess)

	case opts.Ack:
		key, err := readKeyfile(opts.Key)
		if err != nil {
			return err
		}

		toKey, err := fetchKey(opts.To)
		if err != nil {
			return err
		}

		sess := &xochimilco.Session{
			IdentityKey: key,
			VerifyPeer: func(peer ed25519.PublicKey) (valid bool) {
				return peer.Equal(toKey)
			},
		}

		offerMsg, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return err
		}
		ackMsg, err := sess.Acknowledge(string(offerMsg))
		if err != nil {
			return err
		}
		fmt.Println(ackMsg)
		return writeSession(filepath.Join(opts.Data, dataFile(opts.From, opts.To)), sess)

	case opts.Send:
		toKey, err := fetchKey(opts.To)
		if err != nil {
			return err
		}
		sess, err := readSession(filepath.Join(opts.Data, dataFile(opts.From, opts.To)))
		sess.VerifyPeer = func(peer ed25519.PublicKey) (valid bool) {
			return peer.Equal(toKey)
		}
		msg, err := bufio.NewReader(os.Stdin).ReadBytes('\n')
		if err != nil {
			return err
		}
		dataMsg, err := sess.Send(msg)
		if err != nil {
			return err
		}
		fmt.Println(dataMsg)
		return writeSession(filepath.Join(opts.Data, dataFile(opts.From, opts.To)), sess)

	case opts.Recv:
		toKey, err := fetchKey(opts.To)
		if err != nil {
			return err
		}
		sess, err := readSession(filepath.Join(opts.Data, dataFile(opts.From, opts.To)))
		if err != nil {
			return err
		}

		sess.VerifyPeer = func(peer ed25519.PublicKey) (valid bool) {
			return peer.Equal(toKey)
		}
		msg, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil {
			return err
		}
		isEstablished, isClosed, dataMsg, err := sess.Receive(msg)
		if err != nil {
			return err
		}
		fmt.Println(dataMsg)

		if isClosed {
			fmt.Fprintln(os.Stdout, "closing session...")
			return os.Remove(filepath.Join(opts.Data, dataFile(opts.From, opts.To)))
		}

		if isEstablished {
			fmt.Fprintln(os.Stdout, "session established...")
		}
		return writeSession(filepath.Join(opts.Data, dataFile(opts.From, opts.To)), sess)

	case opts.Close:
		sess, err := readSession(filepath.Join(opts.Data, dataFile(opts.From, opts.To)))
		if err != nil {
			return err
		}
		closeMsg, err := sess.Close()
		if err != nil {
			return err
		}
		fmt.Println(closeMsg)

		fmt.Fprintln(os.Stdout, "closing session...")
		return os.Remove(filepath.Join(opts.Data, dataFile(opts.From, opts.To)))
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

func ptr[T any](v T) *T {
	return &v
}

func fetchKey(to string) (ed25519.PrivateKey, error) {
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
	fp, err := os.Create(filename)
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
	fd, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}

	if fd.Mode()&0066 != 0 {
		return nil, fmt.Errorf("permissions are too weak")
	}

	f, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	sess := &xochimilco.Session{}
	err = sess.UnmarshalBinary(b)
	return sess, err
}
