// Package pushtest is a fake Web Push service and a fake browser for tests.
// The service checks each request the way real push services do (VAPID
// signature and audience, content coding, TTL) and the browser decrypts
// what it received, so a test sees the notification a person would.
package pushtest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

var b64 = base64.RawURLEncoding

// Browser is one push subscription's private side.
type Browser struct {
	key  *ecdh.PrivateKey
	auth []byte
}

// NewBrowser makes fresh browser keys.
func NewBrowser() *Browser {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	return &Browser{key: k, auth: auth}
}

// P256dh and Auth are the keys of the browser's PushSubscription, base64url.
func (b *Browser) P256dh() string { return b64.EncodeToString(b.key.PublicKey().Bytes()) }
func (b *Browser) Auth() string   { return b64.EncodeToString(b.auth) }

// Decrypt reads an aes128gcm push message (RFC 8291, receiver side).
func (b *Browser) Decrypt(body []byte) ([]byte, error) {
	if len(body) < 21 {
		return nil, errors.New("too short")
	}
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if len(body) < 21+idlen || rs < 18 {
		return nil, errors.New("bad header")
	}
	asPublic, record := body[21:21+idlen], body[21+idlen:]
	if len(record) > int(rs) {
		return nil, errors.New("more than one record")
	}
	remote, err := ecdh.P256().NewPublicKey(asPublic)
	if err != nil {
		return nil, err
	}
	secret, err := b.key.ECDH(remote)
	if err != nil {
		return nil, err
	}
	info := append(append([]byte("WebPush: info\x00"), b.key.PublicKey().Bytes()...), asPublic...)
	ikm, err := hkdf.Key(sha256.New, secret, b.auth, string(info), 32)
	if err != nil {
		return nil, err
	}
	cek, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, nonce, record, nil)
	if err != nil {
		return nil, err
	}
	plain = []byte(strings.TrimRight(string(plain), "\x00"))
	if len(plain) == 0 || plain[len(plain)-1] != 0x02 {
		return nil, errors.New("missing last-record delimiter")
	}
	return plain[:len(plain)-1], nil
}

// VerifyVAPID checks an Authorization header (`vapid t=…, k=…`) for a
// request to origin and returns the JWT claims.
func VerifyVAPID(header, origin string, now time.Time) (map[string]any, error) {
	rest, ok := strings.CutPrefix(header, "vapid ")
	if !ok {
		return nil, errors.New("not a vapid header")
	}
	var jwt, k string
	for _, part := range strings.Split(rest, ",") {
		name, value, _ := strings.Cut(strings.TrimSpace(part), "=")
		switch name {
		case "t":
			jwt = value
		case "k":
			k = value
		}
	}
	public, err := b64.DecodeString(k)
	if err != nil {
		return nil, fmt.Errorf("k: %w", err)
	}
	pub, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), public)
	if err != nil {
		return nil, fmt.Errorf("k: %w", err)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		return nil, errors.New("t is not a JWT")
	}
	sig, err := b64.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		return nil, errors.New("bad signature encoding")
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(pub, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		return nil, errors.New("bad signature")
	}
	var head, claims map[string]any
	if raw, err := b64.DecodeString(parts[0]); err != nil || json.Unmarshal(raw, &head) != nil || head["alg"] != "ES256" || head["typ"] != "JWT" {
		return nil, errors.New("bad JWT header")
	}
	if raw, err := b64.DecodeString(parts[1]); err != nil || json.Unmarshal(raw, &claims) != nil {
		return nil, errors.New("bad JWT claims")
	}
	if claims["aud"] != origin {
		return nil, fmt.Errorf("aud is %v, want %s", claims["aud"], origin)
	}
	exp, _ := claims["exp"].(float64)
	if exp <= float64(now.Unix()) || exp > float64(now.Add(24*time.Hour).Unix()) {
		return nil, errors.New("exp must be in the next 24 hours")
	}
	if sub, _ := claims["sub"].(string); !strings.HasPrefix(sub, "mailto:") && !strings.HasPrefix(sub, "https:") {
		return nil, fmt.Errorf("sub %q is not a mailto: or https: contact", sub)
	}
	return claims, nil
}

// Request is one push received by the service.
type Request struct {
	Path    string
	Header  http.Header
	Body    []byte
	Problem string // why it was refused, "" if accepted
}

// Service is a fake push service over TLS. Endpoints are
// <URL>/push/<anything>.
type Service struct {
	*httptest.Server
	mu       sync.Mutex
	requests []Request
	status   map[string]int // path → forced status
}

// Start runs the service until the test ends.
func Start(t testing.TB) *Service {
	s := &Service{status: map[string]int{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Fail makes pushes to path answer status from now on (0 resets).
func (s *Service) Fail(path string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status[path] = status
}

// Requests returns what was received so far.
func (s *Service) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Origin is the audience the VAPID claims must name. The test TLS
// certificate is for example.com, so tests reach the service under that
// name.
const Origin = "https://example.com"

func (s *Service) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 8192))
	req := Request{Path: r.URL.Path, Header: r.Header.Clone(), Body: body}
	status := http.StatusCreated
	switch {
	case r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/push/"):
		req.Problem, status = "not a push endpoint", http.StatusNotFound
	case r.Header.Get("Content-Encoding") != "aes128gcm":
		req.Problem, status = "content coding is not aes128gcm", http.StatusBadRequest
	case r.Header.Get("TTL") == "":
		req.Problem, status = "no TTL", http.StatusBadRequest
	case len(body) > 4096:
		req.Problem, status = "payload too large", http.StatusRequestEntityTooLarge
	}
	if req.Problem == "" {
		if _, err := VerifyVAPID(r.Header.Get("Authorization"), Origin, time.Now()); err != nil {
			req.Problem, status = "vapid: "+err.Error(), http.StatusUnauthorized
		}
	}
	s.mu.Lock()
	if forced := s.status[r.URL.Path]; forced != 0 && req.Problem == "" {
		status, req.Problem = forced, http.StatusText(forced)
	}
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	w.WriteHeader(status)
}

// Endpoint is the URL of a new subscription on this service.
func Endpoint(name string) string { return Origin + "/push/" + name }

// Received decrypts the accepted pushes to name for browser b.
func (s *Service) Received(t testing.TB, name string, b *Browser) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, r := range s.Requests() {
		if r.Path != "/push/"+name || r.Problem != "" {
			continue
		}
		plain, err := b.Decrypt(r.Body)
		if err != nil {
			t.Fatalf("decrypt push to %s: %v", name, err)
		}
		var p map[string]any
		if err := json.Unmarshal(plain, &p); err != nil {
			t.Fatalf("push to %s is not JSON: %v", name, err)
		}
		out = append(out, p)
	}
	return out
}
