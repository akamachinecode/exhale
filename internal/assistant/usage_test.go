package assistant

import (
	"testing"
)

func TestPrices(t *testing.T) {
	for in, want := range map[string]int64{"4": 4_000_000, "0.10": 100_000, "0.5": 500_000, "12.345678": 12_345_678, "0": 0} {
		if got, ok := micros(in); !ok || got != want {
			t.Errorf("micros(%q) = %d %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "-1", "1,5", "abc", "1.2345678", "."} {
		if _, ok := micros(bad); ok {
			t.Errorf("micros(%q) accepted", bad)
		}
	}
	if usd(1_234_500) != "1.2345" || usd(0) != "0" || usd(25) != "0.000025" {
		t.Errorf("usd: %s %s %s", usd(1_234_500), usd(0), usd(25))
	}
}

// TestUsageIsRecorded: each question records the provider, model, calls,
// tokens and an estimated cost; a model without a price gets tokens only;
// the owner's price wins over the table.
func TestUsageIsRecorded(t *testing.T) {
	w := newWorld(t)
	answer := func(model string, in, out int) func(Request) (Response, error) {
		return func(Request) (Response, error) {
			return Response{Text: "Fine.", Stop: StopEnd, Model: model, Usage: Usage{Input: in, Output: out}}, nil
		}
	}
	w.fake.Then(
		answer("claude-haiku-5-5", 10_000, 2_000), // 10k × $0.10/M + 2k × $0.50/M = $0.002
		answer("llama3.1", 5_000, 500),            // no price
		answer("llama3.1", 1_000, 1_000),          // after the owner sets one
	)
	if _, err := w.a.Ask(w.ctx, w.owner, "telegram_42", "hi"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.a.Ask(w.ctx, w.owner, "web_x", "hi"); err != nil {
		t.Fatal(err)
	}
	if err := w.a.setPrice(w.ctx, "llama3.1", &Price{Input: "0", Output: "0"}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.a.Ask(w.ctx, w.owner, "web_x", "hi"); err != nil {
		t.Fatal(err)
	}
	list, err := w.a.Usage(w.ctx, w.owner, true, 10)
	if err != nil || len(list.Entries) != 3 {
		t.Fatalf("usage: %+v %v", list, err)
	}
	by := map[int]UsageEntry{} // the clock is fixed: find them by tokens
	for _, e := range list.Entries {
		by[e.InputTokens] = e
	}
	e := by[10_000]
	if e.Surface != "telegram" || e.Provider != "fake" || e.Model != "claude-haiku-5-5" || e.Rounds != 1 || e.InputTokens != 10_000 || e.CostUSD == nil || *e.CostUSD != "0.002" || e.Member != "Alex" {
		t.Errorf("priced entry: %+v", e)
	}
	if e := by[5_000]; e.CostUSD != nil || e.InputTokens != 5_000 {
		t.Errorf("unpriced entry: %+v", e)
	}
	if e := by[1_000]; e.CostUSD == nil || *e.CostUSD != "0" {
		t.Errorf("free local model: %+v", e)
	}
	m := w.a.ThisMonth(w.ctx, w.owner)
	if m.Questions != 3 || m.InputTokens != 16_000 || m.OutputTokens != 3_500 || m.Unpriced != 1 || m.CostUSD == nil || *m.CostUSD != "0.002" {
		t.Errorf("month: %+v", m)
	}
	// A member sees only their own questions.
	if l, _ := w.a.Usage(w.ctx, w.member("Sam"), false, 10); len(l.Entries) != 0 {
		t.Errorf("a member sees others' questions: %+v", l)
	}
}
