package command

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	origin = "https://peyk.example.run.app"
	rpID   = "peyk.example.run.app"
)

var now = time.Date(2026, 11, 14, 12, 0, 0, 0, time.UTC)

type signer struct {
	key  Key
	sign func(digest []byte) []byte
}

func ecSigner(t *testing.T, label string) signer {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	k, err := NewKey(label, "cred-"+label, base64.StdEncoding.EncodeToString(der), ES256, now)
	if err != nil {
		t.Fatal(err)
	}
	return signer{key: k, sign: func(d []byte) []byte {
		sig, _ := ecdsa.SignASN1(rand.Reader, priv, d)
		return sig
	}}
}

type assertionOpts struct {
	origin, rpID, typ string
	flags             byte
	challengeOver     string
}

// assert builds what navigator.credentials.get returns.
func (s signer) assert(payload string, o assertionOpts) Assertion {
	if o.origin == "" {
		o.origin = origin
	}
	if o.rpID == "" {
		o.rpID = rpID
	}
	if o.typ == "" {
		o.typ = "webauthn.get"
	}
	if o.flags == 0 {
		o.flags = 0x05
	}
	if o.challengeOver == "" {
		o.challengeOver = payload
	}
	ch := sha256.Sum256([]byte(o.challengeOver))
	client, _ := json.Marshal(map[string]any{"type": o.typ, "challenge": b64.EncodeToString(ch[:]), "origin": o.origin, "crossOrigin": false})
	rp := sha256.Sum256([]byte(o.rpID))
	auth := append(append([]byte{}, rp[:]...), o.flags, 0, 0, 0, 7)
	ch2 := sha256.Sum256(client)
	digest := sha256.Sum256(append(append([]byte{}, auth...), ch2[:]...))
	return Assertion{
		CredentialID:      s.key.CredentialID,
		AuthenticatorData: b64.EncodeToString(auth),
		ClientDataJSON:    b64.EncodeToString(client),
		Signature:         b64.EncodeToString(s.sign(digest[:])),
	}
}

func payload(id, typ string, expires time.Time) string {
	b, _ := json.Marshal(Payload{ID: id, Type: typ, Session: "s1", Text: "Add --json output", Expires: expires.UnixMilli()})
	return string(b)
}

func TestCheckAcceptsAGoodSignature(t *testing.T) {
	s := ecSigner(t, "pixel")
	p := payload("c1", Reply, now.Add(time.Minute))
	got, err := Check("c1", Command{Type: Reply, Payload: p, Assertion: ptr(s.assert(p, assertionOpts{}))}, []Key{s.key}, origin, rpID, now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Add --json output" || got.Session != "s1" {
		t.Fatalf("payload = %+v", got)
	}
}

func TestCheckRejectsForgeries(t *testing.T) {
	s := ecSigner(t, "pixel")
	other := ecSigner(t, "stranger")
	good := payload("c1", Dispatch, now.Add(time.Minute))
	tampered := strings.Replace(good, "Add --json output", "rm -rf ~", 1)
	tests := []struct {
		name string
		id   string
		cmd  Command
		want string
	}{
		{"unsigned", "c1", Command{Type: Dispatch, Payload: good}, "not signed"},
		{"payload changed after signing", "c1", Command{Type: Dispatch, Payload: tampered, Assertion: ptr(s.assert(good, assertionOpts{}))}, "not over this payload"},
		{"signed by an unpaired passkey", "c1", Command{Type: Dispatch, Payload: good, Assertion: ptr(other.assert(good, assertionOpts{}))}, "not paired"},
		{"credential id of a paired key, signed by another", "c1", Command{Type: Dispatch, Payload: good, Assertion: func() *Assertion {
			a := other.assert(good, assertionOpts{})
			a.CredentialID = s.key.CredentialID
			return &a
		}()}, "bad signature"},
		{"another origin", "c1", Command{Type: Dispatch, Payload: good, Assertion: ptr(s.assert(good, assertionOpts{origin: "https://evil.example"}))}, "not https://"},
		{"another relying party", "c1", Command{Type: Dispatch, Payload: good, Assertion: ptr(s.assert(good, assertionOpts{rpID: "evil.example"}))}, "another site"},
		{"user not verified", "c1", Command{Type: Dispatch, Payload: good, Assertion: ptr(s.assert(good, assertionOpts{flags: 0x01}))}, "verify the user"},
		{"registration ceremony", "c1", Command{Type: Dispatch, Payload: good, Assertion: ptr(s.assert(good, assertionOpts{typ: "webauthn.create"}))}, "client data type"},
		{"replayed into another document", "c2", Command{Type: Dispatch, Payload: good, Assertion: ptr(s.assert(good, assertionOpts{}))}, "another command"},
		{"type swapped", "c1", Command{Type: Stop, Payload: good, Assertion: ptr(s.assert(good, assertionOpts{}))}, "does not match"},
		{"expired", "c1", Command{Type: Dispatch, Payload: payload("c1", Dispatch, now.Add(-time.Second))}, "expired"},
		{"expires too late", "c1", Command{Type: Dispatch, Payload: payload("c1", Dispatch, now.Add(time.Hour))}, "too far"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Check(tt.id, tt.cmd, []Key{s.key}, origin, rpID, now)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestPushNeedsNoSignature(t *testing.T) {
	p := payload("c1", Push, now.Add(time.Minute))
	if _, err := Check("c1", Command{Type: Push, Payload: p}, nil, origin, rpID, now); err != nil {
		t.Fatal(err)
	}
}

func TestRSAKeys(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	k, err := NewKey("win", "cred-win", base64.StdEncoding.EncodeToString(der), RS256, now)
	if err != nil {
		t.Fatal(err)
	}
	s := signer{key: k, sign: func(d []byte) []byte {
		sig, _ := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, d)
		return sig
	}}
	p := payload("c1", Stop, now.Add(time.Minute))
	if _, err := Check("c1", Command{Type: Stop, Payload: p, Assertion: ptr(s.assert(p, assertionOpts{}))}, []Key{k}, origin, rpID, now); err != nil {
		t.Fatal(err)
	}
}

func TestNewKeyRejectsUnusableKeys(t *testing.T) {
	if _, err := NewKey("x", "c", "bm90IGEga2V5", ES256, now); err == nil {
		t.Fatal("garbage key accepted")
	}
	s := ecSigner(t, "p")
	if _, err := NewKey("x", "c", s.key.PublicKey, -8, now); err == nil {
		t.Fatal("unsupported algorithm accepted")
	}
}

func TestKeyringAddReplaceRemove(t *testing.T) {
	r := Keyring{Path: filepath.Join(t.TempDir(), "keys.json")}
	if keys, err := r.Load(); err != nil || len(keys) != 0 {
		t.Fatalf("empty keyring: %v %v", keys, err)
	}
	a, b := ecSigner(t, "pixel"), ecSigner(t, "mac")
	for _, k := range []Key{a.key, b.key, a.key} {
		if err := r.Add(k); err != nil {
			t.Fatal(err)
		}
	}
	keys, _ := r.Load()
	if len(keys) != 2 {
		t.Fatalf("keys = %+v", keys)
	}
	if err := r.Remove("pixel"); err != nil {
		t.Fatal(err)
	}
	if err := r.Remove("pixel"); err == nil {
		t.Fatal("removing a missing key should fail")
	}
	if keys, _ = r.Load(); len(keys) != 1 || keys[0].Label != "mac" {
		t.Fatalf("keys = %+v", keys)
	}
}

func TestFingerprintIsShortAndStable(t *testing.T) {
	s := ecSigner(t, "p")
	f := Fingerprint(s.key.PublicKey)
	if len(f) != 14 || f != Fingerprint(s.key.PublicKey) {
		t.Fatalf("fingerprint %q", f)
	}
}

func ptr[T any](v T) *T { return &v }

// The phone shows the same digits; tests/unit/web.spec.ts checks the phone's
// side against this value.
func TestFingerprintMatchesThePhone(t *testing.T) {
	if got := Fingerprint(base64.StdEncoding.EncodeToString([]byte("abc"))); got != "ba78 16bf 8f01" {
		t.Fatal(got)
	}
}

type session struct {
	priv  *ecdsa.PrivateKey
	grant Grant
}

// newSession does what the phone does once per grant: make a key and have
// the passkey sign a statement naming it.
func newSession(t *testing.T, s signer, st GrantStatement) session {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if st.Kind == "" {
		st.Kind = GrantKind
	}
	if st.Key == "" {
		der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
		st.Key = base64.StdEncoding.EncodeToString(der)
	}
	if st.Expires == 0 {
		st.Expires = now.Add(MaxGrant - time.Minute).UnixMilli()
	}
	b, _ := json.Marshal(st)
	return session{priv: priv, grant: Grant{Statement: string(b), Assertion: s.assert(string(b), assertionOpts{})}}
}

// sign produces WebCrypto's raw r||s signature.
func (ss session) sign(payload string) Grant {
	d := sha256.Sum256([]byte(payload))
	r, s, _ := ecdsa.Sign(rand.Reader, ss.priv, d[:])
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	g := ss.grant
	g.Signature = b64.EncodeToString(sig)
	return g
}

func TestCheckAcceptsASessionSignature(t *testing.T) {
	s := ecSigner(t, "pixel")
	ss := newSession(t, s, GrantStatement{})
	for _, id := range []string{"c1", "c2"} {
		p := payload(id, Dispatch, now.Add(time.Minute))
		if _, err := Check(id, Command{Type: Dispatch, Payload: p, Grant: ptr(ss.sign(p))}, []Key{s.key}, origin, rpID, now); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCheckRejectsBadSessions(t *testing.T) {
	s := ecSigner(t, "pixel")
	other := ecSigner(t, "stranger")
	good := payload("c1", Dispatch, now.Add(time.Minute))
	tampered := strings.Replace(good, "Add --json output", "rm -rf ~", 1)
	ss := newSession(t, s, GrantStatement{})
	swapped := ss.sign(good)
	swapped.Statement = newSession(t, s, GrantStatement{}).grant.Statement
	rsaDER := func() string {
		priv, _ := rsa.GenerateKey(rand.Reader, 2048)
		der, _ := x509.MarshalPKIXPublicKey(&priv.PublicKey)
		return base64.StdEncoding.EncodeToString(der)
	}()
	tests := []struct {
		name  string
		grant Grant
		want  string
	}{
		{"payload changed after signing", ss.sign(tampered), "bad session signature"},
		{"granted by an unpaired passkey", newSession(t, other, GrantStatement{}).sign(good), "not paired"},
		{"statement swapped after the passkey signed", swapped, "not over this payload"},
		{"expired", newSession(t, s, GrantStatement{Expires: now.Add(-time.Second).UnixMilli()}).sign(good), "expired"},
		{"too long", newSession(t, s, GrantStatement{Expires: now.Add(MaxGrant + time.Minute).UnixMilli()}).sign(good), "too long"},
		{"another kind of statement", newSession(t, s, GrantStatement{Kind: "other"}).sign(good), "not a session"},
		{"not a P-256 key", newSession(t, s, GrantStatement{Key: rsaDER}).sign(good), "not P-256"},
		{"command payload signed by the passkey as a statement", Grant{Statement: good, Assertion: s.assert(good, assertionOpts{}), Signature: ss.sign(good).Signature}, "not a session"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Check("c1", Command{Type: Dispatch, Payload: good, Grant: &tt.grant}, []Key{s.key}, origin, rpID, now)
			if err == nil || !strings.Contains(err.Error(), tt.want) || !strings.HasPrefix(err.Error(), grantRefused) {
				t.Fatalf("err = %v, want it to start %q and mention %q", err, grantRefused, tt.want)
			}
		})
	}
}
