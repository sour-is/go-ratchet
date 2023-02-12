package main

import (
	"bytes"
	"crypto/ed25519"
	"encoding/gob"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path/filepath"

	"github.com/oklog/ulid/v2"
	"github.com/sour-is/xochimilco"
	"go.mills.io/saltyim"
)

type Session struct {
	Name     string
	PeerKey  ed25519.PublicKey
	Endpoint string

	PendingAck string

	*xochimilco.Session
}

func NewSession(id ulid.ULID, me string, key ed25519.PrivateKey, name string, them saltyim.Addr) *Session {
	sess := &Session{
		Endpoint: them.Endpoint().String(),
		Session: &xochimilco.Session{
			IdentityKey: key,
			Me:          me,
			LocalUUID:   id[:],
		},
	}
	sess.SetPeerKey(name, them.Key().Bytes())
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
		Name       string
		Key        ed25519.PublicKey
		Endpoint   string
		PendingAck string
		Session    []byte
	}{
		Name:       s.Name,
		Key:        s.PeerKey,
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
		Key        ed25519.PublicKey
		PendingAck string
		Session    []byte
	}

	err := gob.NewDecoder(bytes.NewReader(b)).Decode(&o)
	if err != nil {
		return err
	}

	s.Session = &xochimilco.Session{}
	s.Session.UnmarshalBinary(o.Session)
	s.SetPeerKey(o.Name, o.Key)
	s.Endpoint = o.Endpoint
	s.PendingAck = o.PendingAck

	return err
}
func (s *Session) ReceiveMsg(msg xochimilco.Msg) (isEstablished, isClosed bool, plaintext []byte, err error) {
	isEstablished, isClosed, plaintext, err = s.Session.ReceiveMsg(msg)
	if isEstablished {
		s.PendingAck = string(plaintext)
	}
	return
}

type DiskSessionManager struct {
	me       string
	key      ed25519.PrivateKey
	path     string
	sessions map[string]ulid.ULID
}

func NewSessionManager(path, me string, key ed25519.PrivateKey) (*DiskSessionManager, func() error, error) {
	dm := &DiskSessionManager{me, key, path, make(map[string]ulid.ULID)}
	return dm, dm.Close, dm.Load()
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
	log("REMOVE:", filename)
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
		Sessions []item
	}

	err = json.NewDecoder(fp).Decode(&data)
	if err != nil {
		return err
	}

	for _, v := range data.Sessions {
		sm.sessions[v.Name] = v.Session
	}

	return nil
}
func (sm *DiskSessionManager) Close() error {
	fp, err := os.OpenFile(filepath.Join(sm.path, "sess-"+sm.me+".json"), os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer fp.Close()

	type item struct {
		Name    string
		Session ulid.ULID
	}
	var data struct {
		Sessions []item
	}
	for k, v := range sm.sessions {
		data.Sessions = append(data.Sessions, item{k, v})
	}

	return json.NewEncoder(fp).Encode(data)
}
type pair[K, V any] struct{
	Name K
	ID V
}
func (sm *DiskSessionManager) Sessions() []pair[string, ulid.ULID] {
	lis := make([]pair[string,ulid.ULID], len(sm.sessions))
	for k, v := range sm.sessions {
		lis = append(lis, pair[string, ulid.ULID]{k, v})
	}
	return lis
}

func sessionhash(self string, id ulid.ULID) string {
	h := fnv.New128a()
	fmt.Fprint(h, self)
	h.Write(id.Entropy())
	return enc(h.Sum(nil))
}
