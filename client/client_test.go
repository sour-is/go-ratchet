package client_test

import (
	"context"
	"fmt"
	"io"
	"log"
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

var (
	err   error
	path  string
	alice *keys.EdX25519Key
	bob   *keys.EdX25519Key
)

func TestMain(m *testing.M) {
	// Setup
	path, err = os.MkdirTemp("", "")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(path)

	alice = keys.GenerateEdX25519Key()
	bob = keys.GenerateEdX25519Key()

	http.DefaultClient.Transport = &requests

	requests.fn = func(r *http.Request) (*http.Response, error) {
		fmt.Fprintln(os.Stderr, r.URL)

		switch r.URL.String() {
		case "https://ev.sour.is/.well-known/salty/828c20c06628c46014048f6ddf2d7f89f3bedf667240398f08e47fb13dfabfe9.json":
			return &http.Response{
				Status:     http.StatusText(http.StatusOK),
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"endpoint":"https://ev.sour.is/inbox/01GPYAZ0GX8VCPK9CFEDPA1QG0","key": "` + alice.PublicKey().String() + `"}`)),
			}, nil

		case "https://ev.sour.is/.well-known/salty/f202c7f09045e1bea055c4bef3e585cf9c74e21a342a59dedd505d09dac53ba7.json":
			return &http.Response{
				Status:     http.StatusText(http.StatusOK),
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"endpoint":"https://ev.sour.is/inbox/01GPYAXX53N6GCKJV2BPJGTQPB","key":"` + bob.PublicKey().String() + `"}`)),
			}, nil
		case "https://ev.sour.is/inbox/01GPYAXX53N6GCKJV2BPJGTQPB":
			return &http.Response{
				Status:     http.StatusText(http.StatusAccepted),
				StatusCode: http.StatusAccepted,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
			}, nil
		case "https://ev.sour.is/inbox/01GPYAZ0GX8VCPK9CFEDPA1QG0":
			return &http.Response{
				Status:     http.StatusText(http.StatusAccepted),
				StatusCode: http.StatusAccepted,
				Body:       io.NopCloser(strings.NewReader(`{}`)),
			}, nil
		}
		return &http.Response{Status: http.StatusText(http.StatusNotFound), StatusCode: http.StatusNotFound}, nil
	}

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
	ctx := context.Background()

	{ // Make offer
		sm, closeSession, err := session.NewSessionManager(path, "alice@sour.is", alice)
		is.NoErr(err)

		c, err := client.New(sm, "alice@sour.is")
		is.NoErr(err)
		is.True(c != nil)

		ok, err := c.Chat(ctx, "bob@sour.is")
		is.NoErr(err)
		is.True(!ok)

		err = closeSession()
		is.NoErr(err)
	}

	offerPayload, err := io.ReadAll(requests.reqs[2].Body)
	is.NoErr(err)

	{ // Receive offer and ack
		sm, closeSession, err := session.NewSessionManager(path, "bob@sour.is", bob)
		is.NoErr(err)

		c, err := client.New(sm, "bob@sour.is")
		is.NoErr(err)
		is.True(c != nil)

		var offer client.OnOfferReceived
		done := make(chan struct{})
		client.On(c, func(ctx context.Context, args client.OnOfferReceived) { offer = args; close(done) })

		err = c.Input(client.OnInput{1, string(offerPayload)})
		is.NoErr(err)

		<-done
		is.Equal(offer.Them, "alice@sour.is")

		ctx := context.Background()

		ok, err := c.Chat(ctx, "alice@sour.is")
		is.NoErr(err)
		is.True(ok)

		err = c.Send(ctx, "alice@sour.is", "Hello, Bob.")
		is.NoErr(err)

		err = closeSession()
		is.NoErr(err)
	}

	ackPayload, err := io.ReadAll(requests.reqs[5].Body)
	is.NoErr(err)

	msgPayload, err := io.ReadAll(requests.reqs[6].Body)
	is.NoErr(err)

	{ // Receive ack and message, send close.
		sm, closeSession, err := session.NewSessionManager(path, "alice@sour.is", alice)
		is.NoErr(err)

		c, err := client.New(sm, "alice@sour.is")
		is.NoErr(err)
		is.True(c != nil)

		ackRcvd := make(chan struct{})
		msgRcvd := make(chan struct{})

		var ack client.OnSessionStarted
		var msg client.OnMessageReceived

		client.On(c, func(ctx context.Context, args client.OnSessionStarted) { ack = args; close(ackRcvd) })
		client.On(c, func(ctx context.Context, args client.OnMessageReceived) { msg = args; close(msgRcvd) })

		err = c.Input(client.OnInput{1, string(ackPayload)})
		is.NoErr(err)

		err = c.Input(client.OnInput{1, string(msgPayload)})
		is.NoErr(err)

		<-ackRcvd
		<-msgRcvd

		is.Equal(ack.Them, "bob@sour.is")
		is.Equal(msg.Them, "bob@sour.is")
		is.Equal(msg.Msg.LiteralText(), "Hello, Bob.")

		err = c.Close(ctx, "bob@sour.is")
		is.NoErr(err)

		err = closeSession()
		is.NoErr(err)
	}

	closePayload, err := io.ReadAll(requests.reqs[8].Body)
	is.NoErr(err)
	
	{ // receive close
		sm, closeSession, err := session.NewSessionManager(path, "bob@sour.is", bob)
		is.NoErr(err)

		c, err := client.New(sm, "bob@sour.is")
		is.NoErr(err)
		is.True(c != nil)

		var msg client.OnSessionClosed
		done := make(chan struct{})
		client.On(c, func(ctx context.Context, args client.OnSessionClosed) { msg = args; close(done) })

		err = c.Input(client.OnInput{1, string(closePayload)})
		is.NoErr(err)

		<-done

		is.Equal(msg.Them, "alice@sour.is")

		err = closeSession()
		is.NoErr(err)
	}

	{ // Send salty
		sm, closeSession, err := session.NewSessionManager(path, "bob@sour.is", bob)
		is.NoErr(err)

		c, err := client.New(sm, "bob@sour.is")
		is.NoErr(err)
		is.True(c != nil)

		err = c.SendSalty(ctx, "alice@sour.is", "Hello, Alice.")
		is.NoErr(err)

		err = closeSession()
		is.NoErr(err)
	}

	saltyPayload, err := io.ReadAll(requests.reqs[12].Body)
	is.NoErr(err)

	{ // Receive salty
		sm, closeSession, err := session.NewSessionManager(path, "alice@sour.is", alice)
		is.NoErr(err)

		c, err := client.New(sm, "alice@sour.is")
		is.NoErr(err)
		is.True(c != nil)

		var msg client.OnSaltyTextReceived
		done := make(chan struct{})
		client.On(c, func(ctx context.Context, args client.OnSaltyTextReceived) { msg = args; close(done) })

		err = c.Input(client.OnInput{1, string(saltyPayload)})
		is.NoErr(err)

		<-done

		is.Equal(msg.Msg.User.String(), "alice@sour.is")
		is.Equal(msg.Msg.LiteralText(), "Hello, Alice.")

		err = closeSession()
		is.NoErr(err)
	}
}


var requests httpMock

type httpMock struct {
	fn   func(*http.Request) (*http.Response, error)
	reqs []*http.Request
}

func (m *httpMock) RoundTrip(r *http.Request) (*http.Response, error) {
	m.reqs = append(m.reqs, r)
	return m.fn(r)
}
