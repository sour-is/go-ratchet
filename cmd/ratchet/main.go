package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/gob"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"

	"git.mills.io/prologic/msgbus"
	"git.mills.io/prologic/msgbus/client"
	"github.com/docopt/docopt-go"
	"github.com/oklog/ulid/v2"
	"go.mills.io/saltyim"

	"github.com/sour-is/xochimilco"
	"github.com/sour-is/xochimilco/cmd/ratchet/xdg"
)

var usage = `Rachet Chat.
Usage:
  ratchet [options] recv
  ratchet [options] (offer|send|close) <them>

Args:
  <them>           Receiver acct name to use in offer. 

Options:
  --key <key>      Sender private key [default: ` + xdg.Get(xdg.EnvConfigHome, "rachet/$USER.key") + `]
  --state <state>  Session state path [default: ` + xdg.Get(xdg.EnvDataHome, "rachet") + `]
  --msg <msg>      Msg to read in. [default: stdin]
  --post           Send to msgbus
`

type opts struct {
	Offer bool `docopt:"offer"`
	Send  bool `docopt:"send"`
	Recv  bool `docopt:"recv"`
	Close bool `docopt:"close"`
	Chat  bool `docopt:"chat"`
	Post  bool `docopt:"--post"`

	Them string `docopt:"<them>"`

	Key     string `docopt:"--key"`
	Session string `docopt:"--session"`
	State   string `docopt:"--state"`
	Msg     string `docopt:"--msg"`
}

type Session struct {
	Name    string
	PeerKey ed25519.PublicKey

	*xochimilco.Session
}

func NewSession(id ulid.ULID, me string, key ed25519.PrivateKey, name string, them ed25519.PublicKey) *Session {
	sess := &Session{
		Session: &xochimilco.Session{
			IdentityKey: key,
			Me:          me,
			LocalUUID:   id[:],
		},
	}
	sess.SetPeerKey(name, them)
	return sess
}
func (s *Session) SetPeerKey(name string, p ed25519.PublicKey) {
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
		Name     string
		Endpoint []byte
		Key      ed25519.PublicKey
		Session  []byte
	}

	err := gob.NewDecoder(bytes.NewReader(b)).Decode(&o)
	if err != nil {
		return err
	}

	s.Session = &xochimilco.Session{}
	s.Session.UnmarshalBinary(o.Session)
	s.SetPeerKey(o.Name, o.Key)

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

	if err := run(ctx, opts); err != nil {
		log(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, opts opts) error {
	// log(opts)

	switch {
	// case opts.Gen:
	// todo?

	case opts.Offer:
		me, key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		sm := NewSessionManager(opts.State, me, key)

		sess, err := sm.New(opts.Them)
		if err != nil {
			return fmt.Errorf("read session: %w", err)
		}
		log("local session", toULID(sess.LocalUUID).String())
		log("remote session", toULID(sess.RemoteUUID).String())
		msg, err := sess.Offer()
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

	case opts.Send:
		me, key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		sm := NewSessionManager(opts.State, me, key)

		input, err := readInputFile(opts.Msg)
		if err != nil {
			return fmt.Errorf("reading input: %w", err)
		}

		sess, err := sm.Get(opts.Them)
		if err != nil {
			return fmt.Errorf("read session: %w", err)
		}
		log("me:", me, "send:", opts.Them)
		log("local session", toULID(sess.LocalUUID).String())
		log("remote session", toULID(sess.RemoteUUID).String())

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

	case opts.Recv:
		me, key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		sm := NewSessionManager(opts.State, me, key)

		id, msg, err := readInputMsg(opts.Msg)
		if err != nil {
			return fmt.Errorf("reading input: %w", err)
		}
		log("msg session", id.String())

		var sess *Session
		if offer, ok := msg.(interface{ Nick() string }); ok {
			sess, err = sm.Get(offer.Nick())
			if err != nil {
				return fmt.Errorf("get session: %w", err)
			}
		} else {
			sess, err = sm.GetID(id)
			if err != nil {
				return fmt.Errorf("get session: %w", err)
			}
		}
		log("local session", toULID(sess.LocalUUID).String())
		log("remote session", toULID(sess.RemoteUUID).String())

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
			if len(plaintext) > 0 {
				fmt.Println(string(plaintext))
				if opts.Post {
					addr, err := saltyim.LookupAddr(opts.Them)
					if err != nil {
						return err
					}

					_, err = http.DefaultClient.Post(addr.Endpoint().String(), "text/plain", bytes.NewReader(plaintext))
					if err != nil {
						return err
					}
				}
			}

		default:
			log("GOT: ", sess.Name, ">", string(plaintext))
		}

		return nil

	case opts.Close:
		me, key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		sm := NewSessionManager(opts.State, me, key)

		sess, err := sm.Get(opts.Them)
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

	case opts.Chat:
		me, key, err := readSaltyIdentity(opts.Key)
		if err != nil {
			return fmt.Errorf("reading keyfile: %w", err)
		}

		addr, err := saltyim.LookupAddr(me)
		if err != nil {
			return fmt.Errorf("lookup addr: %w", err)
		}

		sm := NewSessionManager(opts.State, me, key)
		_ = sm

		uri, inbox := saltyim.SplitInbox(addr.Endpoint().String())
		bus := client.NewClient(uri, nil)

		log("listen to", uri, inbox)

		handleFn := func(in *msgbus.Message) error {
			input := string(in.Payload)
			if !strings.HasPrefix(input, "ratchet") {
				return nil
			}

			log(input)
			id, msg, err := readInputMsg(input)
			if err != nil {
				return err
			}

			sess, err := sm.GetID(id)
			if err != nil {
				return fmt.Errorf("get session: %w", err)
			}

			isEstablished, isClosed, plaintext, err := sess.ReceiveMsg(msg)
			if err != nil {
				return fmt.Errorf("session receive: %w", err)
			}
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
				if len(plaintext) > 0 {
					fmt.Println(string(plaintext))
					if opts.Post {
						addr, err := saltyim.LookupAddr(sess.Name)
						if err != nil {
							return err
						}

						_, err = http.DefaultClient.Post(addr.Endpoint().String(), "text/plain", bytes.NewReader(plaintext))
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

		s := bus.Subscribe(inbox, 0, handleFn)
		return s.Run(ctx)

	default:
		log(usage)
	}

	return nil
}

func enc(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
func readSaltyIdentity(keyfile string) (string, ed25519.PrivateKey, error) {
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

	return addr.Addr().String(), addr.Key().Private(), nil
}

func fetchKey(to string) (ed25519.PublicKey, error) {
	log("fetch key: ", to)
	addr, err := saltyim.LookupAddr(to)
	if err != nil {
		return nil, err
	}

	return addr.Key().Bytes(), nil
}

func sessionhash(self string, id ulid.ULID) string {
	h := fnv.New128a()
	fmt.Fprint(h, self)
	h.Write(id.Entropy())
	return enc(h.Sum(nil))
}

func log(a ...any) {
	fmt.Fprintln(os.Stderr, a...)
}

func readInputFile(input string) (msg string, err error) {
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
	return strings.TrimSpace(msg), nil
}

func readInputMsg(input string) (id ulid.ULID, msg xochimilco.Msg, err error) {
	input, err = readInputFile(input)
	if err != nil {
		return
	}
	log(input)

	msg, err = xochimilco.Parse(input)
	if err != nil {
		return
	}

	copy(id[:], msg.ID())

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
func (sm *DiskSessionManager) New(them string) (*Session, error) {
	id, err := ulid.New(ulid.Now(), nil)
	if err != nil {
		return nil, err
	}
	h := fnv.New128a()
	fmt.Fprint(h, them)
	id.SetEntropy(h.Sum(nil)[:10])

	key, err := fetchKey(them)
	if err != nil {
		return nil, fmt.Errorf("fetching key for %s: %w", them, err)
	}
	return NewSession(id, sm.me, sm.key, them, key), nil
}
func (sm *DiskSessionManager) Get(them string) (*Session, error) {
	id, err := ulid.New(ulid.Now(), nil)
	if err != nil {
		return nil, err
	}
	h := fnv.New128a()
	fmt.Fprint(h, them)
	id.SetEntropy(h.Sum(nil)[:10])

	s, err := sm.GetID(id)
	if errors.Is(err, fs.ErrNotExist) {
		return sm.New(them)
	}

	return s, err
}
func (sm *DiskSessionManager) GetID(id ulid.ULID) (*Session, error) {
	sh := sessionhash(sm.me, id)
	filename := filepath.Join(sm.path, sh)

	log("READ: ", filename)
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

	sess := &Session{}
	err = sess.UnmarshalBinary(b)
	return sess, err
}
func (sm *DiskSessionManager) Put(sess *Session) error {
	sh := sessionhash(sm.me, toULID(sess.LocalUUID))
	filename := filepath.Join(sm.path, sh)

	log("SAVE: ", filename)
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
	u := ulid.ULID{}
	copy(u[:], sess.LocalUUID)
	sh := sessionhash(sm.me, u)
	filename := filepath.Join(sm.path, sh)
	log("REMOVE:", filename)
	return os.Remove(filename)
}
func toULID(b []byte) ulid.ULID {
	var id ulid.ULID
	copy(id[:], b)
	return id
}
