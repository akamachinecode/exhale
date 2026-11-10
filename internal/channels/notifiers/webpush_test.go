package notifiers_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/channels/notifiers"
	"github.com/akamachinecode/exhale/internal/channels/webpush/pushtest"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/scheduler"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/testutil"
)

// pushWorld is a store and a fake push service reachable as example.com
// (the name on its test certificate).
func pushWorld(t *testing.T) (*store.Store, *pushtest.Service, notifiers.Options) {
	t.Helper()
	st, err := store.OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	push := pushtest.Start(t)
	net := testutil.NewInternet().
		Host("example.com", push.Listener.Addr().String()).
		PrivateHost("sneaky.test", "127.0.0.1")
	return st, push, notifiers.Options{PublicURL: "https://exhale.example.com", Version: "test", Guard: net.Guard(nil), PushRoots: push.Client().Transport.(*http.Transport).TLSClientConfig.RootCAs}
}

func device(endpoint string, b *pushtest.Browser, name string) notifiers.PushDevice {
	return notifiers.PushDevice{Endpoint: endpoint, P256dh: b.P256dh(), Auth: b.Auth(), Device: name}
}

func TestPushKeysAreKept(t *testing.T) {
	ctx := context.Background()
	st, _, opts := pushWorld(t)
	m, _ := notifiers.New(st, opts, slog.New(slog.DiscardHandler))
	first, err := m.PushStatus(ctx)
	if err != nil || len(first.PublicKey) != 87 { // 65 octets, base64url
		t.Fatalf("status %+v %v", first, err)
	}
	again, _ := notifiers.New(st, opts, slog.New(slog.DiscardHandler))
	if second, _ := again.PushStatus(ctx); second.PublicKey != first.PublicKey {
		t.Fatal("the VAPID key changed on restart; every browser would have to subscribe again")
	}
}

func TestPushDevices(t *testing.T) {
	ctx := context.Background()
	st, push, opts := pushWorld(t)
	m, _ := notifiers.New(st, opts, slog.New(slog.DiscardHandler))
	svc := app.New(st, bus.New(), nil, time.Now)
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	b := pushtest.NewBrowser()
	for _, bad := range []notifiers.PushDevice{
		device("http://example.com/push/a", b, ""),                        // not https
		device("https://sneaky.test/push/a", b, ""),                       // this machine
		{Endpoint: pushtest.Endpoint("a"), P256dh: "abc", Auth: b.Auth()}, // not a key
		{Endpoint: pushtest.Endpoint("a"), P256dh: b.P256dh(), Auth: "c2hvcnQ"},
	} {
		if _, err := m.AddDevice(ctx, owner.MemberID, bad); !errors.Is(err, domain.ErrValidation) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	id, err := m.AddDevice(ctx, owner.MemberID, device(pushtest.Endpoint("phone"), b, "  Chrome   on Android "))
	if err != nil {
		t.Fatal(err)
	}
	// Subscribing again keeps the same device.
	if again, err := m.AddDevice(ctx, owner.MemberID, device(pushtest.Endpoint("phone"), b, "Chrome on Android")); err != nil || again != id {
		t.Fatalf("again = %s, %v", again, err)
	}
	links, _ := auth.ListLinks(ctx, st, owner.MemberID)
	if len(links) != 1 || links[0].Channel != "webpush" {
		t.Fatalf("links %+v", links)
	}
	if name, _ := m.Describe(ctx, "webpush", links[0].ExternalID); name != "Chrome on Android" {
		t.Fatalf("described as %q", name)
	}
	// A test goes through the push service and decrypts in the browser.
	if err := m.Test(ctx, "webpush", pushtest.Endpoint("phone")); err != nil {
		t.Fatal(err)
	}
	got := push.Received(t, "phone", b)
	if len(got) != 1 || got[0]["title"] != "🔔 Test reminder from Exhale" || got[0]["url"] != "/settings" {
		t.Fatalf("received %v", got)
	}
	// The browser unsubscribed: the test says so and the device is gone.
	push.Fail("/push/phone", http.StatusGone)
	if err := m.Test(ctx, "webpush", pushtest.Endpoint("phone")); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("test to a gone device: %v", err)
	}
	if links, _ := auth.ListLinks(ctx, st, owner.MemberID); len(links) != 0 {
		t.Fatalf("a gone device stayed: %+v", links)
	}
	// Turning it off from the browser removes it; others' endpoints are not touched.
	if _, err := m.AddDevice(ctx, owner.MemberID, device(pushtest.Endpoint("laptop"), b, "Firefox")); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveDevice(ctx, "mem_somebody_else", pushtest.Endpoint("laptop")); err != nil {
		t.Fatal(err)
	}
	if links, _ := auth.ListLinks(ctx, st, owner.MemberID); len(links) != 1 {
		t.Fatalf("someone else removed the device: %+v", links)
	}
	if err := m.RemoveDevice(ctx, owner.MemberID, pushtest.Endpoint("laptop")); err != nil {
		t.Fatal(err)
	}
	if links, _ := auth.ListLinks(ctx, st, owner.MemberID); len(links) != 0 {
		t.Fatalf("turned off but still there: %+v", links)
	}
}

// TestPushReminderAndButton is the whole path: the scheduler sends a
// reminder to two devices; one press of "Already paid" in a notification,
// with no session, marks the charge paid through the chat's use case.
func TestPushReminderAndButton(t *testing.T) {
	ctx := context.Background()
	st, push, opts := pushWorld(t)
	m, _ := notifiers.New(st, opts, slog.New(slog.DiscardHandler))
	madrid, _ := time.LoadLocation("Europe/Madrid")
	now := time.Date(2026, 11, 5, 9, 0, 0, 0, madrid)
	svc := app.New(st, bus.New(), nil, func() time.Time { return now })
	owner, err := svc.Bootstrap(ctx, app.BootstrapInput{HouseholdName: "Home", OwnerName: "Alex", Currency: "EUR", Timezone: "Europe/Madrid", Locale: "en"})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := svc.CreateSubscription(ctx, owner, app.CreateSubscriptionInput{Name: "Spotify Family", Price: "17.99", Currency: "EUR", Interval: 1, Unit: "month", FirstCharge: "2026-11-08"})
	if err != nil {
		t.Fatal(err)
	}
	phone, laptop := pushtest.NewBrowser(), pushtest.NewBrowser()
	phoneID, _ := m.AddDevice(ctx, owner.MemberID, device(pushtest.Endpoint("phone"), phone, "Safari on iPhone"))
	if _, err := m.AddDevice(ctx, owner.MemberID, device(pushtest.Endpoint("laptop"), laptop, "Firefox")); err != nil {
		t.Fatal(err)
	}
	eng := actions.New(svc, slog.New(slog.DiscardHandler), m.Channels()...)
	clock := scheduler.New(svc, slog.New(slog.DiscardHandler), eng)
	if err := svc.Plan(ctx); err != nil {
		t.Fatal(err)
	}
	clock.Tick(ctx)
	got := push.Received(t, "phone", phone)
	if len(push.Received(t, "laptop", laptop)) != 1 || len(got) != 1 {
		t.Fatalf("phone got %d, laptop %d", len(got), len(push.Received(t, "laptop", laptop)))
	}
	p := got[0]
	if p["title"] != "🔔 Spotify Family renews in 3 days" || p["url"] != "/subscriptions/"+sub.Subscription.ID || !strings.Contains(p["body"].(string), "17.99") {
		t.Fatalf("payload %v", p)
	}
	acts := p["actions"].([]any)
	paid := acts[0].(map[string]any)
	if len(acts) != 2 || paid["action"] != "paid" || paid["title"] != "✅ Already paid" {
		t.Fatalf("actions %v", acts)
	}
	snooze := acts[1].(map[string]any)
	if snooze["action"] != "snz:1:r" || snooze["title"] != "💤 Remind me tomorrow" {
		t.Fatalf("snooze action %v", snooze)
	}
	token := paid["token"].(string)

	if _, err := m.PushAction(ctx, token); err == nil {
		t.Fatal("ran with no action layer wired")
	}
	m.SetPushActions(eng)
	if _, err := m.PushAction(ctx, token+"x"); !errors.Is(err, notifiers.ErrActionExpired) {
		t.Fatalf("forged token: %v", err)
	}
	// "Remind me tomorrow" runs the chat's snooze, arguments included.
	if res, err := m.PushAction(ctx, snooze["token"].(string)); err != nil || res.Message != "💤 Snoozed" {
		t.Fatalf("snooze: %+v, %v", res, err)
	}
	res, err := m.PushAction(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if res.Message != "Marked as paid ✅" || res.Title != "✅ Spotify Family paid" {
		t.Fatalf("result %+v", res)
	}
	detail, _ := svc.GetSubscription(ctx, owner, app.GetSubscriptionInput{ID: sub.Subscription.ID})
	charges, _ := svc.ListCharges(ctx, owner, app.ListChargesInput{From: "2026-11-08", To: "2026-11-08"})
	if len(charges.Charges) != 1 || charges.Charges[0].Status != domain.ChargePaid {
		t.Fatalf("charges %+v (upcoming %+v)", charges.Charges, detail.Upcoming)
	}
	audit, _ := svc.ListAudit(ctx, owner, app.AuditLogInput{})
	if len(audit.Entries) == 0 || audit.Entries[0].ActorLabel != "Alex" {
		t.Fatalf("audit %+v", audit.Entries)
	}

	// Removing the device revokes the buttons it was sent.
	if err := m.RemoveDevice(ctx, owner.MemberID, pushtest.Endpoint("phone")); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PushAction(ctx, token); !errors.Is(err, notifiers.ErrActionExpired) {
		t.Fatalf("token of a removed device (%s): %v", phoneID, err)
	}

	// The last device is gone at the push service: it is removed and the
	// reminder is recorded as having nowhere to go, not retried forever.
	push.Fail("/push/laptop", http.StatusNotFound)
	item := app.OutboxItem{MemberID: owner.MemberID, ChargeID: charges.Charges[0].ID, Kind: app.KindReminder}
	if err := eng.Deliver(ctx, item); !errors.Is(err, app.ErrNoDestination) {
		t.Fatalf("deliver to a gone device: %v", err)
	}
	if links, _ := auth.ListLinks(ctx, st, owner.MemberID); len(links) != 0 {
		t.Fatalf("links %+v", links)
	}
}
