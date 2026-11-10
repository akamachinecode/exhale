package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/notifiers"
	"github.com/akamachinecode/exhale/internal/channels/webpush/pushtest"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/testutil"
)

// TestPushDevicesAndActions: a person turns push on in a browser, gets a
// reminder there, and its "Already paid" button works from the service
// worker with no session, only with the token in the notification.
func TestPushDevicesAndActions(t *testing.T) {
	ctx := context.Background()
	st, err := store.OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	push := pushtest.Start(t)
	net := testutil.NewInternet().Host("example.com", push.Listener.Addr().String())
	channels, err := notifiers.New(st, notifiers.Options{
		PublicURL: "https://exhale.example.com", Version: "test", Guard: net.Guard(nil),
		PushRoots: push.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs,
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	svc := app.New(st, bus.New(), nil, func() time.Time { return time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC) })
	eng := actions.New(svc, slog.New(slog.DiscardHandler), channels.Channels()...)
	channels.SetPushActions(eng)
	h, _, err := New(svc, Config{Version: "test", Telegram: fakeTelegram{}, Channels: channels})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	owner := newBrowser(t, srv.URL)
	if code, s := owner.do("POST", "/api/v1/auth/setup", alex); code != 200 {
		t.Fatalf("setup: %d %v", code, s)
	}
	anon := newBrowser(t, srv.URL)
	if code, _ := anon.do("GET", "/api/v1/push", nil); code != 401 {
		t.Fatalf("signed out push status = %d", code)
	}
	code, status := owner.do("GET", "/api/v1/push", nil)
	if code != 200 || len(status["publicKey"].(string)) != 87 {
		t.Fatalf("push status = %d %v", code, status)
	}

	phone := pushtest.NewBrowser()
	endpoint := pushtest.Endpoint("phone-secret-path")
	dev := map[string]string{"endpoint": endpoint, "p256dh": phone.P256dh(), "auth": phone.Auth(), "device": "Chrome on Android"}
	if code, _ := anon.do("POST", "/api/v1/push/devices", dev); code != 401 {
		t.Fatalf("signed out add = %d", code)
	}
	code, link := owner.do("POST", "/api/v1/push/devices", dev)
	if code != 200 || link["channel"] != "webpush" || link["destination"] != "Chrome on Android" {
		t.Fatalf("add device = %d %v", code, link)
	}
	if code, found := owner.do("POST", "/api/v1/push/devices/find", map[string]string{"endpoint": endpoint}); code != 200 || found["id"] != link["id"] {
		t.Fatalf("find = %d %v", code, found)
	}
	if _, found := owner.do("POST", "/api/v1/push/devices/find", map[string]string{"endpoint": pushtest.Endpoint("other")}); found["id"] != nil {
		t.Fatalf("found a device that is not there: %v", found)
	}
	// The endpoint is a secret: listing devices never returns it.
	_, links := owner.do("GET", "/api/v1/auth/links", nil)
	if s := text(links); !strings.Contains(s, "Chrome on Android") || strings.Contains(s, "phone-secret-path") {
		t.Fatalf("links = %s", s)
	}
	if code, out := owner.do("POST", "/api/v1/auth/links/test", map[string]string{"id": link["id"].(string)}); code != 200 {
		t.Fatalf("test = %d %v", code, out)
	}
	if got := push.Received(t, "phone-secret-path", phone); len(got) != 1 || got[0]["title"] != "🔔 Test reminder from Exhale" {
		t.Fatalf("test push = %v", got)
	}

	// A reminder, delivered by the action layer.
	actor, _ := svc.Owner(ctx)
	sub, err := svc.CreateSubscription(ctx, actor, app.CreateSubscriptionInput{Name: "Netflix", Price: "12.99", Currency: "EUR", Interval: 1, Unit: "month", FirstCharge: "2026-11-12"})
	if err != nil {
		t.Fatal(err)
	}
	charge := sub.Upcoming[0].ID
	if err := eng.Deliver(ctx, app.OutboxItem{MemberID: actor.MemberID, ChargeID: charge, Kind: app.KindReminder}); err != nil {
		t.Fatal(err)
	}
	got := push.Received(t, "phone-secret-path", phone)
	if len(got) != 2 {
		t.Fatalf("pushes = %v", got)
	}
	token := got[1]["actions"].([]any)[0].(map[string]any)["token"].(string)

	// Wrong or forged tokens do nothing; a cross-site page cannot post one.
	if code, _ := anon.do("POST", "/api/v1/push/action", map[string]string{"token": "nope"}); code != 401 {
		t.Fatalf("bad token = %d", code)
	}
	evil := newBrowser(t, srv.URL)
	evil.hdr.Set("Sec-Fetch-Site", "cross-site")
	if code, _ := evil.do("POST", "/api/v1/push/action", map[string]string{"token": token}); code != 403 {
		t.Fatalf("cross-site = %d", code)
	}
	// The service worker, with no cookie: the charge is paid (this
	// household speaks Spanish).
	code, out := anon.do("POST", "/api/v1/push/action", map[string]string{"token": token})
	if code != 200 || out["message"] != "Marcado como pagado ✅" || out["title"] != "✅ Netflix pagado" {
		t.Fatalf("action = %d %v", code, out)
	}
	list, _ := svc.ListCharges(ctx, actor, app.ListChargesInput{From: "2026-11-12", To: "2026-11-12"})
	if len(list.Charges) != 1 || list.Charges[0].Status != domain.ChargePaid {
		t.Fatalf("charges = %+v", list.Charges)
	}

	// Turning push off in the browser removes the device and its buttons.
	if code, _ := owner.do("POST", "/api/v1/push/devices/remove", map[string]string{"endpoint": endpoint}); code != 200 {
		t.Fatalf("remove = %d", code)
	}
	if code, _ := anon.do("POST", "/api/v1/push/action", map[string]string{"token": token}); code != 401 {
		t.Fatalf("token of a removed device = %d", code)
	}
}
