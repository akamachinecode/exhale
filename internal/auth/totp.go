package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // RFC 6238 uses HMAC-SHA1; every authenticator app supports it
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"hash"
	"net/url"
	"strings"
	"time"
)

// Time-based one-time passwords (RFC 6238 on RFC 4226), with the standard
// library only (ADR 0025): HMAC-SHA1, 6 digits, 30-second steps. These are
// the defaults every authenticator app understands.

const (
	totpPeriod = 30
	totpDigits = 6
	// totpSkew is how many steps before and after now are accepted, for
	// clocks that drift and people who type slowly.
	totpSkew = 1
)

var totpEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

// newTOTPSecret returns 20 random bytes (the size RFC 4226 recommends) in
// base32, the form authenticator apps take.
func newTOTPSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return totpEnc.EncodeToString(b)
}

func decodeTOTPSecret(s string) ([]byte, bool) {
	b, err := totpEnc.DecodeString(strings.ToUpper(strings.ReplaceAll(s, " ", "")))
	return b, err == nil && len(b) > 0
}

// hotp is RFC 4226 section 5.3, with the hash and digits as parameters so
// the RFC 6238 test vectors (SHA-256, SHA-512, 8 digits) can run too.
func hotp(key []byte, counter uint64, digits int, h func() hash.Hash) string {
	mac := hmac.New(h, key)
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	mac.Write(msg[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	mod := uint32(1)
	for range digits {
		mod *= 10
	}
	s := make([]byte, digits)
	v := bin % mod
	for i := digits - 1; i >= 0; i-- {
		s[i] = byte('0' + v%10)
		v /= 10
	}
	return string(s)
}

// totpStep is the time step of t.
func totpStep(t time.Time) uint64 { return uint64(t.Unix()) / totpPeriod } //nolint:gosec // times after 1970

// checkTOTP returns the time step code belongs to, if it is a valid code for
// now ± totpSkew steps and newer than after (so a code works only once).
func checkTOTP(secret, code string, now time.Time, after uint64) (uint64, bool) {
	key, ok := decodeTOTPSecret(secret)
	if !ok || len(code) != totpDigits {
		return 0, false
	}
	cur := totpStep(now)
	var found uint64
	match := 0
	for d := -totpSkew; d <= totpSkew; d++ {
		step := cur + uint64(int64(d)) //nolint:gosec // wraps only before 1970
		want := hotp(key, step, totpDigits, sha1.New)
		// Check every candidate, in constant time, whatever matches.
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 && step > after {
			found, match = step, 1
		}
	}
	return found, match == 1
}

// totpURI is the otpauth:// link an authenticator app reads from the QR code.
func totpURI(secret, account string) string {
	const issuer = "Exhale"
	q := url.Values{}
	q.Set("secret", secret)
	q.Set("issuer", issuer)
	q.Set("algorithm", "SHA1")
	q.Set("digits", "6")
	q.Set("period", "30")
	label := url.PathEscape(issuer + ":" + account)
	return "otpauth://totp/" + label + "?" + q.Encode()
}
