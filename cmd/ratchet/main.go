package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/gob"
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
  ratchet [options] [offer|recv|send|close]

Options:
  --me <me>        My acct name
  --key <key>      From key [default: ` + xdg.Get(xdg.EnvConfigHome, "rachet/$USER.key") + `]
  --them <them>    Their acct name [default: $USER@$DOMAIN]
  --state <state>   Session state path [default: ` + xdg.Get(xdg.EnvDataHome, "rachet") + `]
  --msg <msg>      Msg to read in. [default: stdin]
`

type opts struct {
	Offer bool `docopt:"offer"`
	Send  bool `docopt:"send"`
	Recv  bool `docopt:"recv"`
	Close bool `docopt:"close"`

	Me    string `docopt:"--me"`
	Key   string `docopt:"--key"`
	Them  string `docopt:"--them"`
	State string `docopt:"--state"`
	Msg   string `docopt:"--msg"`
}

type Session struct {
	Name    string
	PeerKey ed25519.PublicKey

	*xochimilco.Session
}

func NewSession(me ed25519.PrivateKey, name string, them ed25519.PublicKey) *Session {
	sess := &Session{
		Session: &xochimilco.Session{
			IdentityKey: me,
		},
	}
	sess.SetPeer(name, them)
	return sess
}
func (s *Session) SetPeer(name string, p ed25519.PublicKey) {
	s.Name = name
	s.PeerKey = p
	s.Session.VerifyPeer = func(peer ed25519.PublicKey) (valid bool) {
		return bytes.Equal(peer, p)
	}
}
func (s *Session) MarshalBinary() ([]byte, error) {
	sess, err := s.Session.MarshalBinary()
	if err != nil {
		return nil, err
	}

	o := struct {
		Name    string
		Key     ed25519.PublicKey
		Session []byte
	}{Name: s.Name, Key: s.PeerKey, Session: sess}

	var buf bytes.Buffer
	err = gob.NewEncoder(&buf).Encode(o)
	return buf.Bytes(), err
}
func (s *Session) UnmarshalBinary(b []byte) error {
	var o struct {
		Name    string
		Key     ed25519.PublicKey
		Session []byte
	}

	err := gob.NewDecoder(bytes.NewReader(b)).Decode(&o)
	if err != nil {
		return err
	}

	s.Session = &xochimilco.Session{}
	s.Session.UnmarshalBinary(o.Session)
	s.SetPeer(o.Name, o.Key)

	return err
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

	if err := run(opts); err != nil {
		log(err)
		os.Exit(1)
	}
}

func run(opts opts) error {
	// logf("%#v\n", opts)

	os.Setenv("DOMAIN", "sour.is")

	key, err := readSaltyIdentity(opts.Key)
	if err != nil {
		return fmt.Errorf("reading keyfile: %w", err)
	}

	sm := NewSessionManager(opts.State, opts.Me, key)

	switch {
	// case opts.Gen:
	// todo?

	case opts.Offer:
		sess, err := sm.Get(opts.Them)
		if err != nil {
			return fmt.Errorf("read session: %w", err)
		}

		offerMsg, err := sess.Offer()
		if err != nil {
			return err
		}

		fmt.Println(opts.Me, offerMsg)
		return sm.Put(sess)

	case opts.Send:
		input, them, err := readInput(opts.Msg, opts.Them)
		if err != nil {
			return fmt.Errorf("reading input: %w", err)
		}

		sess, err := sm.Get(them)
		if err != nil {
			return fmt.Errorf("read session: %w", err)
		}

		msg, err := sess.Send([]byte(input))
		if err != nil {
			return fmt.Errorf("send: %w", err)
		}

		fmt.Println(opts.Me, msg)
		return sm.Put(sess)

	case opts.Recv:
		input, them, err := readInput(opts.Msg, opts.Them)
		if err != nil {
			return fmt.Errorf("reading input from %s: %w", them, err)
		}

		sess, err := sm.Get(them)
		if err != nil {
			return fmt.Errorf("get session: %w", err)
		}

		isEstablished, isClosed, msg, err := sess.Receive(input)
		if err != nil {
			return fmt.Errorf("session receive: %w", err)
		}

		switch {
		case isClosed:
			log("GOT: closing session...")
			return sm.Delete(sess)
		case isEstablished:
			log("GOT: session established with ", them, "...")
			if len(msg) > 0 {
				fmt.Println(opts.Me, string(msg))
			}

		default:
			log("GOT: ", them, ">", string(msg))
		}

		return sm.Put(sess)

	case opts.Close:
		sess, err := sm.Get(opts.Them)
		if err != nil {
			return fmt.Errorf("read session: %w", err)
		}

		msg, err := sess.Close()
		if err != nil {
			return fmt.Errorf("session close: %w", err)
		}

		fmt.Println(opts.Me, msg)
		return sm.Delete(sess)
	}

	return nil
}

func enc(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
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

func fetchKey(to string) (ed25519.PublicKey, error) {
	log("fetch key: ", to)
	addr, err := saltyim.LookupAddr(to)
	if err != nil {
		return nil, err
	}

	return addr.Key().Bytes(), nil
}

func sessionhash(from, to string) string {
	h := fnv.New128a()
	fmt.Fprint(h, from, to)
	return enc(h.Sum(nil))
}

func log(a ...any) {
	fmt.Fprintln(os.Stderr, a...)
}

func readInput(input string, them string) (msg string, name string, err error) {
	var r io.ReadCloser
	if input == "stdin" {
		r = os.Stdin
	} else {
		r, err = os.Open(input)
		if err != nil {
			return
		}
	}

	msg, err = bufio.NewReader(r).ReadString('\n')
	if err != nil {
		err = fmt.Errorf("reading offer from stdin: %w", err)
		return
	}
	// log("msg: ", msg)

	name = them
	if n, m, ok := strings.Cut(msg, " "); ok {
		name = n
		msg = m
	}
	msg = strings.TrimSpace(msg)

	return
}

type DiskSessionManager struct {
	me   string
	key  ed25519.PrivateKey
	path string
}

func NewSessionManager(path, me string, key ed25519.PrivateKey) *DiskSessionManager {
	return &DiskSessionManager{me, key, path}
}
func (sm *DiskSessionManager) Get(them string) (*Session, error) {
	sh := sessionhash(sm.me, them)
	filename := filepath.Join(sm.path, sh)

	fd, err := os.Stat(filename)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("stat: %w", err)
	}

	if os.IsNotExist(err) {
		key, err := fetchKey(them)
		if err != nil {
			return nil, fmt.Errorf("fetching key for %s: %w", them, err)
		}
		return NewSession(sm.key, them, key), nil
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

	sess := &Session{}
	err = sess.UnmarshalBinary(b)
	return sess, err
}
func (sm *DiskSessionManager) Put(sess *Session) error {
	sh := sessionhash(sm.me, sess.Name)
	filename := filepath.Join(sm.path, sh)

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
func (sm *DiskSessionManager) Delete(sess *Session) error {
	sh := sessionhash(sm.me, sess.Name)
	filename := filepath.Join(sm.path, sh)
	return os.Remove(filename)
}
