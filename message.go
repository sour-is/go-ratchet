// SPDX-FileCopyrightText: 2021 Alvar Penning
//
// SPDX-License-Identifier: GPL-3.0-or-later

package xochimilco

import (
	"bytes"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/nacl/box"
)

// messageType identifies the message's type resp. its state and desired action.
type messageType byte

const (
	_ messageType = iota

	// sessOffer is Alice's initial message, asking Bob to upgrade their
	// conversation by advertising her X3DH parameters.
	sessOffer

	// sessAck is Bob's first answer, including his X3DH parameters as well as
	// a first nonsense message as a ciphertext to setup the Double Ratchet.
	sessAck

	// sessData are encrypted messages exchanged between the both parties.
	sessData

	// sessClose cancels a Xochimilco session. This is possible in each state
	// and might occur due to a regular closing as well as rejecting an identity
	// key.
	// A MITM can also send this. However, a MITM can also drop messages.
	sessClose

	// sessSealed wraps the message into an anonymous nacl box. This
	// is used for concealing the offer so the nick is not exposed.
	sessSealed

	// Prefix indicates the beginning of an encoded message.
	Prefix string = "!RAT!"

	// Suffix indicates the end of an encoded message.
	Suffix string = "!CHT!"
)

type Msg interface{ interface{ ID() []byte } }

func Parse(in string) (Msg, error) {
	_, m, err := unmarshalMessage(in)
	return m, err
}

// marshalMessage creates the entire encoded message from a struct.
func marshalMessage(t messageType, m encoding.BinaryMarshaler) (out string, err error) {
	b := new(strings.Builder)

	_, _ = fmt.Fprint(b, Prefix)
	_, _ = fmt.Fprint(b, int(t))

	data, err := m.MarshalBinary()
	if err != nil {
		return
	}

	b64 := base64.NewEncoder(base64.RawURLEncoding, b)
	if _, err = b64.Write(data); err != nil {
		return
	}
	if err = b64.Close(); err != nil {
		return
	}

	_, _ = fmt.Fprint(b, Suffix)

	out = b.String()
	return
}

// unmarshalMessage recreates the struct for an encoded message.
func unmarshalMessage(in string) (t messageType, m Msg, err error) {
	if !strings.HasPrefix(in, Prefix) || !strings.HasSuffix(in, Suffix) {
		err = fmt.Errorf("message string misses pre- and/or suffix")
		return
	}

	t = messageType(in[len(Prefix)] - '0')
	m, err = container(t)
	if err != nil {
		return
	}

	data, err := base64.RawURLEncoding.DecodeString(in[len(Prefix)+1 : len(in)-len(Suffix)])
	if err != nil {
		return
	}

	err = m.(encoding.BinaryUnmarshaler).UnmarshalBinary(data)

	return
}

func container(t messageType) (m Msg, err error) {
	switch t {
	case sessOffer:
		m = new(offerMessage)
	case sessAck:
		m = new(ackMessage)
	case sessData:
		m = new(dataMessage)
	case sessClose:
		m = new(closeMessage)
	case sessSealed:
		m = new(sealedMessage)
	default:
		err = fmt.Errorf("unsupported message type %d", t)
	}
	return
}

// offerMessage is the initial sessOffer message, announcing Alice's public
// Ed25519 Identity Key (32 byte), her X25519 signed prekey (32 byte), and the
// signature (64 bytes).
type offerMessage struct {
	idKey []byte
	spKey []byte
	spSig []byte
	uuid  []byte
	nick  []byte
}

func (msg offerMessage) MarshalBinary() (data []byte, err error) {
	data = make([]byte, 32+32+64+16+len(msg.nick))

	copy(data[:32], msg.idKey)
	copy(data[32:64], msg.spKey)
	copy(data[64:128], msg.spSig)
	copy(data[128:144], msg.uuid)
	copy(data[144:], msg.nick)

	return
}

func (msg *offerMessage) UnmarshalBinary(data []byte) (err error) {
	if len(data) < 32+32+64+16 {
		return fmt.Errorf("sessOffer payload MUST be greater than 144 byte")
	}

	msg.idKey = make([]byte, 32)
	msg.spKey = make([]byte, 32)
	msg.spSig = make([]byte, 64)
	msg.uuid = make([]byte, 16)
	msg.nick = make([]byte, len(data)-(32+32+64+16))

	copy(msg.idKey, data[:32])
	copy(msg.spKey, data[32:64])
	copy(msg.spSig, data[64:128])
	copy(msg.uuid, data[128:144])
	copy(msg.nick, data[144:])

	return
}

func (msg *offerMessage) Nick() string {
	return string(msg.nick)
}

func (msg *offerMessage) ID() []byte {
	return msg.uuid
}

func (msg *offerMessage) Key() ed25519.PublicKey {
	return msg.idKey
}

func (msg *offerMessage) Equal(k ed25519.PublicKey) bool {
	return bytes.Equal(msg.idKey, k)
}

// ackMessage is the second sessAck message for Bob to acknowledge Alice's
// sessOffer, finishing X3DH and starting his Double Ratchet. The fields are
// Bob's Ed25519 public key (32 byte), his ephemeral X25519 key (32 byte) and a
// nonsense initial ciphertext.
type ackMessage struct {
	idKey  []byte
	eKey   []byte
	uuid   []byte
	cipher []byte
}

func (msg ackMessage) MarshalBinary() (data []byte, err error) {
	data = make([]byte, 32+32+16+len(msg.cipher))

	copy(data[:32], msg.idKey)
	copy(data[32:64], msg.eKey)
	copy(data[64:80], msg.uuid)
	copy(data[80:], msg.cipher)

	return
}

func (msg *ackMessage) UnmarshalBinary(data []byte) (err error) {
	if len(data) <= 32+32+16 {
		return fmt.Errorf("sessAck payload MUST be >= 80 byte")
	}

	msg.idKey = make([]byte, 32)
	msg.eKey = make([]byte, 32)
	msg.uuid = make([]byte, 16)
	msg.cipher = make([]byte, len(data)-80)

	copy(msg.idKey, data[:32])
	copy(msg.eKey, data[32:64])
	copy(msg.uuid, data[64:80])
	copy(msg.cipher, data[80:])

	return
}

func (msg *ackMessage) ID() []byte {
	return msg.uuid
}

func (msg *ackMessage) Key() ed25519.PublicKey {
	return msg.idKey
}

func (msg *ackMessage) Equal(k ed25519.PublicKey) bool {
	return bytes.Equal(msg.idKey, k)
}

// dataMessage is the sessData message for the bidirectional exchange of
// encrypted ciphertext. Thus, its length is dynamic.
type dataMessage struct {
	uuid    []byte
	payload []byte
}

func (msg *dataMessage) ID() []byte {
	return msg.uuid
}

func (msg *dataMessage) MarshalBinary() (data []byte, err error) {
	data = make([]byte, 16+len(msg.payload))

	copy(data[:16], msg.uuid)
	copy(data[16:], msg.payload)

	return data, nil
}

func (msg *dataMessage) UnmarshalBinary(data []byte) (err error) {
	if len(data) <= 16 {
		return fmt.Errorf("sessAck payload MUST be >= 16 byte")
	}

	msg.uuid = make([]byte, 16)
	msg.payload = make([]byte, len(data)-16)

	copy(msg.uuid, data[:16])
	copy(msg.payload, data[16:])

	return
}

// closeMessage is the bidirectional sessClose message. Its payload ix 0xff.
type closeMessage struct {
	uuid    []byte
	payload []byte
}

func (msg *closeMessage) ID() []byte {
	return msg.uuid
}

func (msg *closeMessage) MarshalBinary() (data []byte, err error) {
	data = make([]byte, 16+len(msg.payload))

	copy(data[:16], msg.uuid)
	copy(data[16:], msg.payload)

	return data, nil
}

func (msg *closeMessage) UnmarshalBinary(data []byte) (err error) {
	if len(data) <= 16 {
		return fmt.Errorf("sessAck payload MUST be >= 16 byte")
	}

	msg.uuid = make([]byte, 16)
	msg.payload = make([]byte, len(data)-16)

	copy(msg.uuid, data[:16])
	copy(msg.payload, data[16:])

	if subtle.ConstantTimeCompare(msg.payload, []byte{0xff}) != 1 {
		err = fmt.Errorf("sessClose has an inavlid payload")
	}

	return
}

type sealedMessage []byte

func Seal(m encoding.BinaryMarshaler, k []byte) (out sealedMessage, err error) {
	var data []byte

	data, err = m.MarshalBinary()
	if err != nil {
		return
	}

	switch m.(type) {
	case *offerMessage:
		data = append([]byte{'1'}, data...)
	case *ackMessage:
		data = append([]byte{'2'}, data...)
	case *dataMessage:
		data = append([]byte{'3'}, data...)
	case *closeMessage:
		data = append([]byte{'4'}, data...)
	default:
		err = fmt.Errorf("unsupported message type %T", m)
		return
	}

	var key [32]byte
	copy(key[:], k)

	return box.SealAnonymous(nil, data, &key, nil)
}

func (s sealedMessage) Unseal(priv, pub *[32]byte) (m Msg, err error) {
	var ok bool
	var data []byte
	data, ok = box.OpenAnonymous(nil, s, pub, priv)
	if !ok {
		err = fmt.Errorf("unseal invalid")
		return
	}

	m, err = container(messageType(data[0] - '0'))
	if err != nil {
		return
	}

	err = m.(encoding.BinaryUnmarshaler).UnmarshalBinary(data[1:])
	return
}

func (s sealedMessage) MarshalBinary() ([]byte, error) {
	return s, nil
}
func (s *sealedMessage) UnmarshalBinary(data []byte) (err error) {
	if len(data) <= 1 {
		return fmt.Errorf("sessAck payload MUST be >= 1 byte")
	}
	*s = data
	return nil
}

func (s sealedMessage) ID() []byte {
	return nil
}
