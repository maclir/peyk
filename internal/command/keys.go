package command

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Key is a paired passkey's public half.
type Key struct {
	Label        string    `json:"label"`
	CredentialID string    `json:"credential_id"`
	PublicKey    string    `json:"public_key"` // base64 SubjectPublicKeyInfo DER
	Alg          int       `json:"alg"`
	Added        time.Time `json:"added"`
}

// Fingerprint is what the phone and the laptop both show at pairing, so you
// can see the key the laptop is about to trust is the one your phone made.
func Fingerprint(publicKey string) string {
	der, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		return "invalid"
	}
	sum := sha256.Sum256(der)
	h := hex.EncodeToString(sum[:6])
	return h[0:4] + " " + h[4:8] + " " + h[8:12]
}

// NewKey checks a key received at pairing.
func NewKey(label, credentialID, publicKey string, alg int, now time.Time) (Key, error) {
	k := Key{Label: label, CredentialID: credentialID, PublicKey: publicKey, Alg: alg, Added: now}
	if label == "" || credentialID == "" {
		return k, errors.New("label and credential id are required")
	}
	if alg != ES256 && alg != RS256 {
		return k, fmt.Errorf("unsupported passkey algorithm %d", alg)
	}
	der, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil {
		return k, fmt.Errorf("public key: %w", err)
	}
	if _, err := x509.ParsePKIXPublicKey(der); err != nil {
		return k, fmt.Errorf("public key: %w", err)
	}
	return k, nil
}

// Keyring is the local file of trusted keys.
type Keyring struct{ Path string }

func (r Keyring) Load() ([]Key, error) {
	data, err := os.ReadFile(r.Path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var keys []Key
	if err := json.Unmarshal(data, &keys); err != nil {
		return nil, fmt.Errorf("%s: %w", r.Path, err)
	}
	return keys, nil
}

func (r Keyring) save(keys []Key) error {
	if err := os.MkdirAll(filepath.Dir(r.Path), 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(keys, "", "  ")
	tmp := r.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, r.Path)
}

// Add trusts a key, replacing one with the same label.
func (r Keyring) Add(k Key) error {
	keys, err := r.Load()
	if err != nil {
		return err
	}
	out := []Key{}
	for _, old := range keys {
		if old.Label != k.Label && old.CredentialID != k.CredentialID {
			out = append(out, old)
		}
	}
	return r.save(append(out, k))
}

// Remove stops trusting the key with this label.
func (r Keyring) Remove(label string) error {
	keys, err := r.Load()
	if err != nil {
		return err
	}
	out := []Key{}
	for _, k := range keys {
		if k.Label != label {
			out = append(out, k)
		}
	}
	if len(out) == len(keys) {
		return fmt.Errorf("no paired key named %q", label)
	}
	return r.save(out)
}
