package notify_test

import (
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/channels/notify"
)

func TestSplitTitle(t *testing.T) {
	for in, want := range map[string][2]string{
		"🔔 Netflix renews in 3 days":                   {"🔔", "Netflix renews in 3 days"},
		"⚠️ Netflix failed — did it go through later?": {"⚠️", "Netflix failed — did it go through later?"},
		"✏️ Spotify → 12 EUR?":                         {"✏️", "Spotify → 12 EUR?"},
		"Netflix":                                      {"", "Netflix"},
		"Netflix renews":                               {"", "Netflix renews"},
		"3 charges to confirm":                         {"", "3 charges to confirm"},
		"  🔔   Padded  ":                               {"🔔", "Padded"},
		"🔔":                                            {"", "🔔"},
		"":                                             {"", ""},
	} {
		emoji, text := notify.SplitTitle(in)
		if emoji != want[0] || text != want[1] {
			t.Errorf("SplitTitle(%q) = %q, %q; want %q, %q", in, emoji, text, want[0], want[1])
		}
	}
}

func TestSubject(t *testing.T) {
	charge := &actions.Event{Amount: "12.99", Currency: "EUR"}
	for _, c := range []struct {
		m    actions.Message
		want string
	}{
		{actions.Message{Title: "🔔 Netflix renews in 3 days", Event: charge}, "Netflix renews in 3 days — 12.99 EUR"},
		{actions.Message{Title: "❓ Was Netflix charged?", Event: charge}, "Was Netflix charged? — 12.99 EUR"},
		{actions.Message{Title: "🎁 Your free trial ends tomorrow"}, "Your free trial ends tomorrow"},
		{actions.Message{Title: "🔔 Odd\r\nBcc: x@example.com", Event: &actions.Event{}}, "Odd Bcc: x@example.com"},
		{actions.Message{}, "Exhale"},
	} {
		if got := notify.Subject(c.m); got != c.want {
			t.Errorf("Subject(%q) = %q, want %q", c.m.Title, got, c.want)
		}
	}
}

// full uses every part of a message, the way a new kind of reminder might.
var full = actions.Message{
	Title: "📊 Your November <summary>",
	Lines: []string{"Total: 42,00 € & counting", "3 subscriptions"},
	Sections: []actions.Section{
		{Heading: "By category", Table: [][]string{{"Streaming", "30,00 €"}, {"Music", "12,00 €"}}},
		{Heading: "Notes", Lines: []string{`"Quoted" line`}},
	},
	Rows:   [][]actions.Button{{{Label: "📋 Subscriptions", Data: "a|subs|0"}}},
	Footer: "Changed by Claude",
}

func TestText(t *testing.T) {
	want := "📊 Your November <summary>\n\n" +
		"Total: 42,00 € & counting\n3 subscriptions\n\n" +
		"By category\nStreaming  30,00 €\nMusic      12,00 €\n\n" +
		"Notes\n\"Quoted\" line\n\n" +
		"Changed by Claude\n\n" +
		"Open in Exhale: https://exhale.example.com/"
	if got := notify.Text(full, "https://exhale.example.com/"); got != want {
		t.Fatalf("Text =\n%s\nwant\n%s", got, want)
	}
	if got := notify.Text(actions.Message{Title: "Just a title"}, ""); got != "Just a title" {
		t.Fatalf("Text = %q", got)
	}
}

func TestHTML(t *testing.T) {
	html := notify.HTML(full, "https://exhale.example.com/", "https://exhale.example.com/settings")
	for _, want := range []string{
		"<title>Your November &lt;summary&gt;</title>",
		"📊 Your November &lt;summary&gt;</h1>",
		"Total: 42,00 € &amp; counting</p>",
		"By category</h2>",
		">Streaming</td>", `text-align:right">30,00 €</td>`,
		"&#34;Quoted&#34; line</p>",
		"Changed by Claude</p>",
		`href="https://exhale.example.com/"`, ">Open in Exhale</a>",
		`href="https://exhale.example.com/settings"`,
		`<meta name="color-scheme" content="light dark">`,
		"prefers-color-scheme: dark",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML misses %q", want)
		}
	}
	for _, not := range []string{"a|subs|0", "📋 Subscriptions", "<summary>", "background:#fff", "background: #fff"} {
		if strings.Contains(html, not) {
			t.Errorf("HTML contains %q", not)
		}
	}
	// No public address: no button, no links.
	if bare := notify.HTML(full, "", ""); strings.Contains(bare, "<a ") {
		t.Errorf("links without a public address:\n%s", bare)
	}
	// A link that is not a web address does not become a button target.
	if evil := notify.HTML(full, "javascript:alert(1)", ""); strings.Contains(evil, "javascript:alert") {
		t.Errorf("script link:\n%s", evil)
	}
}

func TestLink(t *testing.T) {
	charge := actions.Message{Event: &actions.Event{Path: "/subscriptions/sub_1"}}
	for _, c := range []struct {
		public string
		m      actions.Message
		want   string
	}{
		{"https://exhale.example.com", charge, "https://exhale.example.com/subscriptions/sub_1"},
		{"https://exhale.example.com/", charge, "https://exhale.example.com/subscriptions/sub_1"},
		{"https://example.com/exhale", charge, "https://example.com/exhale/subscriptions/sub_1"},
		{"https://exhale.example.com", actions.Message{}, "https://exhale.example.com/"},
		{"https://exhale.example.com", actions.Message{Event: &actions.Event{Path: "//evil.example"}}, "https://exhale.example.com//evil.example"},
		{"", charge, ""},
	} {
		if got := notify.Link(c.public, c.m); got != c.want {
			t.Errorf("Link(%q) = %q, want %q", c.public, got, c.want)
		}
	}
	for in, want := range map[string]string{
		"https://exhale.example.com/":     "https://exhale.example.com",
		" http://192.168.1.5:8484 ":       "http://192.168.1.5:8484",
		"https://example.com/exhale/?x=1": "https://example.com/exhale",
		"exhale.example.com":              "",
		"ftp://exhale.example.com":        "",
		"":                                "",
	} {
		if got := notify.PublicURL(in); got != want {
			t.Errorf("PublicURL(%q) = %q, want %q", in, got, want)
		}
	}
}
