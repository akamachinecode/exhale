package webpush

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// VAPID (RFC 8292): every request to a push service carries a short JWT
// signed with the instance's key pair. Browsers subscribe with the public
// key, so a push service only accepts messages for that subscription from
// whoever holds the private key.

var b64 = base64.RawURLEncoding

// VAPID is the instance's key pair. Generate it once and keep it: changing
// it invalidates every browser subscription.
type VAPID struct {
	key *ecdsa.PrivateKey
}

// GenerateVAPID makes a new key pair.
func GenerateVAPID() (VAPID, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return VAPID{}, err
	}
	return VAPID{key: k}, nil
}

// ParseVAPID reads a private key saved with PrivateKey.
func ParseVAPID(private string) (VAPID, error) {
	raw, err := b64.DecodeString(private)
	if err != nil {
		return VAPID{}, fmt.Errorf("vapid key: %w", err)
	}
	k, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), raw)
	if err != nil {
		return VAPID{}, fmt.Errorf("vapid key: %w", err)
	}
	return VAPID{key: k}, nil
}

// PrivateKey is the private scalar, base64url, for saving.
func (v VAPID) PrivateKey() string {
	raw, err := v.key.Bytes()
	if err != nil {
		return ""
	}
	return b64.EncodeToString(raw)
}

// PublicKey is the uncompressed public point, base64url: the
// applicationServerKey browsers subscribe with, and the k= of the header.
func (v VAPID) PublicKey() string {
	raw, err := v.key.PublicKey.Bytes()
	if err != nil {
		return ""
	}
	return b64.EncodeToString(raw)
}

// Audience is the origin of a push endpoint, the aud claim.
func Audience(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return "", errors.New("the push endpoint is not an http address")
	}
	return u.Scheme + "://" + u.Host, nil
}

// Header returns the Authorization header for a request to endpoint:
// `vapid t=<JWT>, k=<public key>`. subject is a mailto: or https: contact
// for the push service's operators.
func (v VAPID) Header(endpoint, subject string, now time.Time) (string, error) {
	aud, err := Audience(endpoint)
	if err != nil {
		return "", err
	}
	// 12 hours: under the 24 hours RFC 8292 allows, with room for clocks.
	claims := map[string]any{"aud": aud, "exp": now.Add(12 * time.Hour).Unix(), "sub": subject}
	jwt, err := v.sign(claims)
	if err != nil {
		return "", err
	}
	return "vapid t=" + jwt + ", k=" + v.PublicKey(), nil
}

func (v VAPID) sign(claims map[string]any) (string, error) {
	body, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	input := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`)) + "." + b64.EncodeToString(body)
	sum := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, v.key, sum[:])
	if err != nil {
		return "", err
	}
	// JWS wants r and s as two 32-octet big-endian numbers, not ASN.1.
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + b64.EncodeToString(sig), nil
}
