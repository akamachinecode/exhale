package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/akamachinecode/exhale/internal/auth"
)

// now is a Thursday afternoon in Madrid.
var now = time.Date(2026, 11, 12, 15, 0, 0, 0, time.FixedZone("CET", 3600))

func glance(lang string) *Glance {
	g := &Glance{
		Language: lang, Today: "2026-11-12",
		Charges: []GlanceCharge{
			{SubscriptionID: "s1", Name: "Netflix", Amount: "13,99 €", Date: "2026-11-13", DaysUntil: 1, Kind: "renewal"},
			{SubscriptionID: "s2", Name: "Figma", Amount: "15,00 $", Date: "2026-11-16", DaysUntil: 4, Kind: "trial"},
			{SubscriptionID: "s3", Name: "Domain", Amount: "9,00 €", Date: "2026-11-30", DaysUntil: 18, Kind: "expiry"},
		},
		Totals:  []string{"22,99 €", "15,00 $"},
		Overdue: 2,
	}
	g.KeepAwake.Mode = "off"
	g.KeepAwake.Session.Active = true
	g.KeepAwake.Session.Until = "2026-11-12T15:40:00Z"
	g.KeepAwake.Status.State = "active"
	g.KeepAwake.Status.Until = "2026-11-12T15:40:00Z"
	g.KeepAwake.Status.Lid = "setup"
	g.KeepAwake.Status.Power.HasBattery = true
	g.KeepAwake.Status.Power.Percent = 80
	return g
}

// labels flattens a menu: submenu items are indented, separators are "-",
// ticked checkboxes start with "✓", disabled items are in brackets.
func labels(items []MenuItem, indent string) []string {
	var out []string
	for _, it := range items {
		l := it.Label
		switch {
		case it.Separator:
			l = "-"
		case it.Checked:
			l = "✓ " + l
		}
		if !it.Separator && !it.Enabled {
			l = "[" + l + "]"
		}
		out = append(out, indent+l)
		out = append(out, labels(it.Items, indent+"  ")...)
	}
	return out
}

func TestBuildMenu(t *testing.T) {
	m := BuildMenu(MenuInput{Glance: glance("en"), Lang: "es", Now: now, OS: "darwin"})
	want := []string{
		"[Next charges]",
		"Netflix — 13,99 € · tomorrow",
		"Figma — trial ends, then 15,00 $ · Monday",
		"Domain — expires · in 18 days",
		"[Next 30 days: 22,99 € + 15,00 $]",
		"2 charges to confirm",
		"-",
		"[Awake until 16:40]",
		"Keep awake",
		"  For 1 hour", "  For 2 hours", "  For 8 hours", "  Until I stop it",
		"  -", "  Add 1 hour", "  Stop",
		"  -", "  Until a time or on a schedule…",
		"Keep going with the lid closed…",
		"Only while plugged in",
		"-",
		"Open Exhale",
		"Quit menu bar icon",
	}
	if got := labels(m.Items, ""); !slices.Equal(got, want) {
		t.Errorf("menu:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if !m.Awake {
		t.Error("not shown awake")
	}
	if want := "Exhale\nNext: Netflix — 13,99 € · tomorrow\nAwake until 16:40"; m.Tooltip != want {
		t.Errorf("tooltip = %q, want %q", m.Tooltip, want)
	}
	// What the items do.
	if a := m.Items[1].Action; a.Kind != ActOpen || a.Path != "/subscriptions/s1" {
		t.Errorf("a charge opens %+v", a)
	}
	if a := m.Items[5].Action; a.Kind != ActOpen || a.Path != "/" {
		t.Errorf("charges to confirm open %+v", a)
	}
	sub := m.Items[8].Items
	if a := sub[3].Action; a.Kind != ActStart || a.Minutes != 0 {
		t.Errorf("until stopped: %+v", a)
	}
	if a := sub[5].Action; a.Kind != ActExtend || a.Minutes != 60 {
		t.Errorf("add an hour: %+v", a)
	}
	if a := m.Items[9].Action; a.Kind != ActLid || !a.On || !a.Setup {
		t.Errorf("the lid needs setup: %+v", a)
	}
	if a := m.Items[10].Action; a.Kind != ActOnPower || !a.On || !m.Items[10].Check {
		t.Errorf("plugged in: %+v", m.Items[10])
	}
}

func TestBuildMenuSpanish(t *testing.T) {
	g := glance("es")
	g.Overdue = 1
	m := BuildMenu(MenuInput{Glance: g, Now: now, OS: "linux"})
	got := labels(m.Items, "")
	for _, want := range []string{"[Próximos cobros]", "Netflix — 13,99 € · mañana", "Figma — acaba la prueba, luego 15,00 $ · lunes", "Domain — caduca · en 18 días", "1 cobro por confirmar", "Abrir Exhale", "Cerrar el icono de la bandeja"} {
		if !slices.Contains(got, want) {
			t.Errorf("no %q in\n%s", want, strings.Join(got, "\n"))
		}
	}
}

func TestBuildMenuWithoutGlance(t *testing.T) {
	m := BuildMenu(MenuInput{Lang: "en", Now: now, OS: "windows"})
	if got := labels(m.Items, ""); !slices.Equal(got, []string{"Open Exhale", "Quit tray icon"}) {
		t.Errorf("menu = %q", got)
	}
	if m.Tooltip != "Exhale" || m.Awake {
		t.Errorf("tooltip %q, awake %v", m.Tooltip, m.Awake)
	}
}

func TestAwakeLine(t *testing.T) {
	k := func(state, reason string) KeepAwake {
		var k KeepAwake
		k.From = "08:00"
		k.Status.State, k.Status.Reason = state, reason
		k.Status.Power.HasBattery, k.Status.Power.Percent = true, 18
		return k
	}
	tomorrow := k("active", "")
	tomorrow.Status.Until = "2026-11-13T08:00:00Z"
	monday := k("active", "")
	monday.Status.Until = "2026-11-16T08:00:00Z"
	for _, c := range []struct {
		k    KeepAwake
		want string
	}{
		{k("off", ""), "Not kept awake"},
		{k("active", ""), "Kept awake"},
		{tomorrow, "Awake until tomorrow at 09:00"},
		{monday, "Awake until Monday at 09:00"},
		{k("waiting", ""), "Kept awake from 08:00"},
		{k("paused", "power"), "Paused: on battery (18 %)"},
		{k("paused", "battery"), "Paused: battery low (18 %)"},
		{k("unavailable", "container"), "Cannot keep this computer awake"},
	} {
		if got := awakeLine("en", c.k, now); got != c.want {
			t.Errorf("%s/%s: %q, want %q", c.k.Status.State, c.k.Status.Reason, got, c.want)
		}
	}
}

func TestAwakeItems(t *testing.T) {
	// A desktop: no lid, no battery, no session.
	var k KeepAwake
	k.Status.State = "off"
	got := labels(awakeItems("en", k), "")
	if slices.Contains(got, "Keep going with the lid closed") || slices.Contains(got, "Only while plugged in") || slices.Contains(got, "  Stop") {
		t.Errorf("a desktop without a session: %q", got)
	}
	// Not possible here: no controls.
	k.Status.State = "unavailable"
	if items := awakeItems("en", k); items != nil {
		t.Errorf("unavailable: %q", labels(items, ""))
	}
	// A laptop with the lid kept awake: ticked, and clicking turns it off.
	k.Status.State, k.LidClosed, k.Status.Lid = "active", true, "active"
	items := awakeItems("en", k)
	lid := items[1]
	if !lid.Checked || lid.Action.On || lid.Action.Setup || !lid.Enabled {
		t.Errorf("lid = %+v", lid)
	}
	// A session until stopped has nothing to extend.
	k.Session.Active = true
	got = labels(awakeItems("en", k), "")
	if slices.Contains(got, "  Add 1 hour") || !slices.Contains(got, "  Stop") {
		t.Errorf("until stopped: %q", got)
	}
}

func TestDayWord(t *testing.T) {
	for _, c := range []struct {
		days int
		date string
		want string
	}{
		{0, "2026-11-12", "today"},
		{-1, "2026-11-11", "today"},
		{1, "2026-11-13", "tomorrow"},
		{2, "2026-11-14", "Saturday"},
		{6, "2026-11-18", "Wednesday"},
		{7, "2026-11-19", "in 7 days"},
	} {
		if got := dayWord("en", c.days, c.date); got != c.want {
			t.Errorf("dayWord(%d) = %q, want %q", c.days, got, c.want)
		}
	}
	if got := dayWord("es", 2, "2026-11-14"); got != "sábado" {
		t.Errorf("Spanish Saturday = %q", got)
	}
}

func TestTooltipFitsWindows(t *testing.T) {
	g := glance("en")
	g.Charges[0].Name = strings.Repeat("A very long subscription name ", 10)
	if m := BuildMenu(MenuInput{Glance: g, Now: now}); len([]rune(m.Tooltip)) > maxTooltip {
		t.Errorf("tooltip of %d characters", len([]rune(m.Tooltip)))
	}
}

func TestClient(t *testing.T) {
	dir := t.TempDir()
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer exh_good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body := map[string]any{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		b, _ := json.Marshal(body)
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+string(b))
		switch r.URL.Path {
		case "/api/v1/glance":
			_, _ = w.Write([]byte(`{"language":"es","charges":[{"subscriptionId":"s1","name":"Netflix","amount":"13,99 €","daysUntil":1}],"keepAwake":{"status":{"state":"active","power":{"hasBattery":true,"percent":50}}}}`))
		case "/api/v1/keep-awake":
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"status":400,"title":"Bad Request","detail":"Times must look like 08:00"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()
	c := Client{Base: srv.URL, DataDir: dir}
	ctx := context.Background()
	// No token file yet: a new install.
	if _, err := c.Glance(ctx, 5); !errors.Is(err, ErrNoToken) {
		t.Errorf("without a token: %v", err)
	}
	// A revoked token.
	_ = os.WriteFile(filepath.Join(dir, TokenFile), []byte("exh_old\n"), 0o600)
	if _, err := c.Glance(ctx, 5); !errors.Is(err, ErrNoToken) {
		t.Errorf("with a revoked token: %v", err)
	}
	// The server wrote a new one: read again on every request.
	_ = os.WriteFile(filepath.Join(dir, TokenFile), []byte("exh_good\n"), 0o600)
	g, err := c.Glance(ctx, 5)
	if err != nil || g.Language != "es" || g.Charges[0].Name != "Netflix" || g.KeepAwake.Status.Power.Percent != 50 {
		t.Fatalf("glance = %+v, %v", g, err)
	}
	if err := c.Session(ctx, "start", 60); err != nil {
		t.Error(err)
	}
	if err := c.Session(ctx, "start", 0); err != nil {
		t.Error(err)
	}
	if err := c.SetupLid(ctx); err != nil {
		t.Error(err)
	}
	if err := c.SetKeepAwake(ctx, map[string]any{"lidClosed": true}); err == nil || err.Error() != "Times must look like 08:00" {
		t.Errorf("a refused change: %v", err)
	}
	want := []string{
		"GET /api/v1/glance?limit=5 {}",
		`POST /api/v1/keep-awake/session {"action":"start","minutes":60}`,
		`POST /api/v1/keep-awake/session {"action":"start"}`,
		"POST /api/v1/keep-awake/lid-setup {}",
		`POST /api/v1/keep-awake {"lidClosed":true}`,
	}
	if !slices.Equal(got, want) {
		t.Errorf("requests:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestTokenFileIsTheServers(t *testing.T) {
	if TokenFile != auth.DesktopTokenFile {
		t.Errorf("TokenFile = %q, the server writes %q", TokenFile, auth.DesktopTokenFile)
	}
}

func TestMenuBarPlist(t *testing.T) {
	p := menuBarPlist("/Applications/Exhale & Co.app/Contents/MacOS/exhale", "/Users/a/Library/Logs/Exhale.log")
	var v struct {
		Dict struct {
			Keys    []string `xml:"key"`
			Strings []string `xml:"string"`
			Array   struct {
				Strings []string `xml:"string"`
			} `xml:"array"`
		} `xml:"dict"`
	}
	if err := xml.Unmarshal(p, &v); err != nil {
		t.Fatalf("not XML: %v\n%s", err, p)
	}
	if got := v.Dict.Array.Strings; !slices.Equal(got, []string{"/Applications/Exhale & Co.app/Contents/MacOS/exhale", "open", "--background"}) {
		t.Errorf("ProgramArguments = %q", got)
	}
	if v.Dict.Strings[0] != MenuBarID || v.Dict.Strings[1] != ID {
		t.Errorf("Label, bundle = %q", v.Dict.Strings[:2])
	}
	for _, k := range []string{"RunAtLoad", "LimitLoadToSessionType", "AssociatedBundleIdentifiers"} {
		if !slices.Contains(v.Dict.Keys, k) {
			t.Errorf("missing key %s", k)
		}
	}
	// Quit means quit: launchd must not start it again.
	if slices.Contains(v.Dict.Keys, "KeepAlive") {
		t.Error("the icon's agent is kept alive")
	}
}

func TestAutostartEntry(t *testing.T) {
	e := string(autostartEntry("/usr/bin/exhale"))
	for _, want := range []string{"\nExec=/usr/bin/exhale open --background\n", "\nTryExec=/usr/bin/exhale\n", "\nType=Application\n"} {
		if !strings.Contains(e, want) {
			t.Errorf("no %q in\n%s", want, e)
		}
	}
	if got := desktopExec("/opt/My Apps/exhale"); got != `"/opt/My Apps/exhale"` {
		t.Errorf("desktopExec = %s", got)
	}
}

func TestIconPNG(t *testing.T) {
	for _, c := range []struct {
		size            int
		template, awake bool
	}{{44, true, false}, {44, true, true}, {64, false, false}, {64, false, true}} {
		b := IconPNG(c.size, c.template, c.awake)
		img, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != c.size {
			t.Errorf("%d px wide, want %d", img.Bounds().Dx(), c.size)
		}
		// The template's corners are clear, the tile's middle is not.
		_, _, _, corner := img.At(0, 0).RGBA()
		if c.template && corner != 0 {
			t.Errorf("template corner alpha %d", corner)
		}
	}
	if bytes.Equal(IconPNG(44, true, false), IconPNG(44, true, true)) {
		t.Error("awake looks the same")
	}
}
