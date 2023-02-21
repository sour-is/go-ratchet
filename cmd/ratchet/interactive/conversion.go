package interactive

import (
	"github.com/oklog/ulid/v2"
)

func toULID(b []byte) ulid.ULID {
	var id ulid.ULID
	copy(id[:], b)
	return id
}
