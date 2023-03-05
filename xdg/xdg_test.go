package xdg_test

import (
	"strings"
	"testing"

	"go.salty.im/ratchet/xdg"
)

func TestXDG(t *testing.T) {
	path := xdg.Get(xdg.EnvCacheHome, "test")
	if !strings.HasSuffix(path, "test") {
		t.Fatal("missing suffix")
	}
}
