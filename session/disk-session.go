// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
// SPDX-License-Identifier: BSD-3-Clause
package session

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"

	"github.com/keys-pub/keys"
	"github.com/oklog/ulid/v2"
	"go.salty.im/ratchet/xochimilco"
	"go.salty.im/saltyim"
)

type Session struct {
	Name     string
	PeerKey  *keys.EdX25519PublicKey
	Endpoint string

	PendingAck string

	*xochimilco.Session
}

func NewSession(id ulid.ULID, me string, key *keys.EdX25519Key, name string, them saltyim.Addr) *Session {
	sess := &Session{
		Endpoint: them.Endpoint().String(),
		PeerKey:  them.Key(),
		Session: &xochimilco.Session{
			IdentityKey: key.Private(),
			Me:          me,
			LocalUUID:   id[:],
		},
	}
	sess.SetPeerKey(name, them.Key().Bytes())
	return sess
}
func (s *Session) SetPeerKey(name string, p []byte) {
	s.Name = name
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
		Name       string
		Key        string
		Endpoint   string
		PendingAck string
		Session    []byte
	}{
		Name:       s.Name,
		Key:        s.PeerKey.String(),
		Endpoint:   s.Endpoint,
		Session:    sess,
		PendingAck: s.PendingAck,
	}

	var buf bytes.Buffer
	err = gob.NewEncoder(&buf).Encode(o)
	return buf.Bytes(), err
}
func (s *Session) UnmarshalBinary(b []byte) error {
	var o struct {
		Name       string
		Endpoint   string
		Key        string
		PendingAck string
		Session    []byte
	}

	err := gob.NewDecoder(bytes.NewReader(b)).Decode(&o)
	if err != nil {
		return err
	}

	s.Session = &xochimilco.Session{}
	s.Session.UnmarshalBinary(o.Session)
	id, err := keys.ParseID(o.Key)
	if err != nil {
		return err
	}
	s.PeerKey, err = keys.NewEdX25519PublicKeyFromID(id)
	if err != nil {
		return err
	}
	s.SetPeerKey(o.Name, s.PeerKey.Bytes())
	s.Endpoint = o.Endpoint
	s.PendingAck = o.PendingAck

	return err
}
func (s *Session) ReceiveMsg(msg xochimilco.Msg) (isEstablished, isClosed bool, plaintext []byte, err error) {
	isEstablished, isClosed, plaintext, err = s.Session.ReceiveMsg(msg)
	if isEstablished {
		s.PendingAck = string(plaintext)
		plaintext = plaintext[:0]
	}
	return
}
func (s *Session) Offer() (string, error) {
	return s.Session.OfferSealed(s.PeerKey.X25519PublicKey().Bytes32())
}

type DiskSessionManager struct {
	me       string
	key      *keys.EdX25519Key
	path     string
	pos      int64
	sessions map[string]ulid.ULID
}

func NewSessionManager(path, me string, key *keys.EdX25519Key) (*DiskSessionManager, func() error, error) {
	dm := &DiskSessionManager{me, key, path, -1, make(map[string]ulid.ULID)}
	return dm, dm.Close, dm.Load()
}
func (sm *DiskSessionManager) Identity() *keys.EdX25519Key {
	return sm.key
}
func (sm *DiskSessionManager) ByName(name string) ulid.ULID {
	if u, ok := sm.sessions[name]; ok {
		return u
	}
	sm.sessions[name] = ulid.Make()
	return sm.sessions[name]
}
func (sm *DiskSessionManager) New(them string) (*Session, error) {
	id := sm.ByName(them)
	addr, err := fetchKey(them)
	if err != nil {
		return nil, fmt.Errorf("fetching key for %s: %w", them, err)
	}
	return NewSession(id, sm.me, sm.key, them, addr), nil
}
func (sm *DiskSessionManager) Get(id ulid.ULID) (*Session, error) {
	sh := sessionhash(sm.me, id)
	filename := filepath.Join(sm.path, sh)

	// log("READ: ", filename)
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

	// session only needs private key during initial handshake.
	if !sess.Active() {
		sess.IdentityKey = sm.key.Private()
	}

	return sess, err
}
func (sm *DiskSessionManager) Put(sess *Session) error {
	sh := sessionhash(sm.me, toULID(sess.LocalUUID))
	filename := filepath.Join(sm.path, sh)

	// log("SAVE: ", filename)
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
	// log("REMOVE:", filename)
	delete(sm.sessions, sess.Name)
	return os.Remove(filename)
}
func (sm *DiskSessionManager) Load() error {
	fp, err := os.Open(filepath.Join(sm.path, "sess-"+sm.me+".json"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return err
	}
	defer fp.Close()

	type item struct {
		Name    string
		Session ulid.ULID
	}
	var data struct {
		Position int64
		Sessions []item
	}

	err = json.NewDecoder(fp).Decode(&data)
	if err != nil {
		return err
	}

	if data.Position > 0 {
		sm.pos = data.Position
	}
	for _, v := range data.Sessions {
		sm.sessions[v.Name] = v.Session
	}

	return nil
}
func (sm *DiskSessionManager) Close() error {
	name := filepath.Join(sm.path, "sess-"+sm.me+".json")
	fp, err := os.OpenFile(name, os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer fp.Close()
	// defer log("Saved Session Data ", name)

	type item struct {
		Name    string
		Session ulid.ULID
	}
	var data struct {
		Position int64
		Sessions []item
	}
	data.Position = sm.pos
	for k, v := range sm.sessions {
		data.Sessions = append(data.Sessions, item{k, v})
	}

	return json.NewEncoder(fp).Encode(data)
}
func (sm *DiskSessionManager) Position() int64 {
	return sm.pos
}
func (sm *DiskSessionManager) SetPosition(pos int64) {
	sm.pos = pos
}

type Pair[K, V any] struct {
	Name K
	ID   V
}

func (sm *DiskSessionManager) Sessions() []Pair[string, ulid.ULID] {
	lis := make([]Pair[string, ulid.ULID], 0, len(sm.sessions))
	for k, v := range sm.sessions {
		lis = append(lis, Pair[string, ulid.ULID]{k, v})
	}
	return lis
}

func sessionhash(self string, id ulid.ULID) string {
	h := fnv.New128a()
	fmt.Fprint(h, self)
	h.Write(id.Entropy())
	return enc(h.Sum(nil))
}

// func log(a ...any) {
// 	fmt.Fprintf(os.Stderr, "\033[90m%s\033[0m\n", fmt.Sprint(a...))
// }

func enc(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

func toULID(b []byte) ulid.ULID {
	var id ulid.ULID
	copy(id[:], b)
	return id
}

func fetchKey(to string) (saltyim.Addr, error) {
	// log("fetch key: ", to)
	addr, err := saltyim.LookupAddr(to)
	if err != nil {
		return nil, err
	}
	// log(addr.Endpoint())

	return addr, nil
}

var (
	ErrNotExist = errors.New("does not exist")
	ErrInternal = errors.New("internal error")
)
