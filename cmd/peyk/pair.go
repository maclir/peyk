package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"

	"github.com/maclir/peyk/internal/command"
	"github.com/maclir/peyk/internal/store"
)

func keyring() command.Keyring {
	home, _ := os.UserHomeDir()
	return command.Keyring{Path: filepath.Join(home, ".peyk", "keys.json")}
}

// origin is the phone app's address. Passkeys are bound to its host, so it
// must be the one address you open the app at.
func origin() (*url.URL, error) {
	o := os.Getenv("PEYK_ORIGIN")
	if o == "" {
		return nil, fmt.Errorf("PEYK_ORIGIN is not set; it is the phone app's address, in .env")
	}
	u, err := url.Parse(o)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" {
		return nil, fmt.Errorf("PEYK_ORIGIN must be https://host, got %q", o)
	}
	return u, nil
}

// pair trusts a new passkey. The code in the link ties the key the phone
// writes to this run, and you compare the fingerprint on both screens before
// the laptop trusts it.
func pair(ctx context.Context, label string, in io.Reader, out io.Writer) error {
	o, err := origin()
	if err != nil {
		return err
	}
	fs, _, err := openStore(ctx)
	if err != nil {
		return err
	}
	defer fs.Close()

	raw := make([]byte, 15)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	code := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw))
	link := o.String() + "/app/#pair-" + code
	fmt.Fprintf(out, "Open this on the device to pair as %q (valid 5 minutes):\n\n  %s\n\n", label, link)
	qrterminal.GenerateHalfBlock(link, qrterminal.L, out)

	wait, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	p, err := fs.Pairing(wait, code)
	if err != nil {
		return err
	}
	defer fs.Delete(context.Background(), store.Pairings, code)
	k, err := command.NewKey(label, p.CredentialID, p.PublicKey, p.Alg, time.Now())
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "\nThe device shows a fingerprint. Trust it only if it reads:\n\n  %s\n\nTrust this passkey? [y/N] ", command.Fingerprint(k.PublicKey))
	answer, _ := bufio.NewReader(in).ReadString('\n')
	if strings.ToLower(strings.TrimSpace(answer)) != "y" {
		return fmt.Errorf("not trusted; nothing saved")
	}
	if err := keyring().Add(k); err != nil {
		return err
	}
	fmt.Fprintf(out, "Paired %q. The running agent uses it from the next command.\n", label)
	return nil
}

func listKeys(out io.Writer) error {
	keys, err := keyring().Load()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		fmt.Fprintln(out, "No paired passkeys. Run: make pair LABEL=<device>")
	}
	for _, k := range keys {
		fmt.Fprintf(out, "%-16s %s  added %s\n", k.Label, command.Fingerprint(k.PublicKey), k.Added.Format("2006-01-02"))
	}
	return nil
}
