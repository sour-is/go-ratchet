// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
//
// SPDX-License-Identifier: BSD-3-Clause
package interactive

import (
	"github.com/oklog/ulid/v2"
)

func toULID(b []byte) ulid.ULID {
	var id ulid.ULID
	copy(id[:], b)
	return id
}
