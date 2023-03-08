package client_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/foxcpp/go-mockdns"
	"github.com/keys-pub/keys"
	"github.com/matryer/is"
	"go.salty.im/ratchet/client"
	"go.salty.im/ratchet/session"
)

func TestMain(m *testing.M) {
	// Setup
	http.DefaultClient.Transport = httpMock(func(r *http.Request) (*http.Response, error) {
		fmt.Fprintln(os.Stderr, r.URL)

		switch r.URL.String() {
		case "https://ev.sour.is/.well-known/salty/01bc6186d015218c23dec55447e502e669ca4c61c7566dfcaa1cac256108dff0.json":
			return &http.Response{
				Status:     http.StatusText(http.StatusOK),
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"endpoint":"https://ev.sour.is/inbox/01GPYAXX53N6GCKJV2BPJGTQPB","key":"kex1ac2s0vwskgctgjucqldtd5k4v5xjxv80smf0n9dqqags43keu7usqfh9ud"}`)),
			}, nil

		case "https://ev.sour.is/.well-known/salty/dd431ebefb0660bc09632817618e65322ee2540aa8ca60b4db2be8fb58e4056d.json":
			return &http.Response{
				Status:     http.StatusText(http.StatusOK),
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"endpoint":"https://ev.sour.is/inbox/01GPYAXX53N6GCKJV2BPJGTQPB","key":"kex1ac2s0vwskgctgjucqldtd5k4v5xjxv80smf0n9dqqags43keu7usqfh9ud"}`)),
			}, nil
		}
		return &http.Response{Status: http.StatusText(http.StatusNotFound), StatusCode: http.StatusNotFound}, nil
	})
	defer func() { http.DefaultClient = &http.Client{} }()

	srv, _ := mockdns.NewServer(map[string]mockdns.Zone{
		"_salty._tcp.sour.is.": {
			SRV: []net.SRV{{
				Target: "test.sour.is.",
				Port:   443,
			}},
		},
	}, false)
	defer srv.Close()

	m.Run()
}

func TestClient(t *testing.T) {
	is := is.New(t)

	path, err := os.MkdirTemp("", "")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(path)

	key := keys.GenerateEdX25519Key()

	sm, close, err := session.NewSessionManager(path, "me@sour.is", key)
	is.NoErr(err)
	defer close()

	c, err := client.New(sm, "me@sour.is")
	is.NoErr(err)
	is.True(c != nil)

	ctx := context.Background()

	ok, err := c.Chat(ctx, "them@sour.is")
	is.NoErr(err)
	is.True(ok)
}

type httpMock func(*http.Request) (*http.Response, error)

func (fn httpMock) RoundTrip(r *http.Request) (*http.Response, error) {
	return fn(r)
}
