package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/merchant"
	"github.com/akamachinecode/exhale/internal/money"
)

// Bank matching without a bank connection: the transactions of an imported
// statement are compared with the charges Exhale expects. A charge and a
// transaction match when the merchant is the subscription, the date is near
// the due date and the amount is close. Nothing is changed here: the person
// (or an agent) confirms each match with set_charge_status.

// StatementLine is one outgoing payment of a bank or card statement.
type StatementLine struct {
	Date        string `json:"date" jsonschema:"when the money left, YYYY-MM-DD"`
	Description string `json:"description" jsonschema:"the text the bank shows, e.g. NETFLIX.COM 866-579-7172"`
	Amount      string `json:"amount" jsonschema:"what left the account as a positive decimal string, e.g. 12.99. Incoming money and refunds are ignored"`
	Currency    string `json:"currency,omitempty" jsonschema:"ISO 4217 code; default: the household's currency"`
}

// MatchStatementInput is a statement to compare with the expected charges.
type MatchStatementInput struct {
	Transactions []StatementLine `json:"transactions" jsonschema:"the payments of the statement"`
	From         string          `json:"from,omitempty" jsonschema:"first day the statement covers, YYYY-MM-DD (default: the first transaction)"`
	To           string          `json:"to,omitempty" jsonschema:"last day the statement covers, YYYY-MM-DD (default: the last transaction)"`
}

// StatementMatch says which expected charges a statement confirms.
type StatementMatch struct {
	From    engine.Date   `json:"from"`
	To      engine.Date   `json:"to"`
	Matches []ChargeMatch `json:"matches" jsonschema:"expected charges found in the statement, by due date"`
	// Missing are open charges due in the period that no transaction
	// matches.
	Missing   []ChargeView `json:"missing" jsonschema:"open charges due in the statement period that no transaction matches: expected but not found. When some charges matched, only those paid with the same payment methods (or with none) are listed"`
	Unmatched int          `json:"unmatched" jsonschema:"transactions that match no expected charge"`
}

// Match actions.
const (
	MatchMarkPaid    = "mark_paid"
	MatchAlreadyPaid = "already_paid"
)

// ChargeMatch is an expected charge and the transaction that paid it.
type ChargeMatch struct {
	Charge      ChargeView    `json:"charge"`
	Transaction StatementLine `json:"transaction"`
	Paid        money.Money   `json:"paid" jsonschema:"what the transaction took"`
	Action      string        `json:"action" jsonschema:"mark_paid: call set_charge_status with status=paid, chargedAmount and chargedCurrency from paid (dryRun first); already_paid: nothing to do" enum:"mark_paid,already_paid"`
	// PriceChange is set when the bank took another amount in the same
	// currency: ask whether it is the new price.
	PriceChange *PriceChangeHint `json:"priceChange,omitempty" jsonschema:"the bank took another amount than expected. After marking it paid, ask the user whether this is the new price; if so call update_subscription with price"`
}

// MaxStatementLines bounds one statement.
const MaxStatementLines = 10_000

// Matching tolerances.
const (
	// matchDays is how far a transaction may be from the due date. A
	// weekly or fortnightly subscription gets less, so a charge cannot
	// take the next one's transaction.
	matchDays = 7
	// matchPercent is how far the amount may be from the expected one, in
	// percent: enough for a price rise, not for another product.
	matchPercent = 30
	// settleDays: a charge due in the last days of the statement may not
	// have reached it yet, so it is not reported as missing.
	settleDays = 3
)

// MatchStatement compares the transactions of a statement with the charges
// Exhale expects in the same period. It changes nothing.
func (s *Service) MatchStatement(ctx context.Context, actor Actor, in MatchStatementInput) (StatementMatch, error) {
	if len(in.Transactions) > MaxStatementLines {
		return StatementMatch{}, fmt.Errorf("%w: at most %d transactions per call", domain.ErrValidation, MaxStatementLines)
	}
	q := s.st.Q
	h, err := s.household(ctx, q, actor.HouseholdID)
	if err != nil {
		return StatementMatch{}, err
	}
	type line struct {
		StatementLine
		date engine.Date
		paid money.Money
		key  string
	}
	var lines []line
	var from, to engine.Date
	for i, t := range in.Transactions {
		d, err := engine.ParseDate(strings.TrimSpace(t.Date))
		if err != nil {
			return StatementMatch{}, fmt.Errorf("%w: transaction %d: date: %v", domain.ErrValidation, i+1, err)
		}
		cur := cmp.Or(strings.ToUpper(strings.TrimSpace(t.Currency)), h.Currency)
		paid, err := money.Parse(strings.TrimPrefix(strings.TrimSpace(t.Amount), "+"), cur)
		if err != nil {
			return StatementMatch{}, fmt.Errorf("%w: transaction %d: amount: %v", domain.ErrValidation, i+1, err)
		}
		if from.IsZero() || d.Before(from) {
			from = d
		}
		if d.After(to) {
			to = d
		}
		if paid.Minor <= 0 {
			continue // money that came in: it only tells the period
		}
		t.Currency = cur
		lines = append(lines, line{StatementLine: t, date: d, paid: paid, key: merchant.Key(t.Description)})
	}
	out := StatementMatch{From: from, To: to, Matches: []ChargeMatch{}, Missing: []ChargeView{}}
	if len(lines) == 0 {
		return out, nil
	}
	if in.From != "" {
		if out.From, err = engine.ParseDate(in.From); err != nil {
			return StatementMatch{}, fmt.Errorf("%w: from: %v", domain.ErrValidation, err)
		}
	}
	if in.To != "" {
		if out.To, err = engine.ParseDate(in.To); err != nil {
			return StatementMatch{}, fmt.Errorf("%w: to: %v", domain.ErrValidation, err)
		}
	}
	if out.To.Before(out.From) {
		return StatementMatch{}, fmt.Errorf("%w: to is before from", domain.ErrValidation)
	}
	list, err := s.ListCharges(ctx, actor, ListChargesInput{From: out.From.AddDays(-matchDays).String(), To: out.To.AddDays(matchDays).String()})
	if err != nil {
		return StatementMatch{}, err
	}
	charges := slices.DeleteFunc(list.Charges, func(c ChargeView) bool { return c.Status == domain.ChargeSkipped })

	// The window of each subscription: under half the shortest gap between
	// two of its charges, at most a week.
	window := map[string]int{}
	last := map[string]engine.Date{}
	for _, c := range charges {
		w, ok := window[c.SubscriptionID]
		if !ok {
			w = matchDays
		}
		if prev, ok := last[c.SubscriptionID]; ok {
			w = min(w, max((abs(prev.DaysUntil(c.DueDate))-1)/2, 0))
		}
		window[c.SubscriptionID], last[c.SubscriptionID] = w, c.DueDate
	}

	type pair struct{ c, l, score int }
	var pairs []pair
	for ci, c := range charges {
		key := merchant.Key(c.SubscriptionName)
		for li, l := range lines {
			days := abs(c.DueDate.DaysUntil(l.date))
			if days > window[c.SubscriptionID] || !merchant.Same(key, l.key) {
				continue
			}
			score := days
			if l.paid.Currency == c.Amount.Currency {
				diff := abs(l.paid.Minor - c.Amount.Minor)
				if diff*100 > c.Amount.Minor*matchPercent {
					continue
				}
				if diff != 0 {
					score += 3
				}
			} else {
				score += 2 // another currency: the amount cannot be compared
			}
			pairs = append(pairs, pair{ci, li, score})
		}
	}
	slices.SortStableFunc(pairs, func(a, b pair) int {
		return cmp.Or(cmp.Compare(a.score, b.score), charges[a.c].DueDate.Compare(charges[b.c].DueDate))
	})
	usedCharge, usedLine := map[int]bool{}, map[int]bool{}
	methods := map[string]bool{}
	for _, p := range pairs {
		if usedCharge[p.c] || usedLine[p.l] {
			continue
		}
		usedCharge[p.c], usedLine[p.l] = true, true
		c, l := charges[p.c], lines[p.l]
		m := ChargeMatch{Charge: c, Transaction: l.StatementLine, Paid: l.paid, Action: MatchMarkPaid}
		if c.Status == domain.ChargePaid {
			m.Action = MatchAlreadyPaid
		} else if l.paid.Currency == c.Amount.Currency && l.paid != c.Amount {
			if sub, err := s.subscription(ctx, q, actor, c.SubscriptionID); err == nil {
				paid := c.Charge
				paid.Status, paid.ChargedAmount = domain.ChargePaid, l.paid
				m.PriceChange = priceChangeHint(paid, sub)
			}
		}
		methods[c.PaymentMethod] = true
		out.Matches = append(out.Matches, m)
	}
	slices.SortStableFunc(out.Matches, func(a, b ChargeMatch) int {
		return cmp.Or(a.Charge.DueDate.Compare(b.Charge.DueDate), strings.Compare(a.Charge.SubscriptionName, b.Charge.SubscriptionName))
	})
	// A statement covers one account or card: what other cards pay is not
	// missing from it. Once something matched, only the methods it was paid
	// with (and charges without a method) are expected.
	delete(methods, "")
	settled := out.To.AddDays(-settleDays)
	if settled.Before(out.From) {
		settled = out.To
	}
	for ci, c := range charges {
		if usedCharge[ci] || !c.Open() || c.DueDate.Before(out.From) || c.DueDate.After(settled) {
			continue
		}
		if len(methods) > 0 && c.PaymentMethod != "" && !methods[c.PaymentMethod] {
			continue
		}
		out.Missing = append(out.Missing, c)
	}
	out.Unmatched = len(lines) - len(usedLine)
	return out, nil
}

func abs[T int | int64](n T) T {
	if n < 0 {
		return -n
	}
	return n
}
