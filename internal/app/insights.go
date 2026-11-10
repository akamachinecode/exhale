package app

import (
	"context"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// --- price history -------------------------------------------------------------

// PricePoint is one price a subscription has had (or will have).
type PricePoint struct {
	From      engine.Date `json:"from" jsonschema:"first charge at this price"`
	Price     money.Money `json:"price"`
	Change    string      `json:"changePercent,omitempty" jsonschema:"change against the previous price, e.g. +14.3"`
	Scheduled bool        `json:"scheduled,omitempty" jsonschema:"a change announced in advance that has not arrived yet"`
}

// PaidTotals is what a subscription has really cost so far.
type PaidTotals struct {
	Count int         `json:"count" jsonschema:"charges marked paid"`
	Total money.Money `json:"total" jsonschema:"sum of paid charges, in the subscription currency"`
	Since engine.Date `json:"since,omitzero" jsonschema:"date of the first paid charge"`
}

// priceHistory folds a subscription's charges (oldest first) into the list
// of prices it has had. It is real history: it comes from the charges, not
// from the current price.
func priceHistory(sub domain.Subscription, charges []domain.Charge, today engine.Date) []PricePoint {
	out := []PricePoint{}
	add := func(from engine.Date, p money.Money, scheduled bool) {
		if n := len(out); n > 0 {
			prev := out[n-1].Price
			if prev == p {
				return
			}
			pt := PricePoint{From: from, Price: p, Scheduled: scheduled}
			if prev.Minor > 0 && prev.Currency == p.Currency {
				pct := new(big.Rat).SetFrac64((p.Minor-prev.Minor)*1000, prev.Minor)
				f, _ := pct.Float64()
				pt.Change = fmt.Sprintf("%+.1f", f/10)
			}
			out = append(out, pt)
			return
		}
		out = append(out, PricePoint{From: from, Price: p, Scheduled: scheduled})
	}
	for _, c := range charges {
		if c.Status == domain.ChargeSkipped {
			continue
		}
		add(c.OriginalDate, c.Amount, c.Status == domain.ChargePending && c.OriginalDate.After(today) && sub.NextPrice != nil && !c.OriginalDate.Before(sub.NextPrice.From))
	}
	if len(out) == 0 {
		add(sub.Schedule.First(), sub.Price, false)
	}
	if sub.NextPrice != nil {
		add(sub.NextPrice.From, sub.NextPrice.Price, true)
	}
	return out
}

func paidTotals(sub domain.Subscription, charges []domain.Charge) PaidTotals {
	t := PaidTotals{Total: money.Money{Currency: sub.Price.Currency}}
	for _, c := range charges {
		if c.Status != domain.ChargePaid {
			continue
		}
		amount := c.Amount
		if c.ChargedAmount.Currency == amount.Currency && !c.ChargedAmount.IsZero() {
			amount = c.ChargedAmount
		}
		if sum, err := t.Total.Add(amount); err == nil {
			t.Total = sum
		}
		t.Count++
		if t.Since.IsZero() || c.DueDate.Before(t.Since) {
			t.Since = c.DueDate
		}
	}
	return t
}

// --- spending history ----------------------------------------------------------

// SpendingHistoryInput selects how many months to look back and ahead.
type SpendingHistoryInput struct {
	Months int `json:"months,omitempty" query:"months" jsonschema:"months to look back and ahead, default 12" minimum:"1" maximum:"60"`
}

// MonthSpend is the money of one calendar month.
type MonthSpend struct {
	Month    string      `json:"month" jsonschema:"YYYY-MM"`
	Paid     money.Money `json:"paid" jsonschema:"charges marked paid that month, in the display currency"`
	Expected money.Money `json:"expected" jsonschema:"charges still to come that month (pending or projected from the schedule), in the display currency"`
	Count    int         `json:"count" jsonschema:"paid plus expected charges"`
}

// SubscriptionSpend is what one subscription has really cost.
type SubscriptionSpend struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Paid  money.Money `json:"paid" jsonschema:"sum of paid charges, in the display currency"`
	Count int         `json:"count"`
	Since engine.Date `json:"since,omitzero"`
}

// SpendingHistory is real spend per month and per subscription.
type SpendingHistory struct {
	MainCurrency   string              `json:"mainCurrency"`
	Months         []MonthSpend        `json:"months" jsonschema:"oldest first; past months show what was paid, future months what is expected"`
	BySubscription []SubscriptionSpend `json:"bySubscription" jsonschema:"lifetime paid per subscription, largest first"`
	TotalPaid      money.Money         `json:"totalPaid" jsonschema:"everything marked paid in the months shown"`
	HeaviestMonth  string              `json:"heaviestMonth,omitempty" jsonschema:"upcoming month with the most to pay, YYYY-MM"`
	Unconverted    []string            `json:"unconverted" jsonschema:"currencies without a known rate, left out"`
}

func monthKey(d engine.Date) string { return d.String()[:7] }

// SpendHistory returns what was really paid each month (from the charge
// ledger, not from current prices) and what is expected in the coming
// months.
func (s *Service) SpendHistory(ctx context.Context, actor Actor, in SpendingHistoryInput) (SpendingHistory, error) {
	n := in.Months
	if n <= 0 || n > 60 {
		n = 12
	}
	q := s.st.Q
	h, err := s.household(ctx, q, actor.HouseholdID)
	if err != nil {
		return SpendingHistory{}, err
	}
	today := s.today(ctx, q, actor)
	zero := money.Money{Currency: h.Currency}
	out := SpendingHistory{MainCurrency: h.Currency, Months: []MonthSpend{}, BySubscription: []SubscriptionSpend{}, TotalPaid: zero, Unconverted: []string{}}
	first := engine.MustDate(today.Year(), today.Month(), 1)
	start := first
	for range n - 1 {
		start = engine.MustDate(start.AddDays(-1).Year(), start.AddDays(-1).Month(), 1)
	}
	months := map[string]*MonthSpend{}
	end := start
	for range 2 * n {
		k := monthKey(end)
		months[k] = &MonthSpend{Month: k, Paid: zero, Expected: zero}
		end = end.AddDays(engine.DaysIn(end.Year(), end.Month()))
	}
	end = end.AddDays(-1)
	missing := map[string]bool{}
	convs := map[string]*converter{}
	conv := func(m money.Money, day engine.Date) (money.Money, bool) {
		if day.After(today) {
			day = today
		}
		k := monthKey(day)
		if convs[k] == nil {
			convs[k] = s.converter(h.Currency, day.String())
		}
		v, ok := convs[k].toMain(ctx, m)
		if !ok {
			missing[m.Currency] = true
		}
		return v, ok
	}
	list, err := s.ListSubscriptions(ctx, actor, ListSubscriptionsInput{})
	if err != nil {
		return out, err
	}
	horizon := today.AddDays(HorizonDays)
	for _, v := range list.Subscriptions {
		rows, err := q.ListChargesForSubscription(ctx, db.ListChargesForSubscriptionParams{SubscriptionID: v.ID, Limit: 2000})
		if err != nil {
			return out, err
		}
		spend := SubscriptionSpend{ID: v.ID, Name: v.Name, Paid: zero}
		for _, r := range rows {
			c := store.ChargeFromRow(r)
			amount := c.Amount
			if !c.ChargedAmount.IsZero() {
				amount = c.ChargedAmount
			}
			m := months[monthKey(c.DueDate)]
			switch c.Status {
			case domain.ChargePaid:
				val, ok := conv(amount, c.DueDate)
				if !ok {
					continue
				}
				spend.Paid, _ = spend.Paid.Add(val)
				spend.Count++
				if spend.Since.IsZero() || c.DueDate.Before(spend.Since) {
					spend.Since = c.DueDate
				}
				if m != nil {
					m.Paid, _ = m.Paid.Add(val)
					m.Count++
					out.TotalPaid, _ = out.TotalPaid.Add(val)
				}
			case domain.ChargePending, domain.ChargeFailed:
				if val, ok := conv(c.Amount, c.DueDate); ok && m != nil && !c.DueDate.Before(today) {
					m.Expected, _ = m.Expected.Add(val)
					m.Count++
				}
			case domain.ChargeSkipped: // nothing was or will be paid
			}
		}
		if spend.Count > 0 {
			out.BySubscription = append(out.BySubscription, spend)
		}
		// Beyond the materialized horizon, project from the schedule.
		if v.Status == domain.StatusActive {
			for _, d := range v.Schedule.Between(horizon.AddDays(1), end) {
				if val, ok := conv(v.PriceOn(d), d); ok && months[monthKey(d)] != nil {
					m := months[monthKey(d)]
					m.Expected, _ = m.Expected.Add(val)
					m.Count++
				}
			}
		}
	}
	var heaviest int64
	for _, k := range sortedKeys(months) {
		m := *months[k]
		out.Months = append(out.Months, m)
		if k >= monthKey(today) && m.Expected.Minor > heaviest {
			heaviest, out.HeaviestMonth = m.Expected.Minor, k
		}
	}
	slices.SortFunc(out.BySubscription, func(a, b SubscriptionSpend) int { return int(b.Paid.Minor - a.Paid.Minor) })
	out.Unconverted = sortedKeys(missing)
	return out, nil
}

// --- who owes whom --------------------------------------------------------------

// BalancesInput selects the month to settle.
type BalancesInput struct {
	Month string `json:"month,omitempty" query:"month" jsonschema:"YYYY-MM, default the current month"`
}

// MemberBalance is one member's position for the month.
type MemberBalance struct {
	MemberID string      `json:"memberId"`
	Name     string      `json:"name"`
	Paid     money.Money `json:"paid" jsonschema:"what this member's payment methods paid for shared subscriptions"`
	Share    money.Money `json:"share" jsonschema:"what this member should carry"`
	Net      money.Money `json:"net" jsonschema:"paid minus share: positive is owed money, negative owes money"`
}

// Transfer settles part of the month between two members.
type Transfer struct {
	From   string      `json:"from" jsonschema:"member id who pays"`
	To     string      `json:"to" jsonschema:"member id who receives"`
	Amount money.Money `json:"amount"`
	Text   string      `json:"text" jsonschema:"e.g. Ana pays Alex 12.50 EUR"`
}

// SharedCharge is one split charge of the month.
type SharedCharge struct {
	Subscription string      `json:"subscription"`
	Date         engine.Date `json:"date"`
	Amount       money.Money `json:"amount" jsonschema:"in the display currency"`
	PaidBy       string      `json:"paidBy"`
	Status       string      `json:"status"`
}

// Balances says who owes whom for a month of shared subscriptions.
type Balances struct {
	Month        string          `json:"month"`
	MainCurrency string          `json:"mainCurrency"`
	Members      []MemberBalance `json:"members"`
	Transfers    []Transfer      `json:"transfers" jsonschema:"the fewest payments that settle the month"`
	Charges      []SharedCharge  `json:"charges" jsonschema:"the split charges behind the numbers (paid and still pending this month)"`
}

// MonthBalances computes who owes whom for the charges of one month of the
// subscriptions that have cost shares. The payer of a subscription is who
// actually paid; shares say who should carry it.
func (s *Service) MonthBalances(ctx context.Context, actor Actor, in BalancesInput) (Balances, error) {
	q := s.st.Q
	h, err := s.household(ctx, q, actor.HouseholdID)
	if err != nil {
		return Balances{}, err
	}
	today := s.today(ctx, q, actor)
	month := in.Month
	if month == "" {
		month = monthKey(today)
	}
	from, err := engine.ParseDate(month + "-01")
	if err != nil {
		return Balances{}, domain.Invalid("month must look like 2026-11")
	}
	to := from.AddDays(engine.DaysIn(from.Year(), from.Month()) - 1)
	zero := money.Money{Currency: h.Currency}
	out := Balances{Month: month, MainCurrency: h.Currency, Members: []MemberBalance{}, Transfers: []Transfer{}, Charges: []SharedCharge{}}
	members, err := q.ListMembers(ctx, h.ID)
	if err != nil {
		return out, err
	}
	bal := map[string]*MemberBalance{}
	for _, m := range members {
		bal[m.ID] = &MemberBalance{MemberID: m.ID, Name: m.Name, Paid: zero, Share: zero, Net: zero}
	}
	list, err := s.ListSubscriptions(ctx, actor, ListSubscriptionsInput{})
	if err != nil {
		return out, err
	}
	conv := s.converter(h.Currency, minDate(to, today).String())
	for _, v := range list.Subscriptions {
		if len(v.Shares) == 0 || bal[v.PayerID] == nil {
			continue
		}
		total := 0
		for _, sh := range v.Shares {
			total += sh.Weight
		}
		rows, err := q.ListChargesForSubscription(ctx, db.ListChargesForSubscriptionParams{SubscriptionID: v.ID, Limit: 2000})
		if err != nil {
			return out, err
		}
		for _, r := range rows {
			c := store.ChargeFromRow(r)
			if c.DueDate.Before(from) || c.DueDate.After(to) || c.Status == domain.ChargeSkipped || c.Status == domain.ChargeFailed {
				continue
			}
			amount, ok := conv.toMain(ctx, c.Amount)
			if !ok {
				continue
			}
			out.Charges = append(out.Charges, SharedCharge{Subscription: v.Name, Date: c.DueDate, Amount: amount, PaidBy: bal[v.PayerID].Name, Status: string(c.Status)})
			bal[v.PayerID].Paid, _ = bal[v.PayerID].Paid.Add(amount)
			// Split in minor units; the remainder goes to the first shares so
			// the parts always add up to the amount.
			rest := amount.Minor
			for i, sh := range v.Shares {
				part := amount.Minor * int64(sh.Weight) / int64(total)
				if i == len(v.Shares)-1 {
					part = rest
				}
				rest -= part
				if b := bal[sh.MemberID]; b != nil {
					b.Share.Minor += part
				}
			}
		}
	}
	slices.SortFunc(out.Charges, func(a, b SharedCharge) int { return a.Date.Compare(b.Date) })
	type pos struct {
		id  string
		net int64
	}
	var debt, credit []pos
	for _, m := range members {
		b := bal[m.ID]
		b.Net.Minor = b.Paid.Minor - b.Share.Minor
		out.Members = append(out.Members, *b)
		switch {
		case b.Net.Minor < 0:
			debt = append(debt, pos{m.ID, -b.Net.Minor})
		case b.Net.Minor > 0:
			credit = append(credit, pos{m.ID, b.Net.Minor})
		}
	}
	slices.SortFunc(debt, func(a, b pos) int { return int(b.net - a.net) })
	slices.SortFunc(credit, func(a, b pos) int { return int(b.net - a.net) })
	for i, j := 0, 0; i < len(debt) && j < len(credit); {
		amt := min(debt[i].net, credit[j].net)
		m := money.Money{Minor: amt, Currency: h.Currency}
		out.Transfers = append(out.Transfers, Transfer{From: debt[i].id, To: credit[j].id, Amount: m,
			Text: fmt.Sprintf("%s pays %s %s", bal[debt[i].id].Name, bal[credit[j].id].Name, m)})
		debt[i].net -= amt
		credit[j].net -= amt
		if debt[i].net == 0 {
			i++
		}
		if credit[j].net == 0 {
			j++
		}
	}
	return out, nil
}

func minDate(a, b engine.Date) engine.Date {
	if a.Before(b) {
		return a
	}
	return b
}

// --- audit -----------------------------------------------------------------------

// SubscriptionRef names a subscription in a finding.
type SubscriptionRef struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Payer   string      `json:"payer,omitempty"`
	Monthly money.Money `json:"monthly" jsonschema:"in the subscription currency"`
}

// DuplicateGroup is the same service paid more than once.
type DuplicateGroup struct {
	Service       string            `json:"service"`
	Subscriptions []SubscriptionRef `json:"subscriptions"`
}

// PriceIncrease is a price that went up in the last year.
type PriceIncrease struct {
	Subscription SubscriptionRef `json:"subscription"`
	From         money.Money     `json:"from"`
	To           money.Money     `json:"to"`
	Since        engine.Date     `json:"since"`
	Change       string          `json:"changePercent"`
	YearlyDelta  money.Money     `json:"yearlyDelta" jsonschema:"extra cost per year at the new price, in the subscription currency"`
}

// CardImpact is a payment method about to expire and what depends on it.
type CardImpact struct {
	PaymentMethod domain.PaymentMethod `json:"paymentMethod"`
	Subscriptions []SubscriptionRef    `json:"subscriptions" jsonschema:"active subscriptions that will fail when it expires"`
}

// UnusedSubscription is one the member said they no longer use.
type UnusedSubscription struct {
	Subscription SubscriptionRef `json:"subscription"`
	Yearly       money.Money     `json:"yearly" jsonschema:"what it costs per year, in the subscription currency"`
	AnsweredOn   engine.Date     `json:"answeredOn"`
}

// AuditReport lists facts worth acting on. Everything here is computed, not
// guessed: an agent can turn it into advice.
type AuditReport struct {
	Duplicates      []DuplicateGroup     `json:"duplicates" jsonschema:"the same service in more than one active subscription"`
	PriceIncreases  []PriceIncrease      `json:"priceIncreases" jsonschema:"prices that went up in the last 12 months"`
	TrialsEnding    []ChargeView         `json:"trialsEnding" jsonschema:"free trials that turn into a charge within 14 days"`
	CancelDeadlines []ChargeView         `json:"cancelDeadlines" jsonschema:"last days to cancel within 30 days that have no decision yet"`
	Expiring        []ChargeView         `json:"expiring" jsonschema:"manual renewals that expire within 60 days"`
	BigCharges      []ChargeView         `json:"bigCharges" jsonschema:"yearly or rarer charges due within 30 days"`
	CardsExpiring   []CardImpact         `json:"cardsExpiring" jsonschema:"payment methods expiring within 45 days and the subscriptions that use them"`
	Unconfirmed     []ChargeView         `json:"unconfirmed" jsonschema:"charges whose date passed without being confirmed"`
	NotUsed         []UnusedSubscription `json:"notUsed" jsonschema:"subscriptions the member said they no longer use"`
	Findings        int                  `json:"findings" jsonschema:"total number of findings"`
}

// serviceKey normalises a name so "Spotify Family" and "spotify" match.
func serviceKey(name string) string {
	fields := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	plan := map[string]bool{"family": true, "premium": true, "plus": true, "pro": true, "basic": true, "standard": true, "duo": true, "individual": true, "student": true, "annual": true, "monthly": true, "yearly": true, "plan": true, "subscription": true}
	kept := fields[:0:0]
	for _, f := range fields {
		if !plan[f] {
			kept = append(kept, f)
		}
	}
	if len(kept) == 0 {
		kept = fields
	}
	return strings.Join(kept, "")
}

func ref(v SubscriptionView) SubscriptionRef {
	return SubscriptionRef{ID: v.ID, Name: v.Name, Payer: v.PayerName, Monthly: v.MonthlyCost}
}

// Audit reports duplicates, price increases, trials about to charge,
// cancellation deadlines, things about to expire, big charges coming, cards
// about to expire with what depends on them, unconfirmed charges and
// subscriptions marked as not used.
func (s *Service) Audit(ctx context.Context, actor Actor, _ struct{}) (AuditReport, error) {
	q := s.st.Q
	today := s.today(ctx, q, actor)
	out := AuditReport{Duplicates: []DuplicateGroup{}, PriceIncreases: []PriceIncrease{}, TrialsEnding: []ChargeView{}, CancelDeadlines: []ChargeView{},
		Expiring: []ChargeView{}, BigCharges: []ChargeView{}, CardsExpiring: []CardImpact{}, Unconfirmed: []ChargeView{}, NotUsed: []UnusedSubscription{}}
	list, err := s.ListSubscriptions(ctx, actor, ListSubscriptionsInput{Status: string(domain.StatusActive)})
	if err != nil {
		return out, err
	}
	groups := map[string][]SubscriptionRef{}
	byMethod := map[string][]SubscriptionRef{}
	yearAgo := today.AddDays(-365)
	for _, v := range list.Subscriptions {
		key := serviceKey(v.Name)
		groups[key] = append(groups[key], ref(v))
		if v.PaymentMethodID != "" {
			byMethod[v.PaymentMethodID] = append(byMethod[v.PaymentMethodID], ref(v))
		}
		if v.Usage != nil && !v.Usage.Using {
			out.NotUsed = append(out.NotUsed, UnusedSubscription{Subscription: ref(v), Yearly: v.YearlyCost, AnsweredOn: engine.DateOf(v.Usage.At)})
		}
		rows, err := q.ListChargesForSubscription(ctx, db.ListChargesForSubscriptionParams{SubscriptionID: v.ID, Limit: 2000})
		if err != nil {
			return out, err
		}
		charges := make([]domain.Charge, 0, len(rows))
		for _, r := range slices.Backward(rows) {
			charges = append(charges, store.ChargeFromRow(r))
		}
		hist := priceHistory(v.Subscription, charges, today)
		for i := 1; i < len(hist); i++ {
			prev, cur := hist[i-1], hist[i]
			if cur.Price.Minor <= prev.Price.Minor || cur.From.Before(yearAgo) {
				continue
			}
			delta := money.Money{Minor: cur.Price.Minor - prev.Price.Minor, Currency: cur.Price.Currency}
			perMonth, _ := delta.Scale(monthlyFactor(v.Schedule.Current(cur.From)))
			yearly, _ := perMonth.Scale("12")
			out.PriceIncreases = append(out.PriceIncreases, PriceIncrease{Subscription: ref(v), From: prev.Price, To: cur.Price, Since: cur.From, Change: cur.Change, YearlyDelta: yearly})
		}
		for _, c := range charges {
			if c.Status != domain.ChargePending || c.DueDate.Before(today) {
				continue
			}
			cv := s.chargeView(c, v, today)
			days := today.DaysUntil(c.DueDate)
			rule := v.Schedule.Current(c.OriginalDate)
			switch {
			case cv.Kind == ChargeTrial && days <= 14:
				out.TrialsEnding = append(out.TrialsEnding, cv)
			case cv.Kind == ChargeExpiry && days <= 60:
				out.Expiring = append(out.Expiring, cv)
			case cv.Kind == ChargeRenewal && days <= 30 && (rule.Unit == engine.Year || (rule.Unit == engine.Month && rule.Interval >= 6)):
				out.BigCharges = append(out.BigCharges, cv)
			}
			if !cv.CancelBy.IsZero() && c.Decision == "" && !cv.CancelBy.Before(today) && today.DaysUntil(cv.CancelBy) <= 30 {
				out.CancelDeadlines = append(out.CancelDeadlines, cv)
			}
		}
	}
	for _, key := range sortedKeys(groups) {
		if g := groups[key]; len(g) > 1 && key != "" {
			out.Duplicates = append(out.Duplicates, DuplicateGroup{Service: g[0].Name, Subscriptions: g})
		}
	}
	att, err := s.NeedsAttention(ctx, actor, struct{}{})
	if err != nil {
		return out, err
	}
	out.Unconfirmed = att.Unconfirmed
	for _, pm := range att.ExpiringPaymentMethods {
		subs := byMethod[pm.ID]
		if subs == nil {
			subs = []SubscriptionRef{}
		}
		out.CardsExpiring = append(out.CardsExpiring, CardImpact{PaymentMethod: pm, Subscriptions: subs})
	}
	byDue := func(a, b ChargeView) int { return a.DueDate.Compare(b.DueDate) }
	slices.SortFunc(out.TrialsEnding, byDue)
	slices.SortFunc(out.CancelDeadlines, func(a, b ChargeView) int { return a.CancelBy.Compare(b.CancelBy) })
	slices.SortFunc(out.Expiring, byDue)
	slices.SortFunc(out.BigCharges, byDue)
	out.Findings = len(out.Duplicates) + len(out.PriceIncreases) + len(out.TrialsEnding) + len(out.CancelDeadlines) + len(out.Expiring) +
		len(out.BigCharges) + len(out.CardsExpiring) + len(out.Unconfirmed) + len(out.NotUsed)
	return out, nil
}

// usageStale reports whether it is time to ask "still using this?" again.
func usageStale(sub domain.Subscription, now time.Time) bool {
	if now.Sub(sub.CreatedAt) < 60*24*time.Hour {
		return false // too new to doubt
	}
	return sub.Usage == nil || now.Sub(sub.Usage.At) > 180*24*time.Hour
}
