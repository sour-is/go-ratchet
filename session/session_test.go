// SPDX-FileCopyrightText: 2023 Jon Lundy <jon@xuu.cc>
// SPDX-License-Identifier: BSD-3-Clause
package session_test

import (
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/foxcpp/go-mockdns"
	"github.com/keys-pub/keys"
	"github.com/matryer/is"

	"go.salty.im/ratchet/session"
)

func TestSessionManager(t *testing.T) {
	// Setup
	http.DefaultClient.Transport = httpMock(func(r *http.Request) (*http.Response, error) {
		t.Log(r.URL)

		return &http.Response{
			Status:     http.StatusText(http.StatusOK),
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"endpoint":"https://ev.sour.is/inbox/01GPYAXX53N6GCKJV2BPJGTQPB","key":"kex1ac2s0vwskgctgjucqldtd5k4v5xjxv80smf0n9dqqags43keu7usqfh9ud"}`)),
		}, nil
	})
	defer func() { http.DefaultClient = &http.Client{} }()

	srv, _ := mockdns.NewServer(map[string]mockdns.Zone{
		"_salty._tcp.sour.is.": {
			SRV: []net.SRV{{
				Target: "test.sour.is.",
				Port: 443,
		}},
		},
	}, false)
	defer srv.Close()
	
	srv.PatchNet(net.DefaultResolver)
	defer mockdns.UnpatchNet(net.DefaultResolver)
	
	path, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(path)

	key := keys.GenerateEdX25519Key()

	// Start test
	is := is.New(t)

	// Create new manager.
	sm, close, err := session.NewSessionManager(path, "me@sour.is", key)
	is.NoErr(err)

	// Manager is empty
	is.Equal(len(sm.Sessions()), 0)

	// Create new session.
	them, err := sm.New("bob@sour.is")
	is.NoErr(err)

	// Write session.
	err = sm.Put(them)
	is.NoErr(err)

	// Close manager.
	is.NoErr(close())

	// Reopen manager.
	sm, close, err = session.NewSessionManager(path, "me@sour.is", key)
	is.NoErr(err)
	defer is.NoErr(close())

	// Manager not empty
	is.Equal(len(sm.Sessions()), 1)

	// Get session
	them, err = sm.Get(sm.ByName("bob@sour.is"))
	is.NoErr(err)

	// Session has right name.
	is.Equal(them.Name, "bob@sour.is")

	// Delete session.
	err = sm.Delete(them)
	is.NoErr(err)

	// Manager is empty.
	is.Equal(len(sm.Sessions()), 0)
}

type httpMock func(*http.Request) (*http.Response, error)

func (fn httpMock) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
