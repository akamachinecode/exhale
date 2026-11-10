// Package notify is what the notify-only channels (email, ntfy, webhooks)
// share: turning a channel-agnostic actions.Message into plain text and
// HTML, and the link back to the web. These channels are one-way: no
// buttons, no editing. The message says what is due and links to the place
// where it can be fixed; Telegram stays the interactive channel (ADR 0007).
//
// Everything here reads only the generic parts of a message (title, lines,
// sections, footer, event), so new kinds of reminders need no changes.
package notify

import (
	"bytes"
	"context"
	"html/template"
	"net/url"
	"strings"
	"unicode"

	"github.com/akamachinecode/exhale/internal/actions"
)

// OpenLabel is the text of the link or button that opens the web.
const OpenLabel = "Open in Exhale"

// OneWay is embedded by channels that cannot edit what they sent.
type OneWay struct{}

// Edit does nothing: the message already left.
func (OneWay) Edit(context.Context, string, string, actions.Message) error { return nil }

// SplitTitle separates the emoji a title starts with from its text:
// "🔔 Netflix renews in 3 days" → "🔔", "Netflix renews in 3 days".
func SplitTitle(title string) (emoji, text string) {
	title = strings.TrimSpace(title)
	first, rest, ok := strings.Cut(title, " ")
	if !ok || first == "" {
		return "", title
	}
	for _, r := range first {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return "", title
		}
	}
	return first, strings.TrimSpace(rest)
}

// Subject is a one-line summary: the title without its emoji, plus the
// amount when the message is about a charge ("Netflix renews in 3 days —
// 12.99 EUR").
func Subject(m actions.Message) string {
	_, s := SplitTitle(m.Title)
	if s == "" {
		s = "Exhale"
	}
	if ev := m.Event; ev != nil && ev.Amount != "" && ev.Currency != "" {
		s += " — " + ev.Amount + " " + ev.Currency
	}
	return strings.Join(strings.Fields(s), " ") // no line breaks in a subject
}

// Body is everything under the title as plain text: lines, sections with
// their tables, and the footer. Button rows are left out.
func Body(m actions.Message) string {
	var b strings.Builder
	for _, l := range m.Lines {
		b.WriteString(l + "\n")
	}
	for _, s := range m.Sections {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		if s.Heading != "" {
			b.WriteString(s.Heading + "\n")
		}
		for _, l := range s.Lines {
			b.WriteString(l + "\n")
		}
		if len(s.Table) > 0 {
			b.WriteString(s.TableText() + "\n")
		}
	}
	if m.Footer != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(m.Footer + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// Text is the whole message as plain text, ending with the link if any.
func Text(m actions.Message, link string) string {
	parts := []string{strings.TrimSpace(m.Title)}
	if body := Body(m); body != "" {
		parts = append(parts, body)
	}
	if link != "" {
		parts = append(parts, OpenLabel+": "+link)
	}
	return strings.Join(parts, "\n\n")
}

// PublicURL cleans the instance's public address ("" if it is not an http
// or https URL): no trailing slash, no query.
func PublicURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	u.RawQuery, u.Fragment = "", ""
	return strings.TrimRight(u.String(), "/")
}

// Link is the web address that shows what the message is about: the
// subscription of a charge, or the home page for other messages. It is ""
// when the instance has no public address (--public-url).
func Link(publicURL string, m actions.Message) string {
	publicURL = strings.TrimRight(publicURL, "/")
	if publicURL == "" {
		return ""
	}
	if m.Event != nil && strings.HasPrefix(m.Event.Path, "/") {
		return publicURL + m.Event.Path
	}
	return publicURL + "/"
}

// SettingsLink is where notifications are managed in the web ("" without a
// public address).
func SettingsLink(publicURL string) string {
	publicURL = strings.TrimRight(publicURL, "/")
	if publicURL == "" {
		return ""
	}
	return publicURL + "/settings"
}

type htmlSection struct {
	Heading string
	Lines   []string
	Table   [][]string
}

// The email leaves the page background and text colour to the mail client,
// so it follows its light or dark theme. Only the button has its own
// colours, with a border that keeps it visible on both.
var page = template.Must(template.New("mail").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<meta name="supported-color-schemes" content="light dark">
<title>{{.Subject}}</title>
<style>
:root { color-scheme: light dark; }
@media (prefers-color-scheme: dark) {
  .btn { background: #fafafa !important; color: #18181b !important; }
}
</style>
</head>
<body style="margin:0;padding:24px 16px;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,Helvetica,Arial,sans-serif;font-size:15px;line-height:1.5">
<div style="max-width:480px;margin:0 auto">
<h1 style="font-size:20px;line-height:1.3;margin:0 0 12px">{{.Title}}</h1>
{{range .Lines}}<p style="margin:0 0 4px">{{.}}</p>
{{end}}{{range .Sections}}{{if .Heading}}<h2 style="font-size:15px;margin:20px 0 6px">{{.Heading}}</h2>
{{end}}{{range .Lines}}<p style="margin:0 0 4px">{{.}}</p>
{{end}}{{if .Table}}<table style="border-collapse:collapse;margin:6px 0 0;font-size:14px">
{{range .Table}}<tr>{{range $i, $cell := .}}<td style="padding:3px 16px 3px 0{{if $i}};text-align:right{{end}}">{{$cell}}</td>{{end}}</tr>
{{end}}</table>
{{end}}{{end}}{{if .Footer}}<p style="margin:16px 0 0;font-size:13px;opacity:.7">{{.Footer}}</p>
{{end}}{{if .Link}}<p style="margin:24px 0 0"><a class="btn" href="{{.Link}}" style="display:inline-block;padding:10px 18px;border-radius:8px;border:1px solid #71717a;background:#18181b;color:#fafafa;font-weight:600;text-decoration:none">{{.Open}}</a></p>
{{end}}<p style="margin:28px 0 0;font-size:12px;opacity:.6">Sent by Exhale{{if .Settings}} · <a href="{{.Settings}}" style="color:inherit">Notification settings</a>{{end}}</p>
</div>
</body>
</html>
`))

// HTML is the message as a small HTML page for email. link ("" for none)
// becomes the one button; settings ("" for none) a footer link.
func HTML(m actions.Message, link, settings string) string {
	data := struct {
		Subject, Title, Footer, Link, Settings, Open string
		Lines                                        []string
		Sections                                     []htmlSection
	}{Subject: Subject(m), Title: strings.TrimSpace(m.Title), Footer: m.Footer, Link: link, Settings: settings, Open: OpenLabel, Lines: m.Lines}
	for _, s := range m.Sections {
		data.Sections = append(data.Sections, htmlSection{Heading: s.Heading, Lines: s.Lines, Table: s.Table})
	}
	var b bytes.Buffer
	if err := page.Execute(&b, data); err != nil {
		// The template is fixed; fall back to text rather than send nothing.
		return "<pre>" + template.HTMLEscapeString(Text(m, link)) + "</pre>"
	}
	return b.String()
}

// Test is the message the "Send test" buttons deliver: it looks like a real
// reminder so templates and clients can be checked with it.
func Test(dueDate string) actions.Message {
	return actions.Message{
		Title: "🔔 Test reminder from Exhale",
		Lines: []string{"💶 12.99 EUR · in 3 days", "This is a test. Your reminders will arrive here."},
		Event: &actions.Event{
			Kind: actions.EventTest, Subscription: "Exhale test", Amount: "12.99", Currency: "EUR",
			DueDate: dueDate, DaysUntil: 3, PaymentMethod: "Test card", Path: "/settings",
		},
	}
}
