package desktop

import (
	"fmt"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/i18n"
)

// The menu of the menu bar icon (ADR 0032), built from a glance. It is a
// plain tree here, tested without a desktop; package window turns it into
// the native menu and runs its actions.

// Menu actions.
const (
	ActOpen    = "open"    // show the window, at Path
	ActStart   = "start"   // keep awake for Minutes (0: until stopped)
	ActExtend  = "extend"  // add Minutes to the session
	ActStop    = "stop"    // end the session
	ActLid     = "lid"     // keep going with the lid closed: On
	ActOnPower = "onPower" // only while plugged in: On
	ActQuit    = "quit"    // end the icon and the window, not the server
)

// MenuAction is what clicking an item does.
type MenuAction struct {
	Kind    string
	Path    string // ActOpen: where in the web UI
	Minutes int    // ActStart, ActExtend
	On      bool   // ActLid, ActOnPower: the new value
	Setup   bool   // ActLid: ask the computer for the permission after turning it on
}

// MenuItem is one row of the menu.
type MenuItem struct {
	Label     string
	Separator bool
	Enabled   bool
	Check     bool // a checkbox, ticked when Checked
	Checked   bool
	Action    MenuAction
	Items     []MenuItem // a submenu
}

// Menu is the whole menu, with the icon's tooltip and whether the icon
// shows the computer kept awake.
type Menu struct {
	Items   []MenuItem
	Tooltip string
	Awake   bool
}

// MenuInput is what the menu is built from.
type MenuInput struct {
	Glance *Glance // nil while the API cannot be read
	Lang   string  // the language when there is no glance
	Now    time.Time
	OS     string // runtime.GOOS: the words for the icon differ
}

// The web UI's pages the menu opens.
const (
	homePath      = "/"
	keepAwakePath = "/settings?tab=general"
)

// maxTooltip keeps the tooltip within what Windows shows (128 characters).
const maxTooltip = 127

// BuildMenu builds the menu from a glance.
func BuildMenu(in MenuInput) Menu {
	lang := in.Lang
	g := in.Glance
	if g != nil && g.Language != "" {
		lang = g.Language
	}
	var m Menu
	tip := []string{"Exhale"}
	if g != nil {
		m.Items = append(m.Items, chargeItems(lang, g)...)
		if len(g.Charges) > 0 {
			c := g.Charges[0]
			tip = append(tip, fmt.Sprintf(tr(lang, "Next: %s"), chargeLine(lang, c)))
		}
		k := g.KeepAwake
		status := awakeLine(lang, k, in.Now)
		m.Awake = k.Status.State == "active"
		if m.Awake {
			tip = append(tip, status)
		}
		m.Items = append(m.Items, MenuItem{Separator: true}, MenuItem{Label: status})
		m.Items = append(m.Items, awakeItems(lang, k)...)
		m.Items = append(m.Items, MenuItem{Separator: true})
	}
	quit := tr(lang, "Quit tray icon")
	if in.OS == "darwin" {
		quit = tr(lang, "Quit menu bar icon")
	}
	m.Items = append(m.Items,
		MenuItem{Label: tr(lang, "Open Exhale"), Enabled: true, Action: MenuAction{Kind: ActOpen}},
		MenuItem{Label: quit, Enabled: true, Action: MenuAction{Kind: ActQuit}},
	)
	m.Tooltip = truncateRunes(strings.Join(tip, "\n"), maxTooltip)
	return m
}

// chargeItems lists the next charges, what the next 30 days cost and the
// charges waiting to be confirmed.
func chargeItems(lang string, g *Glance) []MenuItem {
	items := []MenuItem{{Label: tr(lang, "Next charges")}}
	if len(g.Charges) == 0 {
		items = append(items, MenuItem{Label: tr(lang, "Nothing to pay in the next 30 days")})
	}
	for _, c := range g.Charges {
		items = append(items, MenuItem{
			Label: chargeLine(lang, c), Enabled: true,
			Action: MenuAction{Kind: ActOpen, Path: "/subscriptions/" + c.SubscriptionID},
		})
	}
	if len(g.Totals) > 0 {
		items = append(items, MenuItem{Label: fmt.Sprintf(tr(lang, "Next 30 days: %s"), strings.Join(g.Totals, " + "))})
	}
	if g.Overdue > 0 {
		items = append(items, MenuItem{
			Label: fmt.Sprintf(plural(lang, g.Overdue, "%d charge to confirm", "%d charges to confirm"), g.Overdue), Enabled: true,
			Action: MenuAction{Kind: ActOpen, Path: homePath},
		})
	}
	return items
}

// chargeLine is "Netflix — 13,99 € · tomorrow".
func chargeLine(lang string, c GlanceCharge) string {
	when := dayWord(lang, c.DaysUntil, c.Date)
	switch c.Kind {
	case "trial":
		return fmt.Sprintf(tr(lang, "%s — trial ends, then %s · %s"), c.Name, c.Amount, when)
	case "expiry":
		return fmt.Sprintf(tr(lang, "%s — expires · %s"), c.Name, when)
	}
	return fmt.Sprintf("%s — %s · %s", c.Name, c.Amount, when)
}

// dayWord says when a date comes: today, tomorrow, its weekday within a
// week, else in how many days.
func dayWord(lang string, days int, date string) string {
	switch {
	case days <= 0:
		return tr(lang, "today")
	case days == 1:
		return tr(lang, "tomorrow")
	case days < 7:
		if d, err := time.Parse(time.DateOnly, date); err == nil {
			return weekday(lang, d.Weekday())
		}
	}
	return fmt.Sprintf(tr(lang, "in %d days"), days) // a week or more: always plural
}

// weekday names a day in the language: the list the chat uses too.
func weekday(lang string, d time.Weekday) string {
	names := strings.Split(tr(lang, "Sunday, Monday, Tuesday, Wednesday, Thursday, Friday, Saturday"), ",")
	if int(d) >= len(names) {
		return d.String()
	}
	return strings.TrimSpace(names[d])
}

// awakeLine says whether the computer is kept awake now.
func awakeLine(lang string, k KeepAwake, now time.Time) string {
	s := k.Status
	switch s.State {
	case "active":
		if s.Until != "" {
			if t, err := time.Parse(time.RFC3339, s.Until); err == nil {
				return awakeUntil(lang, t.In(now.Location()), now)
			}
		}
		return tr(lang, "Kept awake")
	case "waiting":
		return fmt.Sprintf(tr(lang, "Kept awake from %s"), k.From)
	case "paused":
		if s.Reason == "battery" {
			return fmt.Sprintf(tr(lang, "Paused: battery low (%d %%)"), s.Power.Percent)
		}
		if s.Power.HasBattery {
			return fmt.Sprintf(tr(lang, "Paused: on battery (%d %%)"), s.Power.Percent)
		}
		return tr(lang, "Paused: on battery")
	case "unavailable":
		return tr(lang, "Cannot keep this computer awake")
	case "failed":
		return tr(lang, "Keeping awake failed")
	case "unknown":
		return tr(lang, "Keep awake: unknown")
	}
	return tr(lang, "Not kept awake")
}

// awakeUntil says until when: 15:40 today, else with its day.
func awakeUntil(lang string, t, now time.Time) string {
	hm := t.Format("15:04")
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	y, m, d = t.Date()
	days := int(time.Date(y, m, d, 0, 0, 0, 0, now.Location()).Sub(today).Hours()/24 + 0.5)
	switch {
	case days <= 0:
		return fmt.Sprintf(tr(lang, "Awake until %s"), hm)
	case days == 1:
		return fmt.Sprintf(tr(lang, "Awake until tomorrow at %s"), hm)
	}
	return fmt.Sprintf(tr(lang, "Awake until %s at %s"), weekday(lang, t.Weekday()), hm)
}

// awakeItems are the keep-awake controls: sessions, the lid and power.
func awakeItems(lang string, k KeepAwake) []MenuItem {
	if k.Status.State == "unavailable" || k.Status.State == "unknown" {
		return nil
	}
	sub := []MenuItem{
		{Label: tr(lang, "For 1 hour"), Enabled: true, Action: MenuAction{Kind: ActStart, Minutes: 60}},
		{Label: tr(lang, "For 2 hours"), Enabled: true, Action: MenuAction{Kind: ActStart, Minutes: 120}},
		{Label: tr(lang, "For 8 hours"), Enabled: true, Action: MenuAction{Kind: ActStart, Minutes: 480}},
		{Label: tr(lang, "Until I stop it"), Enabled: true, Action: MenuAction{Kind: ActStart}},
	}
	if k.Session.Active {
		sub = append(sub, MenuItem{Separator: true})
		if k.Session.Until != "" {
			sub = append(sub, MenuItem{Label: tr(lang, "Add 1 hour"), Enabled: true, Action: MenuAction{Kind: ActExtend, Minutes: 60}})
		}
		sub = append(sub, MenuItem{Label: tr(lang, "Stop"), Enabled: true, Action: MenuAction{Kind: ActStop}})
	}
	sub = append(sub, MenuItem{Separator: true},
		MenuItem{Label: tr(lang, "Until a time or on a schedule…"), Enabled: true, Action: MenuAction{Kind: ActOpen, Path: keepAwakePath}})
	items := []MenuItem{{Label: tr(lang, "Keep awake"), Enabled: true, Items: sub}}
	power := k.Status.Power
	// A desktop has no lid and no battery: those two would only confuse.
	if power.HasBattery || k.LidClosed {
		lid := MenuItem{
			Label: tr(lang, "Keep going with the lid closed"), Enabled: k.Status.Lid != "unavailable",
			Check: true, Checked: k.LidClosed, Action: MenuAction{Kind: ActLid, On: !k.LidClosed},
		}
		if k.Status.Lid == "setup" {
			lid.Label = tr(lang, "Keep going with the lid closed…")
			lid.Action = MenuAction{Kind: ActLid, On: true, Setup: true}
		}
		items = append(items, lid)
	}
	if power.HasBattery || k.OnlyOnPower {
		items = append(items, MenuItem{
			Label: tr(lang, "Only while plugged in"), Enabled: true,
			Check: true, Checked: k.OnlyOnPower, Action: MenuAction{Kind: ActOnPower, On: !k.OnlyOnPower},
		})
	}
	return items
}

// plural picks the counted text (the catalogs in internal/i18n).
func plural(lang string, n int, one, other string) string { return i18n.Plural(lang, n, one, other) }

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
