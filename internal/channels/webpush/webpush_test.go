package webpush

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/channels/webpush/pushtest"
)

func unb64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := b64.DecodeString(strings.Join(strings.Fields(s), ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// The example of RFC 8291, section 5 and appendix A.
func TestEncryptRFC8291Vector(t *testing.T) {
	asPrivate := unb64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw")
	local, err := ecdh.P256().NewPrivateKey(asPrivate)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := local.PublicKey().Bytes(), unb64(t, "BP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIg Dll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"); !bytes.Equal(got, want) {
		t.Fatalf("as_public = %x", got)
	}
	keys := Keys{
		P256dh: unb64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcx aOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"),
		Auth:   unb64(t, "BTBZMqHH6r4Tts7J_aSIgg"),
	}
	salt := unb64(t, "DGv6ra1nlYgDCS1FRnbzlw")
	remote, _ := ecdh.P256().NewPublicKey(keys.P256dh)

	cek, nonce, err := deriveKeys(keys.Auth, salt, local, remote)
	if err != nil {
		t.Fatal(err)
	}
	if want := unb64(t, "oIhVW04MRdy2XN9CiKLxTg"); !bytes.Equal(cek, want) {
		t.Errorf("CEK = %s, want %s", b64.EncodeToString(cek), b64.EncodeToString(want))
	}
	if want := unb64(t, "4h_95klXJ5E_qnoN"); !bytes.Equal(nonce, want) {
		t.Errorf("NONCE = %s, want %s", b64.EncodeToString(nonce), b64.EncodeToString(want))
	}

	got, err := encrypt([]byte("When I grow up, I want to be a watermelon"), keys, salt, local)
	if err != nil {
		t.Fatal(err)
	}
	// Section 5: the body of the request, 145 octets.
	want := unb64(t, `DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27ml
   mlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPT
   pK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN`)
	if !bytes.Equal(got, want) {
		t.Fatalf("message =\n%s\nwant\n%s", b64.EncodeToString(got), b64.EncodeToString(want))
	}
}

func TestEncryptRoundTripAndLimits(t *testing.T) {
	b := pushtest.NewBrowser()
	keys := Keys{P256dh: unb64(t, b.P256dh()), Auth: unb64(t, b.Auth())}
	msg := bytes.Repeat([]byte("é"), MaxPlaintext/2)
	body, err := Encrypt(msg, keys)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) > 4096 {
		t.Fatalf("body is %d octets", len(body))
	}
	plain, err := b.Decrypt(body)
	if err != nil || !bytes.Equal(plain, msg) {
		t.Fatalf("round trip: %v", err)
	}
	if _, err := Encrypt(make([]byte, MaxPlaintext+1), keys); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("too large: %v", err)
	}
	if _, err := Encrypt(msg, Keys{P256dh: keys.P256dh, Auth: []byte("short")}); err == nil {
		t.Fatal("a short auth secret was accepted")
	}
	if _, err := Encrypt(msg, Keys{P256dh: []byte{4, 1, 2}, Auth: keys.Auth}); err == nil {
		t.Fatal("a bad p256dh was accepted")
	}
}

func TestVAPIDHeader(t *testing.T) {
	v, err := GenerateVAPID()
	if err != nil {
		t.Fatal(err)
	}
	// Saved and read back, it is the same key.
	again, err := ParseVAPID(v.PrivateKey())
	if err != nil || again.PublicKey() != v.PublicKey() {
		t.Fatalf("parse: %v", err)
	}
	if n := len(unb64(t, v.PublicKey())); n != 65 {
		t.Fatalf("public key is %d octets, want 65 (uncompressed point)", n)
	}
	now := time.Now()
	h, err := again.Header("https://example.com/push/abc?x=1", "https://exhale.example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(h, ", k="+v.PublicKey()) {
		t.Fatalf("header %q does not end with the public key", h)
	}
	claims, err := pushtest.VerifyVAPID(h, "https://example.com", now)
	if err != nil {
		t.Fatal(err)
	}
	if claims["sub"] != "https://exhale.example.com" {
		t.Fatalf("claims %v", claims)
	}
	// Signed by another key, it fails.
	other, _ := GenerateVAPID()
	forged := strings.Replace(h, "k="+v.PublicKey(), "k="+other.PublicKey(), 1)
	if _, err := pushtest.VerifyVAPID(forged, "https://example.com", now); err == nil {
		t.Fatal("a header with another key verified")
	}
	if _, err := pushtest.VerifyVAPID(h, "https://fcm.googleapis.com", now); err == nil {
		t.Fatal("the audience was not checked")
	}
	if _, err := ParseVAPID("not-a-key"); err == nil {
		t.Fatal("a bad key parsed")
	}
}

func TestActionToken(t *testing.T) {
	key := []byte("0123456789abcdef0123456789abcdef")
	now := time.Now()
	a := Action{LinkID: "lnk_1", MemberID: "mem_1", ChargeID: "chg_1", Verb: "paid", Expires: now.Add(time.Hour)}
	tok := SignAction(key, a)
	got, err := VerifyAction(key, tok, now)
	if err != nil || got.ChargeID != "chg_1" || got.Verb != "paid" || got.MemberID != "mem_1" || got.LinkID != "lnk_1" {
		t.Fatalf("verify = %+v, %v", got, err)
	}
	if _, err := VerifyAction(key, tok, now.Add(2*time.Hour)); !errors.Is(err, ErrBadToken) {
		t.Fatalf("expired: %v", err)
	}
	if _, err := VerifyAction([]byte("another key"), tok, now); !errors.Is(err, ErrBadToken) {
		t.Fatalf("other key: %v", err)
	}
	// Changing the verb or the charge breaks the signature.
	p, sig, _ := strings.Cut(tok, ".")
	raw, _ := b64.DecodeString(p)
	for _, swap := range [][2]string{{"|paid|", "|skip|"}, {"|chg_1|", "|chg_2|"}} {
		forged := b64.EncodeToString([]byte(strings.Replace(string(raw), swap[0], swap[1], 1))) + "." + sig
		if _, err := VerifyAction(key, forged, now); !errors.Is(err, ErrBadToken) {
			t.Fatalf("forged %v: %v", swap, err)
		}
	}
	for _, bad := range []string{"", ".", "abc", tok + "x"} {
		if _, err := VerifyAction(key, bad, now); !errors.Is(err, ErrBadToken) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

var reminder = actions.Message{
	Title: "🔔 Netflix renews in 3 days",
	Lines: []string{"💶 12,99 € · Fri, 13 Nov 2026"},
	Rows: [][]actions.Button{
		{{Label: "✅ Already paid", Data: "paid|chg_1"}, {Label: "📅 Change date", Data: "dmenu|chg_1|2026-11"}},
		{{Label: "🔗 Open the cancel page", URL: "https://netflix.com/cancel"}},
	},
	Event: &actions.Event{Kind: actions.EventReminder, ChargeID: "chg_1", Path: "/subscriptions/sub_1"},
}

// fakeChannel is a channel against the fake push service with one device.
func fakeChannel(t *testing.T) (*Channel, *pushtest.Service, *pushtest.Browser, *[]string) {
	t.Helper()
	svc := pushtest.Start(t)
	b := pushtest.NewBrowser()
	vapid, _ := GenerateVAPID()
	key := []byte("action-key")
	var gone []string
	client := svc.Client()
	// The test certificate is for example.com: send there, reach the fake.
	tr := client.Transport.(*http.Transport).Clone()
	addr := svc.Listener.Addr().String()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	client.Transport = tr
	c := &Channel{
		Lookup: func(_ context.Context, endpoint string) (Device, error) {
			if endpoint != pushtest.Endpoint("phone") && endpoint != pushtest.Endpoint("old") {
				return Device{}, ErrNoDevice
			}
			return Device{LinkID: "lnk_1", MemberID: "mem_1", Keys: Keys{P256dh: unb64(t, b.P256dh()), Auth: unb64(t, b.Auth())}}, nil
		},
		Gone:    func(_ context.Context, endpoint string) { gone = append(gone, endpoint) },
		Keys:    func(context.Context) (VAPID, []byte, error) { return vapid, key, nil },
		Subject: "https://exhale.example.com",
		Client:  client,
	}
	return c, svc, b, &gone
}

func TestChannelSend(t *testing.T) {
	ctx := context.Background()
	c, svc, b, gone := fakeChannel(t)
	if _, err := c.Send(ctx, pushtest.Endpoint("phone"), reminder); err != nil {
		t.Fatal(err)
	}
	reqs := svc.Requests()
	if len(reqs) != 1 || reqs[0].Problem != "" {
		t.Fatalf("requests %+v", reqs)
	}
	if reqs[0].Header.Get("Urgency") != "normal" || reqs[0].Header.Get("TTL") != "259200" {
		t.Fatalf("headers %v", reqs[0].Header)
	}
	got := svc.Received(t, "phone", b)
	if len(got) != 1 {
		t.Fatalf("received %v", got)
	}
	p := got[0]
	if p["title"] != "🔔 Netflix renews in 3 days" || p["url"] != "/subscriptions/sub_1" || p["tag"] != "chg_1" || !strings.Contains(p["body"].(string), "12,99") {
		t.Fatalf("payload %v", p)
	}
	acts, _ := p["actions"].([]any)
	if len(acts) != 1 {
		t.Fatalf("actions %v", acts)
	}
	act := acts[0].(map[string]any)
	if act["action"] != "paid" || act["title"] != "✅ Already paid" {
		t.Fatalf("action %v", act)
	}
	a, err := VerifyAction([]byte("action-key"), act["token"].(string), time.Now())
	if err != nil || a.ChargeID != "chg_1" || a.Verb != "paid" || a.LinkID != "lnk_1" || a.MemberID != "mem_1" {
		t.Fatalf("token %+v %v", a, err)
	}
	if time.Until(a.Expires) > ActionTTL {
		t.Fatalf("token lives too long: %v", a.Expires)
	}

	// The push service forgot the device: it is removed, not retried.
	for _, status := range []int{http.StatusGone, http.StatusNotFound} {
		svc.Fail("/push/old", status)
		if _, err := c.Send(ctx, pushtest.Endpoint("old"), reminder); !errors.Is(err, actions.ErrGone) {
			t.Fatalf("%d: %v", status, err)
		}
	}
	if len(*gone) != 2 || (*gone)[0] != pushtest.Endpoint("old") {
		t.Fatalf("gone %v", *gone)
	}
	// Overloaded or broken: an error the outbox retries, the device stays.
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusForbidden} {
		svc.Fail("/push/phone", status)
		_, err := c.Send(ctx, pushtest.Endpoint("phone"), reminder)
		if err == nil || errors.Is(err, actions.ErrGone) || !strings.Contains(err.Error(), "example.com") {
			t.Fatalf("%d: %v", status, err)
		}
	}
	if len(*gone) != 2 {
		t.Fatalf("gone %v", *gone)
	}
	// An endpoint nobody registered is gone too.
	if _, err := c.Send(ctx, pushtest.Endpoint("nobody"), reminder); !errors.Is(err, actions.ErrGone) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestPayloadFits(t *testing.T) {
	m := reminder
	m.Lines = []string{strings.Repeat("Ñandú ", 2000)}
	p := BuildPayload(m, Device{LinkID: "l", MemberID: "m"}, []byte("k"), time.Now())
	raw, err := p.encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > MaxPlaintext {
		t.Fatalf("payload is %d octets", len(raw))
	}
	var back Payload
	if err := json.Unmarshal(raw, &back); err != nil || !strings.HasSuffix(back.Body, "…") || back.Title != m.Title || len(back.Actions) != 1 {
		t.Fatalf("payload %+v %v", back, err)
	}
	// A message with no paid button has no actions; a test has no charge.
	p = BuildPayload(actions.Message{Title: "x", Rows: [][]actions.Button{{{Label: "👍 Keep it", Data: "keep|chg_1"}}}}, Device{}, nil, time.Now())
	if len(p.Actions) != 0 || p.URL != "/" || p.Tag != "exhale" {
		t.Fatalf("payload %+v", p)
	}
}
