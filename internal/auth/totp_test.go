package auth

import (
	"crypto/sha1" //nolint:gosec // RFC 6238 test vectors
	"crypto/sha256"
	"crypto/sha512"
	"hash"
	"testing"
	"time"
)

// TestTOTPVectors runs the test vectors of RFC 6238 appendix B (8 digits,
// 30-second steps, the seeds the RFC gives for each hash).
func TestTOTPVectors(t *testing.T) {
	seeds := map[string][]byte{
		"SHA1":   []byte("12345678901234567890"),
		"SHA256": []byte("12345678901234567890123456789012"),
		"SHA512": []byte("1234567890123456789012345678901234567890123456789012345678901234"),
	}
	hashes := map[string]func() hash.Hash{"SHA1": sha1.New, "SHA256": sha256.New, "SHA512": sha512.New}
	vectors := []struct {
		unix int64
		algo string
		want string
	}{
		{59, "SHA1", "94287082"}, {59, "SHA256", "46119246"}, {59, "SHA512", "90693936"},
		{1111111109, "SHA1", "07081804"}, {1111111109, "SHA256", "68084774"}, {1111111109, "SHA512", "25091201"},
		{1111111111, "SHA1", "14050471"}, {1111111111, "SHA256", "67062674"}, {1111111111, "SHA512", "99943326"},
		{1234567890, "SHA1", "89005924"}, {1234567890, "SHA256", "91819424"}, {1234567890, "SHA512", "93441116"},
		{2000000000, "SHA1", "69279037"}, {2000000000, "SHA256", "90698825"}, {2000000000, "SHA512", "38618901"},
		{20000000000, "SHA1", "65353130"}, {20000000000, "SHA256", "77737706"}, {20000000000, "SHA512", "47863826"},
	}
	for _, v := range vectors {
		got := hotp(seeds[v.algo], totpStep(time.Unix(v.unix, 0)), 8, hashes[v.algo])
		if got != v.want {
			t.Errorf("%s at %d: got %s, want %s", v.algo, v.unix, got, v.want)
		}
	}
	// RFC 4226 appendix D: HOTP, 6 digits.
	for i, want := range []string{"755224", "287082", "359152", "969429", "338314", "254676", "287922", "162583", "399871", "520489"} {
		if got := hotp(seeds["SHA1"], uint64(i), 6, sha1.New); got != want { //nolint:gosec // small i
			t.Errorf("HOTP counter %d: got %s, want %s", i, got, want)
		}
	}
}

func TestCheckTOTP(t *testing.T) {
	secret := newTOTPSecret()
	key, _ := decodeTOTPSecret(secret)
	now := time.Unix(1_800_000_015, 0)
	step := totpStep(now)
	code := func(s uint64) string { return hotp(key, s, totpDigits, sha1.New) }

	for _, d := range []int64{-1, 0, 1} {
		s := uint64(int64(step) + d) //nolint:gosec // test values
		if got, ok := checkTOTP(secret, code(s), now, 0); !ok || got != s {
			t.Errorf("step %+d: got %d %v", d, got, ok)
		}
	}
	for _, d := range []int64{-2, 2} {
		if _, ok := checkTOTP(secret, code(uint64(int64(step)+d)), now, 0); ok { //nolint:gosec // test values
			t.Errorf("step %+d must be refused", d)
		}
	}
	if _, ok := checkTOTP(secret, code(step), now, step); ok {
		t.Error("a code of a step already used must be refused")
	}
	if _, ok := checkTOTP(secret, code(step-1), now, step); ok {
		t.Error("a code older than the last one used must be refused")
	}
	if _, ok := checkTOTP(secret, "12345", now, 0); ok {
		t.Error("5 digits accepted")
	}
	if _, ok := checkTOTP("not base32!", code(step), now, 0); ok {
		t.Error("a broken secret accepted")
	}
}

func TestTOTPURI(t *testing.T) {
	got := totpURI("JBSWY3DPEHPK3PXP", "alex@example.com")
	want := "otpauth://totp/Exhale:alex@example.com?algorithm=SHA1&digits=6&issuer=Exhale&period=30&secret=JBSWY3DPEHPK3PXP"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestNormalizeCode(t *testing.T) {
	for in, want := range map[string]string{" 123 456 ": "123456", "ABCDE-fghij": "abcdefghij", "abcde fghij\t": "abcdefghij"} {
		if got := normalizeCode(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
