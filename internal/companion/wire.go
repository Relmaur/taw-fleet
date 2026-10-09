// Package companion talks to the TAW companion plugin on a live site: signed,
// read-only calls over the TAW-HUB-v1 wire protocol (taw-hub ADR-0003, frozen
// with the companion). Requests are signed with the fleet's Ed25519 key; every
// response is signed by the site's own key and checked against the key pinned
// for that site.
package companion

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Scheme is the first line of every canonical string.
const Scheme = "TAW-HUB-v1"

// Namespace is the companion's REST namespace.
const Namespace = "/wp-json/taw-hub/v1"

// MaxDrift is how far apart the clocks may be (the companion's fixed 60 s).
const MaxDrift = 60 * time.Second

// Header names.
const (
	HeaderAlgo      = "X-Taw-Hub-Algo"
	HeaderKeyID     = "X-Taw-Hub-Key-Id"
	HeaderTimestamp = "X-Taw-Hub-Timestamp"
	HeaderNonce     = "X-Taw-Hub-Nonce"
	HeaderSignature = "X-Taw-Hub-Signature"
)

// Canonical is the string that gets signed: six lines, no trailing newline.
// path has no host and no query string; method is upper case, or "RESPONSE".
func Canonical(method, path string, ts int64, nonce string, body []byte) string {
	sum := sha256.Sum256(body)
	return strings.Join([]string{Scheme, strings.ToUpper(method), path, strconv.FormatInt(ts, 10), nonce, hex.EncodeToString(sum[:])}, "\n")
}

// Key is the fleet's signing key.
type Key struct {
	ID      string
	Private ed25519.PrivateKey
}

// ParseKey reads a libsodium secret key (base64 of 64 bytes, seed + public
// key, as taw-hub's HUB_SIGNING_SECRET_KEY) or a bare 32-byte seed.
func ParseKey(id, b64 string) (Key, error) {
	raw, err := decodeB64(strings.TrimSpace(b64))
	if err != nil {
		return Key{}, fmt.Errorf("signing key: %w", err)
	}
	switch len(raw) {
	case ed25519.PrivateKeySize:
		priv := ed25519.PrivateKey(raw)
		if !priv.Equal(ed25519.NewKeyFromSeed(raw[:ed25519.SeedSize])) {
			return Key{}, errors.New("signing key: the public half doesn't match the seed")
		}
		return Key{ID: id, Private: priv}, nil
	case ed25519.SeedSize:
		return Key{ID: id, Private: ed25519.NewKeyFromSeed(raw)}, nil
	}
	return Key{}, fmt.Errorf("signing key: %d bytes, want 64 (libsodium secret key) or 32 (seed)", len(raw))
}

// GenerateKey makes a new random signing key.
func GenerateKey(id string) (Key, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return Key{}, err
	}
	return Key{ID: id, Private: priv}, nil
}

// Public is the key's public half, base64 (what a site's TAW_HUB_PUBLIC_KEY holds).
func (k Key) Public() string {
	return base64.StdEncoding.EncodeToString(k.Private.Public().(ed25519.PublicKey))
}

// Encode is the secret key as base64 (for storing in the Keychain).
func (k Key) Encode() string { return base64.StdEncoding.EncodeToString(k.Private) }

// Sign sets the five signature headers on h.
func (k Key) Sign(h http.Header, method, path string, ts int64, nonce string, body []byte) {
	sig := ed25519.Sign(k.Private, []byte(Canonical(method, path, ts, nonce, body)))
	h.Set(HeaderAlgo, "ed25519")
	h.Set(HeaderKeyID, k.ID)
	h.Set(HeaderTimestamp, strconv.FormatInt(ts, 10))
	h.Set(HeaderNonce, nonce)
	h.Set(HeaderSignature, base64.StdEncoding.EncodeToString(sig))
}

// NewNonce is 32 lowercase hex characters, like the Hub's.
func NewNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Errors from VerifyResponse.
var (
	ErrUnsigned     = errors.New("the response isn't signed")
	ErrBadSignature = errors.New("the response signature doesn't match the site's key")
	ErrKeyChanged   = errors.New("the site answered with a different key than the one pinned for it")
	ErrStale        = errors.New("the response timestamp is outside the allowed clock drift")
)

// SiteKey is a site's pinned public key.
type SiteKey struct {
	ID     string `json:"key_id"`
	Public string `json:"public_key"` // base64, 32 bytes
}

// VerifyResponse checks a response's signature: path is the request's signed
// path, body the raw response body.
func VerifyResponse(h http.Header, path string, body []byte, pinned SiteKey, now time.Time) error {
	algo, keyID, tsS, nonce, sigS := h.Get(HeaderAlgo), h.Get(HeaderKeyID), h.Get(HeaderTimestamp), h.Get(HeaderNonce), h.Get(HeaderSignature)
	if algo == "" || keyID == "" || tsS == "" || nonce == "" || sigS == "" {
		return ErrUnsigned
	}
	if algo != "ed25519" {
		return fmt.Errorf("%w (algorithm %q)", ErrBadSignature, algo)
	}
	if keyID != pinned.ID {
		return fmt.Errorf("%w (pinned %s, got %s)", ErrKeyChanged, pinned.ID, keyID)
	}
	pub, err := decodeB64(pinned.Public)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("pinned key for %s isn't a 32-byte base64 Ed25519 key", pinned.ID)
	}
	sig, err := decodeB64(sigS)
	if err != nil || len(sig) != ed25519.SignatureSize {
		return ErrBadSignature
	}
	ts, err := strconv.ParseInt(tsS, 10, 64)
	if err != nil {
		return ErrBadSignature
	}
	if d := now.Sub(time.Unix(ts, 0)); d > MaxDrift || d < -MaxDrift {
		return ErrStale
	}
	if !ed25519.Verify(pub, []byte(Canonical("RESPONSE", path, ts, nonce, body)), sig) {
		return ErrBadSignature
	}
	return nil
}

// decodeB64 accepts standard and URL-safe base64, padded or not.
func decodeB64(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	if strings.ContainsAny(s, "-_") {
		return base64.RawURLEncoding.DecodeString(s)
	}
	return base64.RawStdEncoding.DecodeString(s)
}
