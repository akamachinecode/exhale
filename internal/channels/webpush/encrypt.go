package webpush

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
)

// Message encryption for Web Push (RFC 8291) in the aes128gcm content
// coding (RFC 8188). A push message is one record: the browser's keys
// (p256dh, auth) and a fresh key pair of ours derive the key, so only that
// browser can read it and the push service only sees ciphertext.

// recordSize is the rs field of the header. The whole body must fit in one
// record; push services accept at most 4096 octets of payload.
const recordSize = 4096

// headerLen is salt (16) + rs (4) + idlen (1) + our public key (65).
const headerLen = 16 + 4 + 1 + 65

// MaxPlaintext is the longest message that still fits in one record: the
// record holds the plaintext, the padding delimiter and the 16-octet tag,
// and push services limit the body (header included) to 4096 octets.
const MaxPlaintext = recordSize - headerLen - 16 - 1

// ErrTooLarge is returned for a message longer than MaxPlaintext.
var ErrTooLarge = errors.New("push message too large")

// Keys are a browser's keys from its PushSubscription.
type Keys struct {
	P256dh []byte // uncompressed P-256 point, 65 octets
	Auth   []byte // authentication secret, 16 octets
}

// Encrypt encrypts plaintext for a browser with a fresh key pair and salt.
func Encrypt(plaintext []byte, keys Keys) ([]byte, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	local, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return encrypt(plaintext, keys, salt, local)
}

// encrypt is Encrypt with the salt and our key pair given (tests use the
// values of RFC 8291, appendix A).
func encrypt(plaintext []byte, keys Keys, salt []byte, local *ecdh.PrivateKey) ([]byte, error) {
	if len(plaintext) > MaxPlaintext {
		return nil, ErrTooLarge
	}
	if len(keys.Auth) != 16 {
		return nil, fmt.Errorf("the auth secret is %d octets, want 16", len(keys.Auth))
	}
	if len(salt) != 16 {
		return nil, fmt.Errorf("the salt is %d octets, want 16", len(salt))
	}
	remote, err := ecdh.P256().NewPublicKey(keys.P256dh)
	if err != nil {
		return nil, fmt.Errorf("the p256dh key is not a P-256 point: %w", err)
	}
	cek, nonce, err := deriveKeys(keys.Auth, salt, local, remote)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	asPublic := local.PublicKey().Bytes()
	out := make([]byte, 0, headerLen+len(plaintext)+1+gcm.Overhead())
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, recordSize)
	out = append(out, 65) // idlen: our uncompressed P-256 public key
	out = append(out, asPublic...)
	// One record, so it is the last: delimiter 0x02 and no padding.
	record := append(append(make([]byte, 0, len(plaintext)+1), plaintext...), 0x02)
	return gcm.Seal(out, nonce, record, nil), nil
}

// deriveKeys runs the key schedule of RFC 8291, section 3.4.
func deriveKeys(auth, salt []byte, local *ecdh.PrivateKey, remote *ecdh.PublicKey) (cek, nonce []byte, err error) {
	ecdhSecret, err := local.ECDH(remote)
	if err != nil {
		return nil, nil, err
	}
	keyInfo := append(append([]byte("WebPush: info\x00"), remote.Bytes()...), local.PublicKey().Bytes()...)
	ikm, err := hkdf.Key(sha256.New, ecdhSecret, auth, string(keyInfo), 32)
	if err != nil {
		return nil, nil, err
	}
	if cek, err = hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16); err != nil {
		return nil, nil, err
	}
	if nonce, err = hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12); err != nil {
		return nil, nil, err
	}
	return cek, nonce, nil
}
