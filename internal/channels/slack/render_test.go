package slack

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	slackapi "github.com/slack-go/slack"

	"github.com/akamachinecode/exhale/internal/actions"
	"github.com/akamachinecode/exhale/internal/testutil"
)

func blocksJSON(t *testing.T, bs []slackapi.Block) string {
	t.Helper()
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(bs); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// reminder is the message of a renewal reminder, as the action layer
// builds it.
var reminder = actions.Message{
	Title: "🔔 Netflix renews in 3 days",
	Lines: []string{"💶 €19.99 · Sun, 8 Nov 2026", "💳 Visa ••4421", "🏷 Streaming"},
	Rows: [][]actions.Button{
		{{Label: "✅ Already paid", Data: "paid|chg_1"}, {Label: "📅 Change date", Data: "date|chg_1|2026-11"}},
		{{Label: "💳 Payment method", Data: "pm|chg_1"}, {Label: "✏️ Amount", Data: "amount|chg_1"}},
		{{Label: "⏭ Skip this one", Data: "skip|chg_1"}, {Label: "❌ Cancel subscription", Data: "cancel|chg_1"}},
		{{Label: "🔗 Open the cancel page", URL: "https://www.netflix.com/cancelplan"}},
	},
	Footer: "Changed by Marta (Slack)",
}

// summary is an app screen with tables.
var summary = actions.Message{
	Title: "📊 Summary",
	Lines: []string{"💶 €142.50 / month · €1,710.00 / year", "📦 9 active subscriptions"},
	Sections: []actions.Section{
		{Heading: "Per month by category", Table: [][]string{{"Cloud & Dev", "€61.00", "3"}, {"Streaming", "€45.50", "4"}, {"AT&T <family>", "€36.00", "2"}}},
		{Heading: "Next 7 days", Lines: []string{"Nothing due. Breathe out."}},
	},
	Rows: [][]actions.Button{{{Label: "🏠 Home", Data: "a|home"}}},
}

func TestRenderReminder(t *testing.T) {
	testutil.Golden(t, "reminder", blocksJSON(t, blocks(reminder, maxMessageBlocks)))
}

func TestRenderScreenWithTable(t *testing.T) {
	testutil.Golden(t, "summary", blocksJSON(t, blocks(summary, maxHomeBlocks)))
}

// check walks rendered blocks and fails on anything Slack would reject.
func check(t *testing.T, bs []slackapi.Block, limit int) (buttons int) {
	t.Helper()
	var raw []struct {
		Type    string `json:"type"`
		BlockID string `json:"block_id"`
		Text    *struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"text"`
		Elements []struct {
			Type     string          `json:"type"`
			Text     json.RawMessage `json:"text"`
			ActionID string          `json:"action_id"`
			Value    string          `json:"value"`
			URL      string          `json:"url"`
		} `json:"elements"`
	}
	if err := json.Unmarshal([]byte(blocksJSON(t, bs)), &raw); err != nil {
		t.Fatal(err)
	}
	if len(raw) > limit {
		t.Errorf("%d blocks, the surface takes %d", len(raw), limit)
	}
	ids, actionIDs := map[string]bool{}, map[string]bool{}
	for _, b := range raw {
		if b.BlockID != "" {
			if ids[b.BlockID] {
				t.Errorf("block_id %q is used twice", b.BlockID)
			}
			ids[b.BlockID] = true
		}
		switch b.Type {
		case "header":
			if width(b.Text.Text) > maxHeader || b.Text.Type != "plain_text" {
				t.Errorf("bad header: %d characters, %s", width(b.Text.Text), b.Text.Type)
			}
		case "section":
			if n := width(b.Text.Text); n > maxText || n == 0 {
				t.Errorf("section text has %d characters", n)
			}
		case "context":
			for _, e := range b.Elements {
				var txt string
				_ = json.Unmarshal(e.Text, &txt)
				if n := width(txt); n > maxText || n == 0 {
					t.Errorf("context text has %d characters", n)
				}
			}
		case "actions":
			if n := len(b.Elements); n == 0 || n > maxPerActions {
				t.Errorf("actions block with %d elements", n)
			}
			for _, e := range b.Elements {
				buttons++
				var txt struct{ Text string }
				_ = json.Unmarshal(e.Text, &txt)
				if n := width(txt.Text); n > maxButtonText || strings.TrimSpace(txt.Text) == "" {
					t.Errorf("button label %q", txt.Text)
				}
				if actionIDs[e.ActionID] || e.ActionID == "" || len(e.ActionID) > 255 {
					t.Errorf("action_id %q is empty, too long or used twice", e.ActionID)
				}
				actionIDs[e.ActionID] = true
				if len(e.Value) > maxValue || len(e.URL) > maxURL || (e.Value == "") == (e.URL == "") {
					t.Errorf("button %q needs exactly one of value and url within limits", txt.Text)
				}
			}
		default:
			t.Errorf("unexpected block type %q", b.Type)
		}
	}
	return buttons
}

func row(n int) []actions.Button {
	out := make([]actions.Button, n)
	for i := range out {
		out[i] = actions.Button{Label: fmt.Sprint(i + 1), Data: fmt.Sprintf("pick|chg_1|%d", i+1)}
	}
	return out
}

func TestRenderLimits(t *testing.T) {
	t.Run("a week of the calendar stays one row", func(t *testing.T) {
		bs := blocks(actions.Message{Title: "📅 New date", Rows: [][]actions.Button{row(7)}}, maxMessageBlocks)
		if n := check(t, bs, maxMessageBlocks); n != 7 || len(bs) != 2 {
			t.Fatalf("7 buttons in one actions block expected, got %d buttons in %d blocks", n, len(bs))
		}
	})
	t.Run("a row over 25 buttons continues in the next block", func(t *testing.T) {
		bs := blocks(actions.Message{Title: "Many", Rows: [][]actions.Button{row(30)}}, maxMessageBlocks)
		if n := check(t, bs, maxMessageBlocks); n != 30 || len(bs) != 3 {
			t.Fatalf("30 buttons in two actions blocks expected, got %d buttons in %d blocks", n, len(bs))
		}
	})
	t.Run("a whole calendar keeps every day", func(t *testing.T) {
		m := actions.Message{Title: "📅 New date for Netflix", Lines: []string{"💶 €19.99"}}
		m.Rows = append(m.Rows, row(3))
		for range 6 {
			m.Rows = append(m.Rows, row(7))
		}
		m.Rows = append(m.Rows, []actions.Button{{Label: "↩︎ Back", Data: "back|chg_1"}})
		// Same data in every week is fine for the layout; ids must still differ.
		if n := check(t, blocks(m, maxMessageBlocks), maxMessageBlocks); n != 46 {
			t.Fatalf("46 buttons expected, got %d", n)
		}
	})
	t.Run("more rows than a message takes keep the way home", func(t *testing.T) {
		m := actions.Message{Title: "📋 Subscriptions"}
		for i := range 70 {
			m.Rows = append(m.Rows, []actions.Button{{Label: fmt.Sprintf("Sub %d", i), Data: fmt.Sprintf("a|sub|%d", i)}})
		}
		m.Rows = append(m.Rows, []actions.Button{{Label: "🏠 Home", Data: "a|home"}})
		bs := blocks(m, maxMessageBlocks)
		check(t, bs, maxMessageBlocks)
		out := blocksJSON(t, bs)
		if len(bs) != maxMessageBlocks || !strings.Contains(out, "a|home") || !strings.Contains(out, "more than Slack can show") {
			t.Fatalf("expected %d blocks ending in a notice and Home, got %d", maxMessageBlocks, len(bs))
		}
		if home := blocks(m, maxHomeBlocks); len(home) != 72 {
			t.Fatalf("the Home tab takes 100 blocks: nothing should be dropped, got %d", len(home))
		}
	})
	t.Run("long texts are shortened or split", func(t *testing.T) {
		long := strings.Repeat("Netflix & chill <3 ", 400) // 7600 characters
		m := actions.Message{
			Title:  strings.Repeat("Very long title ", 20),
			Lines:  append([]string{long}, strings.Split(strings.Repeat("a line of text\n", 500), "\n")...),
			Footer: long,
			Rows: [][]actions.Button{{
				{Label: strings.Repeat("😀", 60), Data: "a|home"},
				{Label: " ", Data: "a|ad|2026-11"},
				{Label: "too much data", Data: strings.Repeat("x", 2001)},
				{Label: "too long a link", URL: "https://example.com/" + strings.Repeat("x", 3000)},
			}},
		}
		for range 120 {
			m.Sections = append(m.Sections, actions.Section{}) // empty sections add nothing
		}
		table := actions.Section{Heading: "Big table"}
		for i := range 200 {
			table.Table = append(table.Table, []string{fmt.Sprintf("Subscription number %d", i), "€12.99", "```"})
		}
		m.Sections = append(m.Sections, table)
		bs := blocks(m, maxMessageBlocks)
		if n := check(t, bs, maxMessageBlocks); n != 2 {
			t.Fatalf("the two sendable buttons expected, got %d", n)
		}
		out := blocksJSON(t, bs)
		if strings.Contains(out, "&am…") || strings.Contains(out, "&l…") || strings.Contains(out, "<3") {
			t.Fatal("text was cut inside an entity or left unescaped")
		}
		if strings.Count(out, "```") < 4 || strings.Count(out, "```")%2 != 0 {
			t.Fatalf("the table should be split into whole code blocks, got %d fences", strings.Count(out, "```"))
		}
		if !strings.Contains(out, `"text": "·"`) {
			t.Fatal("a blank label should become a dot")
		}
	})
	t.Run("an empty message still renders", func(t *testing.T) {
		if bs := blocks(actions.Message{}, maxMessageBlocks); len(bs) != 1 {
			t.Fatalf("one placeholder block expected, got %d", len(bs))
		}
	})
}

func TestRenderEscapesAndAdaptsText(t *testing.T) {
	m := actions.Message{
		Title:  "⚠️ That name is too long.",
		Lines:  []string{"Try again, or send /home to leave it.", "AT&T <!channel> /add", "see http://x.com/add and /unknown"},
		Footer: "That took too long. Send /add to start again.",
	}
	out := blocksJSON(t, blocks(m, maxMessageBlocks))
	for _, want := range []string{"send /exhale home to leave", "AT&amp;T &lt;!channel&gt; /exhale add", "http://x.com/add and /unknown", "Send /exhale add to start"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if got := fallback(reminder); got != "🔔 Netflix renews in 3 days — 💶 €19.99 · Sun, 8 Nov 2026" {
		t.Errorf("fallback text: %q", got)
	}
}

func TestUnwrapTypedText(t *testing.T) {
	for in, want := range map[string]string{
		"  Netflix &amp; chill ":                         "Netflix & chill",
		"<https://www.netflix.com/cancelplan>":           "https://www.netflix.com/cancelplan",
		"<https://netflix.com|netflix.com>":              "https://netflix.com",
		"<mailto:alex@example.com|alex@example.com>":     "alex@example.com",
		"12.99 &lt; 13":                                  "12.99 < 13",
		"link <https://exhale.example/x?a=1&amp;b=2|me>": "link https://exhale.example/x?a=1&b=2",
	} {
		if got := unwrap(in); got != want {
			t.Errorf("unwrap(%q) = %q, want %q", in, got, want)
		}
	}
}
