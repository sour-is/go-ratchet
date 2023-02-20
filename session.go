// SPDX-FileCopyrightText: 2021 Alvar Penning
//
// SPDX-License-Identifier: GPL-3.0-or-later

package xochimilco

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/gob"
	"fmt"

	"github.com/oklog/ulid"
	"github.com/sour-is/xochimilco/doubleratchet"
	"github.com/sour-is/xochimilco/x3dh"
)

// Session between two parties to exchange encrypted messages.
//
// Each party creates a new Session variable configured with their private
// long time identity key and a function callback to verify the other party's
// public identity key.
//
// The active party must start by offering to "upgrade" the current channel
// (Offer). Afterwards, the other party must confirm this step (Acknowledge).
// Once the first party finally receives the acknowledgement (Receive), the
// connection is established.
//
// Now both parties can create encrypted messages directed to the other (Send).
// Furthermore, the Session can be closed again (Close). Incoming messages can
// be inspected and the payload extracted, if present (Receive).
type Session struct {
	// LocalUUID is a unique identifier for the session. Provided in the offer.
	LocalUUID  []byte
	RemoteUUID []byte

	Me string

	// IdentityKey is this node's private Ed25519 identity key.
	//
	// This will only be used within the X3DH key agreement protocol. The other
	// party might want to verify this key's public part.
	IdentityKey ed25519.PrivateKey

	// VerifyPeer is a callback during session initialization to verify the
	// other party's public key.
	//
	// To determine when a key is correct is out of Xochimilco's scope. The key
	// might be either exchanged over another secure channel or a trust on first
	// use (TOFU) principle might be used.
	VerifyPeer func(peer ed25519.PublicKey) (valid bool)

	// private fields //

	// spkPub / spkPriv is the X3DH signed prekey for our opening party.
	spkPub, spkPriv []byte

	// doubleRatchet is the internal Double Ratchet.
	doubleRatchet *doubleratchet.DoubleRatchet
}

func (sess *Session) MarshalBinary() ([]byte, error) {
	var err error
	var dr []byte

	if sess.doubleRatchet != nil {
		dr, err = sess.doubleRatchet.MarshalBinary()
		if err != nil {
			return nil, err
		}
	}
	o := struct {
		LocalUUID    []byte
		RemoteUUID   []byte
		Me           string
		SpkPub       []byte
		SpkPriv      []byte
		DoubleRachet []byte
	}{
		sess.LocalUUID,
		sess.RemoteUUID,
		sess.Me,
		sess.spkPub,
		sess.spkPriv,
		dr,
	}
	var buf bytes.Buffer
	err = gob.NewEncoder(&buf).Encode(o)
	return buf.Bytes(), err
}
func (sess *Session) UnmarshalBinary(b []byte) error {
	var o struct {
		LocalUUID    []byte
		RemoteUUID   []byte
		Me           string
		SpkPub       []byte
		SpkPriv      []byte
		DoubleRachet []byte
	}
	err := gob.NewDecoder(bytes.NewReader(b)).Decode(&o)
	if err != nil {
		return err
	}

	sess.Me = o.Me
	sess.LocalUUID = o.LocalUUID
	sess.RemoteUUID = o.RemoteUUID
	sess.spkPub = o.SpkPub
	sess.spkPriv = o.SpkPriv
	if len(o.DoubleRachet) > 0 {
		sess.doubleRatchet = &doubleratchet.DoubleRatchet{}
		err = sess.doubleRatchet.UnmarshalBinary(o.DoubleRachet)
	}
	return err
}

// Active returns true if the session has been activated.
func (sess *Session) Active() bool {
	return sess.doubleRatchet != nil
}

// Offer to establish an encrypted Session.
//
// This method MUST be called initially by the active resp. opening party
// (Alice) once. The other party will hopefully Acknowledge this message.
func (sess *Session) Offer() (offerMsg string, err error) {
	offer, err := sess.createOffer()
	if err != nil {
		return
	}

	offerMsg, err = marshalMessage(sessOffer, offer)
	return
}

func (sess *Session) OfferSealed(k []byte) (offerMsg string, err error) {
	offer, err := sess.createOffer()
	if err != nil {
		return
	}

	sealed, err := Seal(offer, k)
	if err != nil {
		return
	}

	offerMsg, err = marshalMessage(sessSealed, sealed)
	return
}

func (sess *Session) createOffer() (offer *offerMessage, err error) {
	spkPub, spkPriv, spkSig, err := x3dh.CreateNewSpk(sess.IdentityKey)
	if err != nil {
		return
	}

	sess.spkPub = spkPub
	sess.spkPriv = spkPriv

	offer = &offerMessage{
		uuid:  sess.LocalUUID,
		nick:  []byte(sess.Me),
		idKey: sess.IdentityKey.Public().(ed25519.PublicKey),
		spKey: spkPub,
		spSig: spkSig,
	}
	return
}

// Acknowledge to establish an encrypted Session.
//
// This method MUST be called by the passive party (Bob) with the active party's
// (Alice's) offer message. The created acknowledge message MUST be send back.
//
// At this point, this passive part is able to send and receive messages.
func (sess *Session) Acknowledge(offerMsg string) (ackMsg string, err error) {
	msgType, offerIf, err := unmarshalMessage(offerMsg)
	if err != nil {
		return
	} else if msgType != sessOffer {
		err = fmt.Errorf("unexpected message type %d", msgType)
		return
	}
	offer := offerIf.(*offerMessage)
	_, ackMsg, err = sess.receiveOffer(offer)

	return
}

func (sess *Session) receiveOffer(offer *offerMessage) (isEstablished bool, ackMsg string, err error) {
	if !sess.VerifyPeer(offer.idKey) {
		err = fmt.Errorf("verification function refuses public key")
		return
	}

	sessKey, associatedData, ekPub, err := x3dh.CreateInitialMessage(
		sess.IdentityKey, offer.idKey, offer.spKey, offer.spSig)
	if err != nil {
		return
	}

	sess.RemoteUUID = offer.uuid
	sess.doubleRatchet, err = doubleratchet.CreateActive(sessKey, associatedData, offer.spKey)
	if err != nil {
		return
	}

	// This will be padded up to 32 bytes for AES-256.
	initialPayload := make([]byte, 23)
	copy(initialPayload[:16], sess.LocalUUID)
	if _, err = rand.Read(initialPayload[16:]); err != nil {
		return
	}
	initialCiphertext, err := sess.doubleRatchet.Encrypt(initialPayload)
	if err != nil {
		return
	}

	isEstablished = true
	ack := ackMessage{
		idKey:  sess.IdentityKey.Public().(ed25519.PublicKey),
		eKey:   ekPub,
		cipher: initialCiphertext,
		uuid:   sess.RemoteUUID,
	}
	sess.IdentityKey = nil
	ackMsg, err = marshalMessage(sessAck, ack)

	return
}

// receiveAck deals with incoming sessAck messages.
//
// The active / opening party receives the other party's acknowledgement and
// tries to establish a Session.
func (sess *Session) receiveAck(ack *ackMessage) (isEstablished bool, err error) {
	if sess.doubleRatchet != nil {
		err = fmt.Errorf("received sessAck while being in an active session")
		return
	}

	if !sess.VerifyPeer(ack.idKey) {
		err = fmt.Errorf("verification function refuses public key")
		return
	}

	sessKey, associatedData, err := x3dh.ReceiveInitialMessage(
		sess.IdentityKey, ack.idKey, sess.spkPriv, ack.eKey)
	if err != nil {
		return
	}

	sess.doubleRatchet, err = doubleratchet.CreatePassive(
		sessKey, associatedData, sess.spkPub, sess.spkPriv)
	if err != nil {
		return
	}
	sess.spkPub, sess.spkPriv = nil, nil
	plaintext, err := sess.doubleRatchet.Decrypt(ack.cipher)
	if err != nil {
		return
	}

	sess.RemoteUUID = plaintext[:16]
	sess.IdentityKey = nil
	isEstablished = true
	return
}

// receiveData deals with incoming sessData messages.
func (sess *Session) receiveData(data *dataMessage) (plaintext []byte, err error) {
	if sess.doubleRatchet == nil {
		err = fmt.Errorf("received sessData while not being in an active session")
		return
	}

	ciphertext := data.payload
	plaintext, err = sess.doubleRatchet.Decrypt(ciphertext)
	return
}

// Receive an incoming message.
//
// All messages except the passive party's initial offer message MUST be passed
// to this method. The multiple return fields indicate this message's kind.
//
// If the active party receives its first (acknowledge) message, this Session
// will be established; isEstablished. If the other party has signaled to close
// the Session, isClosed is set. This Session MUST then also be closed down. In
// case of an incoming encrypted message, the plaintext field holds its
// decrypted plaintext value. Of course, there might also be an error.
func (sess *Session) Receive(msg string) (isEstablished, isClosed bool, plaintext []byte, err error) {
	_, msgIf, err := unmarshalMessage(msg)
	if err != nil {
		return
	}
	return sess.ReceiveMsg(msgIf)
}
func (sess *Session) ReceiveMsg(msg Msg) (isEstablished, isClosed bool, plaintext []byte, err error) {
	switch msg := msg.(type) {
	case *offerMessage:
		var txt string
		isEstablished, txt, err = sess.receiveOffer(msg)
		plaintext = []byte(txt)

	case *ackMessage:
		isEstablished, err = sess.receiveAck(msg)

	case *dataMessage:
		plaintext, err = sess.receiveData(msg)

	case *closeMessage:
		isClosed = true

	default:
		err = fmt.Errorf("received an unexpected message type %T", msg)
	}

	return
}

// Send a message to the other party. The given plaintext byte array will be
// embedded in an encrypted message.
//
// This method is allowed to be called after the initial handshake, Offer resp.
// Acknowledge.
func (sess *Session) Send(plaintext []byte) (dataMsg string, err error) {
	if sess.doubleRatchet == nil {
		err = fmt.Errorf("cannot encrypt data without being in an active session")
		return
	}

	ciphertext, err := sess.doubleRatchet.Encrypt(plaintext)
	if err != nil {
		return
	}

	dataMsg, err = marshalMessage(sessData, &dataMessage{encTime(sess.RemoteUUID), ciphertext})
	return
}

// Close this Session and tell the other party to do the same.
//
// This resets the internal state. Thus, the same Session might be reused.
func (sess *Session) Close() (closeMsg string, err error) {
	sess.spkPub, sess.spkPriv = nil, nil
	sess.doubleRatchet = nil

	closeMsg, err = marshalMessage(sessClose, &closeMessage{sess.RemoteUUID, []byte{0xff}})
	return
}

func encTime(in []byte) []byte {
	u := ulid.ULID{}
	copy(u[:], in)
	u.SetTime(ulid.Now())
	return u[:]
}
