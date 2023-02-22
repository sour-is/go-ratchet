package main_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"git.mills.io/saltyim/ratchet/xochimilco"
	"github.com/matryer/is"
)

func TestSession(t *testing.T) {
	tt := "RP+BAwEC/4IAAQQBC0lkZW50aXR5S2V5AQoAAQZTcGtQdWIBCgABB1Nwa1ByaXYBCgABDERvdWJsZVJhY2hldAEKAAAA/4n/ggFA6wi3Ew0I5aHG99kbAgFg0gO5o2ityd4lztMHcZJfiwaonDNo+E2Qo9pNgtmHVDD/ROWgmeZB9GheJFJ6XUXA5QEg2ge8sJcUnHA4fsnmusjp1+6FJBIeOxZAmBviQdEWpVIBIH9qdvKnX84KdomLoqSeOzb2+RE6HmcpjHeYMsCe+SMmAA=="

	is := is.New(t)

	b, err := dec(tt)
	is.NoErr(err)

	sess := &xochimilco.Session{}
	err = sess.UnmarshalBinary(b)
	is.NoErr(err)
}

func dec(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	return base64.StdEncoding.DecodeString(s)
}
