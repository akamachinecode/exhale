package slack

import (
	"fmt"
	"regexp"
	"strings"

	slackapi "github.com/slack-go/slack"

	"github.com/akamachinecode/exhale/internal/actions"
)

// Block Kit limits, from docs.slack.dev/reference/block-kit (checked in
// November 2026). Anything over a limit makes Slack reject the whole
// message with invalid_blocks, so the renderer splits or shortens instead.
const (
	maxHeader        = 150  // characters in a header block
	maxText          = 3000 // characters in a section or context text
	maxButtonText    = 75   // characters in a button label
	maxValue         = 2000 // characters in a button value
	maxURL           = 3000 // characters in a button URL
	maxPerActions    = 25   // elements in one actions block
	maxMessageBlocks = 50   // blocks in a message
	maxHomeBlocks    = 100  // blocks in the Home tab
)

// Action ids tell a button that runs an action from one that opens a link.
// Slack sends a block_actions payload for both.
const (
	actionPrefix = "act:"
	urlPrefix    = "url:"
)

var mrkdwnEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// escape makes text safe inside mrkdwn: Slack reads &, < and > as control
// characters (links, mentions, <!channel>).
func escape(s string) string { return mrkdwnEscaper.Replace(s) }

// Screens name commands the Telegram way ("send /home to leave it"). A
// Slack app has one slash command, so here they read "/exhale home".
var slashCommand = func() *regexp.Regexp {
	names := []string{"home"}
	for _, c := range actions.Commands {
		names = append(names, regexp.QuoteMeta(c.Command))
	}
	return regexp.MustCompile(`(^|[\s(“"'])/(` + strings.Join(names, "|") + `)\b`)
}()

func slashify(s string) string { return slashCommand.ReplaceAllString(s, "${1}/exhale ${2}") }

// localize adapts a screen's texts to Slack before rendering.
func localize(m actions.Message) actions.Message {
	m.Title, m.Footer = slashify(m.Title), slashify(m.Footer)
	m.Lines = slashifyAll(m.Lines)
	sections := make([]actions.Section, len(m.Sections))
	for i, s := range m.Sections {
		s.Heading, s.Lines = slashify(s.Heading), slashifyAll(s.Lines)
		sections[i] = s
	}
	m.Sections = sections
	return m
}

func slashifyAll(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = slashify(l)
	}
	return out
}

// width counts characters the way Slack does for its limits. Counting
// UTF-16 units is the conservative reading: an emoji takes two.
func width(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}

// clip shortens s to at most n characters, ending in an ellipsis.
func clip(s string, n int) string {
	if width(s) <= n {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		w := 1
		if r > 0xFFFF {
			w = 2
		}
		if used+w > n-1 {
			break
		}
		b.WriteRune(r)
		used += w
	}
	return b.String() + "…"
}

// clipEscaped is clip for text already escaped: it never cuts an entity
// like &amp; in half.
func clipEscaped(s string, n int) string {
	if width(s) <= n {
		return s
	}
	out := strings.TrimSuffix(clip(s, n), "…")
	if i := strings.LastIndexByte(out, '&'); i >= 0 && !strings.Contains(out[i:], ";") {
		out = out[:i]
	}
	return out + "…"
}

// pack joins lines into as few texts as possible, each within limit.
func pack(lines []string, limit int) []string {
	var out []string
	cur := ""
	for _, l := range lines {
		l = clipEscaped(l, limit)
		switch {
		case cur == "":
			cur = l
		case width(cur)+1+width(l) <= limit:
			cur += "\n" + l
		default:
			out = append(out, cur)
			cur = l
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func plain(s string) *slackapi.TextBlockObject {
	return slackapi.NewTextBlockObject(slackapi.PlainTextType, s, true, false)
}

func mrkdwn(s string) *slackapi.TextBlockObject {
	return slackapi.NewTextBlockObject(slackapi.MarkdownType, s, false, false)
}

func section(text string) slackapi.Block {
	return slackapi.NewSectionBlock(mrkdwn(text), nil, nil)
}

func escapeAll(lines []string) []string {
	out := make([]string, 0, len(lines))
	for _, l := range lines {
		if strings.TrimSpace(l) != "" {
			out = append(out, escape(l))
		}
	}
	return out
}

// codeFence wraps a table in a monospace block. Slack has a table block,
// but one message takes a single table and its availability is still
// moving, so aligned text is the safe rendering (ADR 0019).
const codeFence = "```"

func tableTexts(s actions.Section) []string {
	rows := strings.Split(strings.ReplaceAll(escape(s.TableText()), codeFence, "'''"), "\n")
	var out []string
	for _, chunk := range pack(rows, maxText-2*len(codeFence)-2) {
		out = append(out, codeFence+"\n"+chunk+"\n"+codeFence)
	}
	return out
}

// blocks renders a screen as Block Kit: a header, the lines, each section
// with its heading and table, the footer as context and one actions block
// per row of buttons. limit is the surface's block limit.
func blocks(m actions.Message, limit int) []slackapi.Block {
	m = localize(m)
	var out []slackapi.Block
	if t := strings.TrimSpace(m.Title); t != "" {
		out = append(out, slackapi.NewHeaderBlock(plain(clip(t, maxHeader))))
	}
	for _, text := range pack(escapeAll(m.Lines), maxText) {
		out = append(out, section(text))
	}
	for _, s := range m.Sections {
		lines := escapeAll(s.Lines)
		if h := strings.TrimSpace(s.Heading); h != "" {
			lines = append([]string{"*" + escape(h) + "*"}, lines...)
		}
		texts := pack(lines, maxText)
		if len(s.Table) > 0 {
			tables := tableTexts(s)
			// A short heading shares the block with its table.
			if n := len(texts); n > 0 && len(tables) > 0 && width(texts[n-1])+1+width(tables[0]) <= maxText {
				tables[0] = texts[n-1] + "\n" + tables[0]
				texts = texts[:n-1]
			}
			texts = append(texts, tables...)
		}
		for _, text := range texts {
			out = append(out, section(text))
		}
	}
	if f := strings.TrimSpace(m.Footer); f != "" {
		out = append(out, slackapi.NewContextBlock("", mrkdwn(clipEscaped(escape(f), maxText))))
	}
	out = append(out, actionBlocks(m.Rows)...)
	if len(out) == 0 {
		out = append(out, section("…"))
	}
	return fit(out, limit)
}

// actionBlocks renders each row of buttons as an actions block; a row with
// more buttons than a block takes continues in the next block.
func actionBlocks(rows [][]actions.Button) []slackapi.Block {
	var out []slackapi.Block
	for r, row := range rows {
		var els []slackapi.BlockElement
		for c, b := range row {
			if el := button(b, fmt.Sprintf("%d:%d", r, c)); el != nil {
				els = append(els, el)
			}
		}
		for i := 0; i < len(els); i += maxPerActions {
			id := fmt.Sprintf("row%d", r)
			if i > 0 {
				id = fmt.Sprintf("row%d.%d", r, i/maxPerActions)
			}
			out = append(out, slackapi.NewActionBlock(id, els[i:min(i+maxPerActions, len(els))]...))
		}
	}
	return out
}

var blank = regexp.MustCompile(`^\s*$`)

// button renders one button, or nil when it cannot be sent: a value or URL
// over Slack's limit cannot be shortened without changing what it does.
func button(b actions.Button, id string) *slackapi.ButtonBlockElement {
	label := clip(b.Label, maxButtonText)
	if blank.MatchString(label) {
		label = "·" // Slack rejects empty labels (calendar padding)
	}
	if b.URL != "" {
		if width(b.URL) > maxURL {
			return nil
		}
		el := slackapi.NewButtonBlockElement(urlPrefix+id, "", plain(label))
		el.URL = b.URL
		return el
	}
	if b.Data == "" || width(b.Data) > maxValue {
		return nil
	}
	return slackapi.NewButtonBlockElement(actionPrefix+id, b.Data, plain(label))
}

// fit keeps a screen within the surface's block limit. It drops blocks from
// the middle and says so, keeping the last one: the way back home.
func fit(bs []slackapi.Block, limit int) []slackapi.Block {
	if len(bs) <= limit {
		return bs
	}
	head := append([]slackapi.Block{}, bs[:limit-2]...)
	more := slackapi.NewContextBlock("", mrkdwn(fmt.Sprintf("… %d more than Slack can show here. The web app has everything.", len(bs)-limit+1)))
	return append(head, more, bs[len(bs)-1])
}

// fallback is the plain text of a message: what notifications and screen
// readers get instead of the blocks.
func fallback(m actions.Message) string {
	m = localize(m)
	text := strings.TrimSpace(m.Title)
	if len(m.Lines) > 0 {
		if text != "" {
			text += " — "
		}
		text += m.Lines[0]
	}
	if text == "" {
		text = "Exhale"
	}
	return clipEscaped(escape(text), maxText)
}

func messageOptions(m actions.Message) []slackapi.MsgOption {
	return []slackapi.MsgOption{
		slackapi.MsgOptionText(fallback(m), false),
		slackapi.MsgOptionBlocks(blocks(m, maxMessageBlocks)...),
		slackapi.MsgOptionDisableLinkUnfurl(),
		slackapi.MsgOptionDisableMediaUnfurl(),
	}
}

func homeView(m actions.Message) slackapi.HomeTabViewRequest {
	return slackapi.HomeTabViewRequest{Type: slackapi.VTHomeTab, Blocks: slackapi.Blocks{BlockSet: blocks(m, maxHomeBlocks)}}
}

// Slack sends typed text escaped and with links wrapped: <https://x.com>,
// <https://x.com|x.com>, <mailto:a@b.c|a@b.c>.
var (
	wrapped     = regexp.MustCompile(`<((?:https?://|mailto:)[^|>\s]+)(?:\|([^>]*))?>`)
	unescaper   = strings.NewReplacer("&lt;", "<", "&gt;", ">", "&amp;", "&")
	codePattern = regexp.MustCompile(`^[a-z2-7]{16,40}$`)
)

// unwrap returns what the person typed.
func unwrap(text string) string {
	text = wrapped.ReplaceAllStringFunc(text, func(m string) string {
		p := wrapped.FindStringSubmatch(m)
		if strings.HasPrefix(p[1], "mailto:") {
			return strings.TrimPrefix(p[1], "mailto:")
		}
		return p[1]
	})
	return strings.TrimSpace(unescaper.Replace(text))
}
