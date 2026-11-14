// Package command defines what the phone asks the laptop to do and checks
// that the phone's passkey signed it.
//
// The phone signs the exact payload string it stores: the WebAuthn challenge
// is SHA-256 of that string. The laptop verifies the assertion against public
// keys it was given at pairing and keeps in a local file, so neither Firestore
// nor the Google account can forge a command.
package command

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	Dispatch = "dispatch"
	Reply    = "reply"
	Stop     = "stop"
	Push     = "push"
)

const (
	Pending = "pending"
	Done    = "done"
	Failed  = "failed"
)

// Signed says whether a command type needs a passkey signature. Push sends
// commits that are already made to the remote they track, so it does not.
func Signed(typ string) bool { return typ != Push }

// MaxAge bounds how far ahead a payload may expire, so a signed command
// cannot be held back and replayed much later.
const MaxAge = 2 * time.Minute

// Command is the Firestore document.
type Command struct {
	Type       string     `firestore:"type"`
	Payload    string     `firestore:"payload"`
	Assertion  *Assertion `firestore:"assertion,omitempty"`
	Status     string     `firestore:"status"`
	Error      string     `firestore:"error,omitempty"`
	CreatedAt  time.Time  `firestore:"created_at"`
	FinishedAt time.Time  `firestore:"finished_at,omitempty"`
}

// Payload is what the phone signs. ID is the command document's id, so a
// signed payload is good for exactly one document, and documents cannot be
// recreated once they exist.
type Payload struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Session string `json:"session,omitempty"`
	Project string `json:"project,omitempty"`
	Task    string `json:"task,omitempty"`
	Text    string `json:"text,omitempty"`
	// Expires is Unix milliseconds.
	Expires int64 `json:"expires"`
}

// Assertion is a WebAuthn assertion, each field base64url without padding.
type Assertion struct {
	CredentialID      string `firestore:"credential_id"`
	AuthenticatorData string `firestore:"authenticator_data"`
	ClientDataJSON    string `firestore:"client_data_json"`
	Signature         string `firestore:"signature"`
}

// Check parses a command and, for signed types, verifies its signature. The
// returned payload is safe to act on.
func Check(id string, c Command, keys []Key, origin, rpID string, now time.Time) (Payload, error) {
	var p Payload
	if err := json.Unmarshal([]byte(c.Payload), &p); err != nil {
		return p, fmt.Errorf("unreadable payload: %w", err)
	}
	if p.ID != id {
		return p, errors.New("payload was signed for another command")
	}
	if p.Type != c.Type {
		return p, errors.New("payload type does not match the command")
	}
	exp := time.UnixMilli(p.Expires)
	if !now.Before(exp) {
		return p, errors.New("command expired before the laptop saw it")
	}
	if exp.Sub(now) > MaxAge {
		return p, errors.New("command expires too far in the future")
	}
	if !Signed(c.Type) {
		return p, nil
	}
	if c.Assertion == nil {
		return p, errors.New("command is not signed")
	}
	if err := Verify(keys, c.Payload, *c.Assertion, origin, rpID); err != nil {
		return p, err
	}
	return p, nil
}

var b64 = base64.RawURLEncoding

// Verify checks a WebAuthn assertion over payload.
func Verify(keys []Key, payload string, a Assertion, origin, rpID string) error {
	var key *Key
	for i := range keys {
		if keys[i].CredentialID == a.CredentialID {
			key = &keys[i]
		}
	}
	if key == nil {
		return errors.New("signed by a passkey this laptop has not paired")
	}
	authData, err := b64.DecodeString(a.AuthenticatorData)
	if err != nil {
		return fmt.Errorf("authenticator data: %w", err)
	}
	clientJSON, err := b64.DecodeString(a.ClientDataJSON)
	if err != nil {
		return fmt.Errorf("client data: %w", err)
	}
	sig, err := b64.DecodeString(a.Signature)
	if err != nil {
		return fmt.Errorf("signature: %w", err)
	}

	var client struct {
		Type      string `json:"type"`
		Challenge string `json:"challenge"`
		Origin    string `json:"origin"`
	}
	if err := json.Unmarshal(clientJSON, &client); err != nil {
		return fmt.Errorf("client data: %w", err)
	}
	if client.Type != "webauthn.get" {
		return fmt.Errorf("client data type %q", client.Type)
	}
	want := sha256.Sum256([]byte(payload))
	if got, err := b64.DecodeString(client.Challenge); err != nil || !bytes.Equal(got, want[:]) {
		return errors.New("signature is not over this payload")
	}
	if client.Origin != origin {
		return fmt.Errorf("signed on %s, not %s", client.Origin, origin)
	}

	if len(authData) < 37 {
		return errors.New("authenticator data too short")
	}
	rp := sha256.Sum256([]byte(rpID))
	if !bytes.Equal(authData[:32], rp[:]) {
		return errors.New("signed for another site")
	}
	const userPresent, userVerified = 0x01, 0x04
	if authData[32]&userPresent == 0 || authData[32]&userVerified == 0 {
		return errors.New("passkey did not verify the user")
	}

	clientHash := sha256.Sum256(clientJSON)
	digest := sha256.Sum256(append(append([]byte{}, authData...), clientHash[:]...))
	pub, err := key.publicKey()
	if err != nil {
		return err
	}
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		if key.Alg != ES256 || !ecdsa.VerifyASN1(k, digest[:], sig) {
			return errors.New("bad signature")
		}
	case *rsa.PublicKey:
		if key.Alg != RS256 || rsa.VerifyPKCS1v15(k, crypto.SHA256, digest[:], sig) != nil {
			return errors.New("bad signature")
		}
	default:
		return fmt.Errorf("unsupported key type %T", pub)
	}
	return nil
}

// COSE algorithm identifiers, as navigator.credentials reports them.
const (
	ES256 = -7
	RS256 = -257
)

func (k Key) publicKey() (any, error) {
	der, err := base64.StdEncoding.DecodeString(k.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("stored key %s: %w", k.Label, err)
	}
	pub, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, fmt.Errorf("stored key %s: %w", k.Label, err)
	}
	return pub, nil
}
