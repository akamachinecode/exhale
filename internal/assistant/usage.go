package assistant

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/auth"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// What the assistant costs (ADR 0018, safety rule 6). Each question and
// each import read by a model is recorded: who asked, where, the provider
// and model, how many calls, the tokens and an estimated cost. Never the
// words. The cost comes from a small price table, which the owner can
// override per model (a local model costs nothing; a new one is not in
// the table). A model without a price gets tokens only: no invented cost.

// Price is what a model costs, in US dollars per million tokens, as
// decimal text ("4", "0.10").
type Price struct {
	Input  string `json:"input" doc:"US dollars per million input tokens, e.g. 4 or 0.10"`
	Output string `json:"output" doc:"US dollars per million output tokens, e.g. 20 or 0.50"`
}

// defaultPrices are Anthropic's list prices (checked 2026-11-10). Prices
// of other providers change too often and vary by reseller: owners add
// the ones they use in Settings.
var defaultPrices = map[string]Price{
	"claude-fable-5-2":  {"8", "40"},
	"claude-fable-5-1":  {"10", "50"},
	"claude-fable-5":    {"10", "50"},
	"claude-opus-5-5":   {"4", "20"},
	"claude-opus-5":     {"5", "25"},
	"claude-opus-4-8":   {"5", "25"},
	"claude-opus-4-7":   {"5", "25"},
	"claude-opus-4-6":   {"5", "25"},
	"claude-sonnet-5-5": {"2", "10"},
	"claude-sonnet-5":   {"2", "10"},
	"claude-sonnet-4-6": {"3", "15"},
	"claude-haiku-5-5":  {"0.10", "0.50"},
	"claude-haiku-4-5":  {"1", "5"},
}

const settingPrices = "assistant_prices" // JSON: model → Price

// usageKeep is how long usage rows are kept: a year and a month, so the
// same month last year can be compared.
const usageKeep = 400 * 24 * time.Hour

var priceRe = regexp.MustCompile(`^(\d{1,6})(?:\.(\d{1,6}))?$`)

// micros parses a price in dollars ("4", "0.10") into millionths of a
// dollar.
func micros(s string) (int64, bool) {
	m := priceRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	w, _ := strconv.ParseInt(m[1], 10, 64)
	f, _ := strconv.ParseInt((m[2] + "000000")[:6], 10, 64)
	return w*1_000_000 + f, true
}

// usd writes millionths of a dollar as dollars with up to six decimals.
func usd(m int64) string {
	s := fmt.Sprintf("%d.%06d", m/1_000_000, m%1_000_000)
	s = strings.TrimRight(s, "0")
	return strings.TrimSuffix(s, ".")
}

// prices returns the owner's prices, by model.
func (a *Assistant) prices(ctx context.Context) map[string]Price {
	out := map[string]Price{}
	if raw := a.setting(ctx, settingPrices); raw != "" {
		_ = json.Unmarshal([]byte(raw), &out)
	}
	return out
}

// priceFor returns the price of a model and where it comes from:
// "settings", "default", or "" when it is not known.
func (a *Assistant) priceFor(ctx context.Context, model string) (Price, string) {
	if p, ok := a.prices(ctx)[model]; ok {
		return p, "settings"
	}
	// A dated or suffixed id ("claude-opus-5-5-20261101") has the price of
	// its model; the longest known prefix wins.
	best := ""
	for id := range defaultPrices {
		if (model == id || strings.HasPrefix(model, id+"-")) && len(id) > len(best) {
			best = id
		}
	}
	if best == "" {
		return Price{}, ""
	}
	return defaultPrices[best], "default"
}

// cost estimates what usage cost with a model, in millionths of a dollar.
// ok is false when the price is not known.
func (a *Assistant) cost(ctx context.Context, model string, u Usage) (int64, bool) {
	p, src := a.priceFor(ctx, model)
	if src == "" {
		return 0, false
	}
	in, ok1 := micros(p.Input)
	out, ok2 := micros(p.Output)
	if !ok1 || !ok2 {
		return 0, false
	}
	// Per million tokens: round to the nearest millionth of a dollar.
	return (int64(u.Input)*in + int64(u.Output)*out + 500_000) / 1_000_000, true
}

// usageRecord is one question or one import, as it is recorded.
type usageRecord struct {
	kind     string // question or import
	surface  string // web, telegram, slack, import
	provider string
	model    string
	rounds   int
	usage    Usage
	failed   bool
}

// surfaceOf says where a conversation happens, from its id
// ("web_mem_…", "telegram_42").
func surfaceOf(conv string) string {
	s, _, _ := strings.Cut(conv, "_")
	if s == "" {
		return "web"
	}
	return s
}

// record keeps what a question cost and logs it, without the words. A
// question that never reached the model is not recorded.
func (a *Assistant) record(ctx context.Context, actor app.Actor, r usageRecord) {
	if r.rounds == 0 {
		return
	}
	if r.model == "" {
		r.model = a.Config(ctx).Model
	}
	if r.model == "" {
		r.model = r.provider
	}
	c, priced := a.cost(ctx, r.model, r.usage)
	var costPtr *int64
	attrs := []any{"kind", r.kind, "surface", r.surface, "provider", r.provider, "model", r.model, "rounds", r.rounds, "input_tokens", r.usage.Input, "output_tokens", r.usage.Output}
	if priced {
		costPtr = &c
		attrs = append(attrs, "cost_usd", usd(c))
	}
	a.log.Info("assistant usage", attrs...)
	now := a.svc.Now().UTC()
	failed := int64(0)
	if r.failed {
		failed = 1
	}
	q := a.svc.Store().Q
	if err := q.InsertAssistantUsage(context.WithoutCancel(ctx), db.InsertAssistantUsageParams{
		ID: "use_" + auth.Random(10), HouseholdID: actor.HouseholdID, MemberID: actor.MemberID, At: store.FormatTime(now),
		Kind: r.kind, Surface: r.surface, Provider: r.provider, Model: r.model, Rounds: int64(r.rounds),
		InputTokens: int64(r.usage.Input), OutputTokens: int64(r.usage.Output), CostMicros: costPtr, Failed: failed,
	}); err != nil {
		a.log.Warn("assistant: could not record usage", "err", err)
	}
	_ = q.DeleteAssistantUsageBefore(context.WithoutCancel(ctx), store.FormatTime(now.Add(-usageKeep)))
}

// UsageEntry is one question (or import) in the activity timeline.
type UsageEntry struct {
	ID           string    `json:"id"`
	At           time.Time `json:"at"`
	Member       string    `json:"member" doc:"who asked"`
	Kind         string    `json:"kind" enum:"question,import"`
	Surface      string    `json:"surface" doc:"web, telegram, slack or import"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	Rounds       int       `json:"rounds" doc:"calls to the model"`
	InputTokens  int       `json:"inputTokens"`
	OutputTokens int       `json:"outputTokens"`
	CostUSD      *string   `json:"costUsd,omitempty" doc:"estimated cost in US dollars; absent when the model's price is not known"`
	Failed       bool      `json:"failed"`
}

// UsageList is the newest entries first.
type UsageList struct {
	Entries []UsageEntry `json:"entries"`
}

// Usage lists what the assistant was asked, newest first. An owner sees
// the whole household (they pay the provider); a member sees their own.
func (a *Assistant) Usage(ctx context.Context, actor app.Actor, owner bool, limit int) (UsageList, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	member := actor.MemberID
	if owner {
		member = ""
	}
	rows, err := a.svc.Store().Q.ListAssistantUsage(ctx, db.ListAssistantUsageParams{HouseholdID: actor.HouseholdID, MemberID: member, Lim: int64(limit)})
	if err != nil {
		return UsageList{}, err
	}
	out := UsageList{Entries: []UsageEntry{}}
	for _, r := range rows {
		e := UsageEntry{
			ID: r.ID, At: store.ParseTime(r.At), Member: r.MemberName, Kind: r.Kind, Surface: r.Surface, Provider: r.Provider, Model: r.Model,
			Rounds: int(r.Rounds), InputTokens: int(r.InputTokens), OutputTokens: int(r.OutputTokens), Failed: r.Failed != 0,
		}
		if r.CostMicros != nil {
			e.CostUSD = new(usd(*r.CostMicros))
		}
		out.Entries = append(out.Entries, e)
	}
	return out, nil
}

// MonthUsage is the total of one month.
type MonthUsage struct {
	Month        string  `json:"month" doc:"e.g. 2026-11, in the household's time zone"`
	Questions    int     `json:"questions" doc:"questions and imports read by a model"`
	InputTokens  int     `json:"inputTokens"`
	OutputTokens int     `json:"outputTokens"`
	CostUSD      *string `json:"costUsd,omitempty" doc:"estimated cost in US dollars of the entries whose price is known"`
	Unpriced     int     `json:"unpriced" doc:"entries whose model has no price: their tokens count, their cost does not"`
}

// ThisMonth totals the household's usage since the first of the month.
func (a *Assistant) ThisMonth(ctx context.Context, actor app.Actor) MonthUsage {
	loc := time.UTC
	if info, err := a.svc.GetHousehold(ctx, actor, struct{}{}); err == nil {
		if l, err := time.LoadLocation(info.Household.Timezone); err == nil {
			loc = l
		}
	}
	now := a.svc.Now().In(loc)
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
	m := MonthUsage{Month: start.Format("2006-01")}
	sum, err := a.svc.Store().Q.SumAssistantUsage(ctx, db.SumAssistantUsageParams{HouseholdID: actor.HouseholdID, Since: store.FormatTime(start), Until: store.FormatTime(start.AddDate(0, 1, 0))})
	if err != nil {
		return m
	}
	m.Questions, m.InputTokens, m.OutputTokens, m.Unpriced = int(sum.Questions), int(sum.InputTokens), int(sum.OutputTokens), int(sum.Unpriced)
	if m.Questions > m.Unpriced {
		m.CostUSD = new(usd(sum.CostMicros))
	}
	return m
}

// setPrice saves the owner's price for a model; an empty price removes it.
func (a *Assistant) setPrice(ctx context.Context, model string, p *Price) error {
	if p == nil || model == "" {
		return nil
	}
	all := a.prices(ctx)
	if strings.TrimSpace(p.Input) == "" && strings.TrimSpace(p.Output) == "" {
		delete(all, model)
	} else {
		if _, ok := micros(p.Input); !ok {
			return domain.Invalid("the input price must be a number of dollars, like 4 or 0.10")
		}
		if _, ok := micros(p.Output); !ok {
			return domain.Invalid("the output price must be a number of dollars, like 20 or 0.50")
		}
		all[model] = Price{Input: strings.TrimSpace(p.Input), Output: strings.TrimSpace(p.Output)}
	}
	b, _ := json.Marshal(all)
	return a.svc.Store().Q.PutSetting(ctx, db.PutSettingParams{Key: settingPrices, Value: string(b)})
}
