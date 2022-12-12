// SPDX-FileCopyrightText: 2021 Alvar Penning
//
// SPDX-License-Identifier: GPL-3.0-or-later

// This file implements a buffer to be used for skipped message keys. The buffer
// consists of a ring buffer mapping sending DH public keys to message numbers.
// By doing so, we can guarantee that no MITM can allocate a huge buffer of
// precalculated message keys for non-existing messages.

package doubleratchet

import (
	"bytes"
	"container/ring"
	"crypto/subtle"
	"encoding/gob"
	"fmt"
)

const (
	// maxSkipChains is the maximum amount of cached chains.
	maxSkipChains = 8

	// maxSkipElements is the maximum amount of message keys per cached chain.
	maxSkipElements = 32
)

// keyBuffer is used within the DoubleRatchet to cache skipped message keys.
type keyBuffer struct {
	buff *ring.Ring
}

func (kb *keyBuffer) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	lis := make([]*keyBufferElement, kb.buff.Len())
	i := 0
	kb.buff.Do(func(a interface{}) {
		if kbe, ok := a.(*keyBufferElement); ok {
			lis[i] = kbe
			i++
		}
	})
	lis = lis[:i]
	err := gob.NewEncoder(&buf).Encode(lis)

	return buf.Bytes(), err
}

func (kb *keyBuffer) UnmarshalBinary(b []byte) error {
	lis := make([]*keyBufferElement, maxSkipChains)
	err := gob.NewDecoder(bytes.NewReader(b)).Decode(&lis)
	if err != nil {
		return err
	}

	for _, kbe := range lis {
		kb.buff.Value = kbe
		kb.buff.Prev()
	}

	return nil
}

// keyBufferElement is the type of a keyBuffer's ring buffer element.
type keyBufferElement struct {
	dhPub   []byte
	msgKeys map[int][]byte
}

func (kb *keyBufferElement) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	o := struct {
		DhPub   []byte
		MsgKeys map[int][]byte
	}{
		kb.dhPub,
		kb.msgKeys,
	}
	err := gob.NewEncoder(&buf).Encode(o)
	return buf.Bytes(), err
}
func (kb *keyBufferElement) UnmarshalBinary(b []byte) error {
	var o struct {
		DhPub   []byte
		MsgKeys map[int][]byte
	}
	err := gob.NewDecoder(bytes.NewReader(b)).Decode(&o)

	kb.dhPub = o.DhPub
	kb.msgKeys = o.MsgKeys

	return err
}

// newKeyBuffer to be used within the DoubleRatchet.
func newKeyBuffer() *keyBuffer {
	return &keyBuffer{ring.New(maxSkipChains)}
}

// elementFind searches for a keyBufferElement in the buffer. If no such element
// is found, nil will be returned.
func (kb *keyBuffer) elementFind(dhPub []byte) (kbe *keyBufferElement) {
	kb.buff.Do(func(e interface{}) {
		if e == nil {
			return
		}

		if subtle.ConstantTimeCompare(dhPub, e.(*keyBufferElement).dhPub) == 1 {
			kbe = e.(*keyBufferElement)
		}
	})

	return
}

// elementAdd creates and returns a new keyBufferElement within the buffer. The
// oldest previous element will be overwritten.
func (kb *keyBuffer) elementAdd(dhPub []byte) (kbe *keyBufferElement) {
	kbe = &keyBufferElement{
		dhPub:   dhPub,
		msgKeys: make(map[int][]byte),
	}

	kb.buff = kb.buff.Prev()
	kb.buff.Value = kbe

	return
}

// find a message key for a sender's DH public key and the message number.
func (kb *keyBuffer) find(dhPub []byte, msgNo int) (msgKey []byte, err error) {
	kbe := kb.elementFind(dhPub)
	if kbe == nil {
		return nil, fmt.Errorf("public key is not cached")
	}

	msgKey, ok := kbe.msgKeys[msgNo]
	if !ok {
		err = fmt.Errorf("message number is not cached")
	}

	return
}

// insert a message key for its sender's DH public key and message number.
func (kb *keyBuffer) insert(dhPub []byte, msgNo int, msgKey []byte) {
	kbe := kb.elementFind(dhPub)
	if kbe == nil {
		kbe = kb.elementAdd(dhPub)
	}

	kbe.msgKeys[msgNo] = msgKey
}
