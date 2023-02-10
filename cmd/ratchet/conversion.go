package main

import (
	"encoding/base64"

	"github.com/oklog/ulid/v2"
)

func enc(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
func toULID(b []byte) ulid.ULID {
	var id ulid.ULID
	copy(id[:], b)
	return id
}
