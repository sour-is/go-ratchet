package main

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/docopt/docopt-go"
	"github.com/golang-jwt/jwt/v4"

	"github.com/sour-is/xochimilco/cmd/ratchet/xdg"
)

var usage = `Rachet Chat.
usage:
  ratchet [options] [gen|jwt|offer|ack|send|recv|close]

Options:
  --to <to>        To key
  --key <key>      From key [default: ` + xdg.Get(xdg.EnvConfigHome, "rachet/$USER.key") + `]
  --from <user>    From acct name [default: $USER@$DOMAIN]
  --data <state>   Session state path [default: ` + xdg.Get(xdg.EnvDataHome, "rachet") + `]
  --force, -f      Force recreate key for gen
`

type opts struct {
	Gen   bool `docopt:"gen"`
	JWT   bool `docopt:"jwt"`
	Offer bool `docopt:"offer"`
	Ack   bool `docopt:"ack"`
	Send  bool `docopt:"send"`
	Recv  bool `docopt:"recv"`
	Close bool `docopt:"close"`

	Key   string `docopt:"--key"`
	From  string `docopt:"--from"`
	To    string `docopt:"--to"`
	Data  string `docopt:"--data"`
	Force bool   `docopt:"--force"`
}

func main() {
	o, err := docopt.ParseDoc(usage)
	if err != nil {
		fmt.Println(err)
		os.Exit(2)
	}

	var opts opts
	o.Bind(&opts)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, os.Kill)
	go func() {
		<-ctx.Done()
		defer cancel() // restore interrupt function
	}()

	if err := run(opts); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func run(opts opts) error {
	// fmt.Printf("%#v\n", opts)

	os.Setenv("DOMAIN", "sour.is")

	acct := os.ExpandEnv(opts.From)
	_ = acct

	switch {
	case opts.Gen:
		err := mkKeyfile(opts.Key, opts.Force)
		if err != nil {
			return err
		}
		fmt.Println("wrote keyfile to", opts.Key)
	case opts.JWT:
		key, err := readKeyfile(opts.Key)
		if err != nil {
			return err
		}
		b := []byte(key.Public().(ed25519.PublicKey))
		// fmt.Println(base64.RawURLEncoding.EncodeToString(key))
		token := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.MapClaims{
			"sub":     "acct:me@sour.is",
			"pub":     enc(b),
			"aliases": []string{"acct:xuu@sour.is"},
			"links": []map[string]any{
				{
					"rel":    "salty:xuu",
					"type":   "application/json+salty",
					"href":   "https://ev.sour.is/inbox/01GAEMKXYJ4857JQP1MJGD61Z5",
					"titles": map[string]string{"default": "Jon Lundy"},
					"properties": map[string]*string{
						"nick":    ptr("xuu"),
						"display": ptr("Jon Lundy"),
						"pub":     ptr("kex140fwaena9t0mrgnjeare5zuknmmvl0vc7agqy5yr938vusxfh9ys34vd2p"),
					},
				},

				{
					"rel": "https://txt.sour.is/user/xuu",
					"properties": map[string]*string{
						"https://sour.is/rel/redirect": ptr("https://txt.sour.is/.well-known/webfinger?resource=acct%3Axuu%40txt.sour.is"),
					},
				},

				{
					"rel": "http://joinmastodon.org#xuu%40chaos.social",
					"properties": map[string]*string{
						"https://sour.is/rel/redirect": ptr("https://chaos.social/.well-known/webfinger?resource=acct%3Axuu%40chaos.social"),
					},
				},
			},

			"exp": time.Now().Add(90 * time.Minute).Unix(),
			"iat": time.Now().Unix(),
			"aud": "webfinger",
			"iss": "sour.is-rachet",
		})
		aToken, err := token.SignedString(key)
		if err != nil {
			return err
		}

		fmt.Println("Token: ", aToken)

		token, err = jwt.Parse(
			aToken,
			func(t *jwt.Token) (any, error) {
				return key.Public(), nil
			},
			jwt.WithValidMethods([]string{"EdDSA"}),
			jwt.WithJSONNumber(),
		)
		if err != nil {
			return err
		}

		fmt.Println("valid: ", token.Valid)
		fmt.Println("valid: ", token.Claims)

	case opts.Offer:
		key, err := readKeyfile(opts.Key)
		if err != nil {
			return err
		}
		fmt.Println(key)

	case opts.Ack:
	case opts.Send:
	case opts.Recv:
	case opts.Close:
	}

	return nil
}

func enc(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
func dec(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	return base64.RawURLEncoding.DecodeString(s)
}

func mkKeyfile(keyfile string, force bool) error {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Dir(keyfile), 0700)
	if err != nil {
		return err
	}

	_, err = os.Stat(keyfile)
	if !os.IsNotExist(err) {
		if force {
			fmt.Println("removing keyfile", keyfile)
			err = os.Remove(keyfile)
			if err != nil {
				return err
			}
		} else {
			return fmt.Errorf("the keyfile %s exists. use --force", keyfile)
		}
	}

	fp, err := os.OpenFile(keyfile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	fmt.Fprint(fp, "# pub: ", enc(pub), "\n", enc(priv))

	return fp.Close()
}

func readKeyfile(keyfile string) (ed25519.PrivateKey, error) {
	fd, err := os.Stat(keyfile)
	if err != nil {
		return nil, err
	}

	if fd.Mode()&0066 != 0 {
		return nil, fmt.Errorf("permissions are too weak")
	}

	f, err := os.Open(keyfile)
	scan := bufio.NewScanner(f)

	var key ed25519.PrivateKey
	for scan.Scan() {
		txt := scan.Text()
		if strings.HasPrefix(txt, "#") {
			continue
		}
		if strings.TrimSpace(txt) == "" {
			continue
		}

		txt = strings.TrimPrefix(txt, "# priv: ")
		b, err := dec(txt)
		if err != nil {
			return nil, err
		}
		key = b
	}

	return key, err
}

func ptr[T any](v T) *T {
	return &v
}
