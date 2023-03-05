// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
// SPDX-License-Identifier: GPL-3.0-or-later

package doubleratchet

import (
	"bytes"
	"encoding/gob"
)

func (dhr *dhRatchet) MarshalBinary() ([]byte, error) {
	if dhr == nil {
		return nil, nil
	}

	var buf bytes.Buffer
	o := struct {
		RootKey   []byte
		DhPub     []byte
		DhPriv    []byte
		PeerDhPub []byte

		IsActive      bool
		IsInitialized bool
	}{
		dhr.rootKey,
		dhr.dhPub,
		dhr.dhPriv,
		dhr.peerDhPub,
		dhr.isActive,
		dhr.isInitialized,
	}

	err := gob.NewEncoder(&buf).Encode(o)

	return buf.Bytes(), err
}

func (dhr *dhRatchet) UnmarshalBinary(b []byte) error {
	if len(b) == 0 {
		return nil
	}

	var o struct {
		RootKey   []byte
		DhPub     []byte
		DhPriv    []byte
		PeerDhPub []byte

		IsActive      bool
		IsInitialized bool
	}
	err := gob.NewDecoder(bytes.NewReader(b)).Decode(&o)

	dhr.rootKey = o.RootKey
	dhr.dhPriv = o.DhPriv
	dhr.dhPub = o.DhPub
	dhr.dhPriv = o.DhPriv
	dhr.peerDhPub = o.PeerDhPub
	dhr.isActive = o.IsActive
	dhr.isInitialized = o.IsInitialized

	return err
}

func (kb *keyBuffer) MarshalBinary() ([]byte, error) {
	if kb == nil {
		return nil, nil
	}

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
	if len(b) == 0 {
		return nil
	}

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

func (dr *DoubleRatchet) MarshalBinary() ([]byte, error) {
	dhr, err := dr.dhr.MarshalBinary()
	if err != nil {
		return nil, err
	}

	mkb, err := dr.msgKeyBuffer.MarshalBinary()
	if err != nil {
		return nil, err
	}

	o := struct {
		AssociatedData []byte

		Dhr []byte

		PeerDhPub    []byte
		ChainKeySend []byte
		ChainKeyRecv []byte

		SendNo     int
		RecvNo     int
		PrevSendNo int

		MsgKeyBuffer []byte
	}{
		dr.associatedData,
		dhr,
		dr.peerDhPub,
		dr.chainKeySend,
		dr.chainKeyRecv,
		dr.sendNo,
		dr.recvNo,
		dr.prevSendNo,
		mkb,
	}

	var buf bytes.Buffer
	err = gob.NewEncoder(&buf).Encode(o)

	return buf.Bytes(), err
}
func (dr *DoubleRatchet) UnmarshalBinary(b []byte) error {
	if len(b) == 0 {
		return nil
	}
	var o struct {
		AssociatedData []byte

		Dhr []byte

		PeerDhPub    []byte
		ChainKeySend []byte
		ChainKeyRecv []byte

		SendNo     int
		RecvNo     int
		PrevSendNo int

		MsgKeyBuffer []byte
	}
	err := gob.NewDecoder(bytes.NewReader(b)).Decode(&o)
	if err != nil {
		return err
	}
	dr.associatedData = o.AssociatedData
	dr.peerDhPub = o.PeerDhPub
	dr.chainKeySend = o.ChainKeySend
	dr.chainKeyRecv = o.ChainKeyRecv
	dr.sendNo = o.SendNo
	dr.recvNo = o.RecvNo

	dr.dhr = &dhRatchet{}
	err = dr.dhr.UnmarshalBinary(o.Dhr)
	if err != nil {
		return err
	}

	dr.msgKeyBuffer = newKeyBuffer()
	err = dr.msgKeyBuffer.UnmarshalBinary(o.MsgKeyBuffer)

	return err
}
