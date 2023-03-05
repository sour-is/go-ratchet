package doubleratchet

import (
	"testing"
)

func TestMarshal(t *testing.T) {
	dr := &DoubleRatchet{
		dhr: &dhRatchet{},
		msgKeyBuffer: newKeyBuffer(),
	}
	dr.msgKeyBuffer.elementAdd(nil)


	b, err := dr.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	
	err = dr.UnmarshalBinary(b)
	if err != nil {
		t.Fatal(err)
	}
}
