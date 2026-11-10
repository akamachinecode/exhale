package notifiers

import (
	"context"
	"database/sql"
	"encoding/base64"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/channels/webpush"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Web Push (ADR 0026): one destination per browser or installed app. The
// instance's VAPID key pair and the key that signs action tokens are made
// the first time they are needed and kept in the settings table.

const (
	keyVAPID     = "webpush_vapid"      // base64url private key
	keyActionKey = "webpush_action_key" // base64url HMAC key for action tokens
)

// MaxDevices is how many browsers one member can turn push on in.
const MaxDevices = 10

// projectURL is the VAPID contact when the instance has no https address.
const projectURL = "https://github.com/akamachinecode/exhale"

// PushActions runs a notification button (the action layer implements it).
type PushActions interface {
	HandlePush(ctx context.Context, memberID, device, verb, chargeID string) (actions.PushResult, error)
}

// SetPushActions wires the action layer, which is built after the manager.
func (m *Manager) SetPushActions(a PushActions) {
	m.pushMu.Lock()
	defer m.pushMu.Unlock()
	m.pushActions = a
}

func (m *Manager) newPush() *webpush.Channel {
	client := m.guard.Client(15 * time.Second)
	if m.opts.PushRoots != nil {
		client.Transport.(*http.Transport).TLSClientConfig = &tls.Config{RootCAs: m.opts.PushRoots, MinVersion: tls.VersionTLS12}
	}
	subject := projectURL
	if strings.HasPrefix(m.publicURL, "https://") {
		subject = m.publicURL
	}
	return &webpush.Channel{
		Lookup:  m.device,
		Gone:    func(ctx context.Context, endpoint string) { _ = m.st.Q.DeletePushEndpoint(ctx, endpoint) },
		Keys:    m.pushKeys,
		Subject: subject,
		Client:  client,
	}
}

func (m *Manager) device(ctx context.Context, endpoint string) (webpush.Device, error) {
	row, err := m.st.Q.GetPushSubscription(ctx, endpoint)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return webpush.Device{}, webpush.ErrNoDevice
		}
		return webpush.Device{}, err
	}
	p256dh, err1 := b64(row.P256dh)
	auth, err2 := b64(row.Auth)
	if err := errors.Join(err1, err2); err != nil {
		return webpush.Device{}, err
	}
	return webpush.Device{LinkID: row.LinkID, MemberID: row.MemberID, Keys: webpush.Keys{P256dh: p256dh, Auth: auth}}, nil
}

// pushKeys loads the instance's keys, making them on first use.
func (m *Manager) pushKeys(ctx context.Context) (webpush.VAPID, []byte, error) {
	m.pushMu.Lock()
	defer m.pushMu.Unlock()
	if m.vapid != nil {
		return *m.vapid, m.actionKey, nil
	}
	var vapid webpush.VAPID
	var err error
	if raw := m.setting(ctx, keyVAPID); raw != "" {
		vapid, err = webpush.ParseVAPID(raw)
	} else {
		if vapid, err = webpush.GenerateVAPID(); err == nil {
			err = m.put(ctx, keyVAPID, vapid.PrivateKey())
			m.log.Info("web push keys created")
		}
	}
	if err != nil {
		return webpush.VAPID{}, nil, err
	}
	key, _ := b64(m.setting(ctx, keyActionKey))
	if len(key) < 32 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return webpush.VAPID{}, nil, err
		}
		if err := m.put(ctx, keyActionKey, encode64(key)); err != nil {
			return webpush.VAPID{}, nil, err
		}
	}
	m.vapid, m.actionKey = &vapid, key
	return vapid, key, nil
}

// PushStatus is what the web needs to turn push on.
type PushStatus struct {
	PublicKey string `json:"publicKey" doc:"VAPID public key, base64url: the applicationServerKey to subscribe with"`
}

// PushStatus returns the public key, making the keys if needed.
func (m *Manager) PushStatus(ctx context.Context) (PushStatus, error) {
	vapid, _, err := m.pushKeys(ctx)
	if err != nil {
		return PushStatus{}, err
	}
	return PushStatus{PublicKey: vapid.PublicKey()}, nil
}

// PushDevice is a browser's PushSubscription (its toJSON()) and a name to
// show for it.
type PushDevice struct {
	Endpoint string `json:"endpoint" doc:"the push service address from PushSubscription.endpoint"`
	P256dh   string `json:"p256dh" doc:"keys.p256dh, base64url"`
	Auth     string `json:"auth" doc:"keys.auth, base64url"`
	Device   string `json:"device,omitempty" doc:"how to show it, e.g. Chrome on Android"`
}

var webpushB64 = base64.RawURLEncoding

func encode64(b []byte) string { return webpushB64.EncodeToString(b) }

// b64 reads base64url with or without padding, as browsers send it.
func b64(s string) ([]byte, error) {
	return webpushB64.DecodeString(strings.TrimRight(strings.TrimSpace(s), "="))
}

// AddDevice saves a browser's subscription for a member and returns its
// link id. The same browser signed in as someone else moves to them.
func (m *Manager) AddDevice(ctx context.Context, memberID string, in PushDevice) (string, error) {
	endpoint := strings.TrimSpace(in.Endpoint)
	if len(endpoint) > 2048 || !strings.HasPrefix(endpoint, "https://") {
		return "", fmt.Errorf("%w: the push endpoint should be an https address", domain.ErrValidation)
	}
	if _, err := m.guard.CheckURL(ctx, endpoint); err != nil {
		return "", invalid(err)
	}
	p256dh, err := b64(in.P256dh)
	if err != nil || len(p256dh) != 65 || p256dh[0] != 4 {
		return "", fmt.Errorf("%w: keys.p256dh is not a P-256 public key", domain.ErrValidation)
	}
	auth, err := b64(in.Auth)
	if err != nil || len(auth) != 16 {
		return "", fmt.Errorf("%w: keys.auth should be 16 bytes", domain.ErrValidation)
	}
	// The keys must work before they are saved.
	if _, err := webpush.Encrypt([]byte("{}"), webpush.Keys{P256dh: p256dh, Auth: auth}); err != nil {
		return "", fmt.Errorf("%w: %s", domain.ErrValidation, err.Error())
	}
	name := strings.Join(strings.Fields(in.Device), " ")
	if utf8.RuneCountInString(name) > 60 {
		name = string([]rune(name)[:60])
	}
	q := m.st.Q
	links, err := q.ChannelLinksForMember(ctx, memberID)
	if err != nil {
		return "", err
	}
	n := 0
	for _, l := range links {
		if l.Channel == webpush.Name && l.ExternalID != endpoint {
			n++
		}
	}
	if n >= MaxDevices {
		return "", fmt.Errorf("%w: %d devices is the limit; remove one first", domain.ErrValidation, MaxDevices)
	}
	now := store.FormatTime(time.Now())
	// Upsert: a browser already linked keeps its link and moves to this
	// member (a shared computer).
	if err := q.LinkChannel(ctx, db.LinkChannelParams{ID: domain.NewID("lnk"), MemberID: memberID, Channel: webpush.Name, ExternalID: endpoint, CreatedAt: now}); err != nil {
		return "", err
	}
	link, err := q.PushLink(ctx, endpoint)
	if err != nil {
		return "", err
	}
	if err := q.SavePushSubscription(ctx, db.SavePushSubscriptionParams{LinkID: link.ID, P256dh: encode64(p256dh), Auth: encode64(auth), Device: name, CreatedAt: now}); err != nil {
		return "", err
	}
	return link.ID, nil
}

// RemoveDevice forgets a browser by its endpoint, if it is the member's
// (turning push off in that browser).
func (m *Manager) RemoveDevice(ctx context.Context, memberID, endpoint string) error {
	link, err := m.st.Q.PushLink(ctx, strings.TrimSpace(endpoint))
	if err != nil || link.MemberID != memberID {
		return nil // nothing of theirs to remove
	}
	return m.st.Q.DeletePushEndpoint(ctx, link.ExternalID)
}

// ErrActionExpired is returned for a notification button whose token is
// not valid any more; the web opens the charge instead.
var ErrActionExpired = webpush.ErrBadToken

// PushAction runs the button of a notification from its token. The device
// it was sent to must still be the member's.
func (m *Manager) PushAction(ctx context.Context, token string) (actions.PushResult, error) {
	_, key, err := m.pushKeys(ctx)
	if err != nil {
		return actions.PushResult{}, err
	}
	a, err := webpush.VerifyAction(key, strings.TrimSpace(token), time.Now())
	if err != nil {
		return actions.PushResult{}, err
	}
	links, err := m.st.Q.ChannelLinksForMember(ctx, a.MemberID)
	if err != nil {
		return actions.PushResult{}, err
	}
	found := false
	for _, l := range links {
		found = found || (l.ID == a.LinkID && l.Channel == webpush.Name)
	}
	if !found {
		return actions.PushResult{}, ErrActionExpired // the device was removed
	}
	m.pushMu.Lock()
	h := m.pushActions
	m.pushMu.Unlock()
	if h == nil {
		return actions.PushResult{}, errors.New("notification actions are not available")
	}
	return h.HandlePush(ctx, a.MemberID, a.LinkID, a.Verb, a.ChargeID)
}

// deviceName is how Settings shows a browser.
func (m *Manager) deviceName(ctx context.Context, endpoint string) string {
	row, err := m.st.Q.GetPushSubscription(ctx, endpoint)
	if err != nil {
		return ""
	}
	return row.Device
}
