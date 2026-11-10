package webpush

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Action tokens let a notification's buttons work with no tab open and no
// session: the service worker posts the token, and the token alone says
// who may do what. Each one is scoped to one device, one charge and one
// verb, and expires.

// ActionTTL is how long a notification's buttons keep working. After that
// the service worker opens the charge in the web instead.
const ActionTTL = 72 * time.Hour

// ErrBadToken means the token is not one of ours, or has expired.
var ErrBadToken = errors.New("this button has expired")

// Action is what a token allows.
type Action struct {
	LinkID   string // the device (channel_links.id) the notification went to
	MemberID string
	ChargeID string
	Verb     string // button verb of the action layer, e.g. "paid"
	Expires  time.Time
}

const tokenVersion = "a1"

// SignAction returns a token for a, signed with key.
func SignAction(key []byte, a Action) string {
	payload := strings.Join([]string{tokenVersion, a.LinkID, a.MemberID, a.ChargeID, a.Verb, strconv.FormatInt(a.Expires.Unix(), 10)}, "|")
	p := b64.EncodeToString([]byte(payload))
	return p + "." + b64.EncodeToString(mac(key, p))
}

// VerifyAction checks a token and returns what it allows.
func VerifyAction(key []byte, token string, now time.Time) (Action, error) {
	p, sig, ok := strings.Cut(token, ".")
	if !ok || len(token) > 1024 {
		return Action{}, ErrBadToken
	}
	got, err := b64.DecodeString(sig)
	if err != nil || !hmac.Equal(got, mac(key, p)) {
		return Action{}, ErrBadToken
	}
	raw, err := b64.DecodeString(p)
	if err != nil {
		return Action{}, ErrBadToken
	}
	f := strings.Split(string(raw), "|")
	if len(f) != 6 || f[0] != tokenVersion {
		return Action{}, ErrBadToken
	}
	exp, err := strconv.ParseInt(f[5], 10, 64)
	if err != nil || now.Unix() > exp {
		return Action{}, ErrBadToken
	}
	return Action{LinkID: f[1], MemberID: f[2], ChargeID: f[3], Verb: f[4], Expires: time.Unix(exp, 0)}, nil
}

func mac(key []byte, payload string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte("exhale push action\x00"))
	h.Write([]byte(payload))
	return h.Sum(nil)
}
