// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
// SPDX-License-Identifier: GPL-3.0-or-later

package doubleratchet

import (
	"bytes"
	"encoding/gob"
)

func (dhr *dhRatchet) MarshalBinary() ([]byte, error) {
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
