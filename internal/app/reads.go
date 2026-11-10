package app

import (
	"context"
	"slices"
	"strings"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// ListSubscriptionsInput filters subscriptions.
type ListSubscriptionsInput struct {
	Status string `json:"status,omitempty" query:"status" jsonschema:"active, paused or cancelled; empty for all"`
	Query  string `json:"query,omitempty" query:"query" jsonschema:"case-insensitive search in the name, notes, account, category and tags"`
	Tag    string `json:"tag,omitempty" query:"tag" jsonschema:"only subscriptions with this tag (case-insensitive)"`
}

// SubscriptionList is a list of subscriptions.
type SubscriptionList struct {
	Subscriptions []SubscriptionView `json:"subscriptions"`
}

// ListSubscriptions returns the subscriptions visible to the actor.
func (s *Service) ListSubscriptions(ctx context.Context, actor Actor, in ListSubscriptionsInput) (SubscriptionList, error) {
	q := s.st.Q
	subs, err := s.visibleSubscriptions(ctx, q, actor)
	if err != nil {
		return SubscriptionList{}, err
	}
	today := s.today(ctx, q, actor)
	out := SubscriptionList{Subscriptions: []SubscriptionView{}}
	cats := map[string]string{}
	if in.Query != "" {
		for _, c := range mustList(q.ListCategories(ctx, actor.HouseholdID)) {
			cats[c.ID] = c.Name
		}
	}
	for _, sub := range subs {
		if in.Status != "" && string(sub.Status) != in.Status {
			continue
		}
		if in.Tag != "" && !domain.HasTag(sub.Tags, in.Tag) {
			continue
		}
		if in.Query != "" && !matchesQuery(sub, cats[sub.CategoryID], in.Query) {
			continue
		}
		out.Subscriptions = append(out.Subscriptions, s.view(ctx, q, sub, today))
	}
	return out, nil
}

// matchesQuery reports whether every word of query appears in the name,
// notes, account, category or tags.
func matchesQuery(sub domain.Subscription, category, query string) bool {
	hay := strings.ToLower(strings.Join(append([]string{sub.Name, sub.Notes, sub.Account, category}, sub.Tags...), "\n"))
	for _, w := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// visibleSubscriptions loads every subscription the actor may see.
func (s *Service) visibleSubscriptions(ctx context.Context, q *db.Queries, actor Actor) ([]domain.Subscription, error) {
	rows, err := q.ListSubscriptions(ctx, actor.HouseholdID)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Subscription, 0, len(rows))
	for _, r := range rows {
		sub, err := store.SubscriptionFromRow(r)
		if err != nil {
			return out, err
		}
		if !sub.VisibleTo(actor.MemberID) && actor.Kind != ActorSystem {
			continue
		}
		out = append(out, sub)
	}
	return out, nil
}

// GetSubscriptionInput identifies a subscription.
type GetSubscriptionInput struct {
	ID string `json:"id" path:"id" jsonschema:"subscription id (sub_…)"`
}

// GetSubscription returns a subscription with upcoming and past charges.
func (s *Service) GetSubscription(ctx context.Context, actor Actor, in GetSubscriptionInput) (SubscriptionDetail, error) {
	return s.detail(ctx, s.st.Q, actor, in.ID)
}

// ListChargesInput selects charges by due date.
type ListChargesInput struct {
	From string `json:"from,omitempty" query:"from" jsonschema:"first due date, YYYY-MM-DD (default today)"`
	To   string `json:"to,omitempty" query:"to" jsonschema:"last due date, YYYY-MM-DD (default from + 30 days)"`
}

// ChargeList is a list of charges.
type ChargeList struct {
	From    engine.Date  `json:"from"`
	To      engine.Date  `json:"to"`
	Charges []ChargeView `json:"charges"`
}

// ListCharges returns charges due in a date range, e.g. the next 30 days.
func (s *Service) ListCharges(ctx context.Context, actor Actor, in ListChargesInput) (ChargeList, error) {
	q := s.st.Q
	today := s.today(ctx, q, actor)
	from, to := today, engine.Date{}
	var err error
	if in.From != "" {
		if from, err = engine.ParseDate(in.From); err != nil {
			return ChargeList{}, domain.BadDate("from", in.From)
		}
	}
	if to = from.AddDays(30); in.To != "" {
		if to, err = engine.ParseDate(in.To); err != nil {
			return ChargeList{}, domain.BadDate("to", in.To)
		}
	}
	rows, err := q.ListChargesBetween(ctx, db.ListChargesBetweenParams{HouseholdID: actor.HouseholdID, FromDate: from.String(), ToDate: to.String()})
	if err != nil {
		return ChargeList{}, err
	}
	return ChargeList{From: from, To: to, Charges: s.chargeViews(ctx, q, actor, rows, today)}, nil
}

// AttentionList is what needs the user's attention.
type AttentionList struct {
	Unconfirmed            []ChargeView           `json:"unconfirmed" jsonschema:"charges whose due date passed and that are not marked paid, failed or skipped"`
	ExpiringPaymentMethods []domain.PaymentMethod `json:"expiringPaymentMethods" jsonschema:"payment methods expiring within 45 days"`
	Deadlines              []ChargeView           `json:"deadlines" jsonschema:"last days to cancel within 14 days that have no decision yet, and free trials ending within 7 days"`
	// Delivery reports reminders that could not be delivered lately, so a
	// broken channel does not go unnoticed.
	Delivery *DeliveryProblem `json:"delivery,omitempty" jsonschema:"set when reminders failed to be delivered in the last 7 days"`
}

// DeliveryProblem says reminders are not arriving.
type DeliveryProblem struct {
	Failed    int    `json:"failed" jsonschema:"reminders not delivered in the last 7 days"`
	NoChannel int    `json:"noChannel" jsonschema:"of those, how many had nowhere to go (no chat or channel linked)"`
	LastError string `json:"lastError,omitempty" jsonschema:"most recent delivery error"`
}

// NeedsAttention lists unconfirmed past charges and expiring payment methods.
func (s *Service) NeedsAttention(ctx context.Context, actor Actor, _ struct{}) (AttentionList, error) {
	q := s.st.Q
	today := s.today(ctx, q, actor)
	rows, err := q.ListOpenChargesDueBefore(ctx, db.ListOpenChargesDueBeforeParams{HouseholdID: actor.HouseholdID, Before: today.String()})
	if err != nil {
		return AttentionList{}, err
	}
	out := AttentionList{Unconfirmed: s.chargeViews(ctx, q, actor, rows, today), ExpiringPaymentMethods: []domain.PaymentMethod{}, Deadlines: []ChargeView{}}
	soon, err := q.ListChargesBetween(ctx, db.ListChargesBetweenParams{HouseholdID: actor.HouseholdID, FromDate: today.String(), ToDate: today.AddDays(14 + 366).String()})
	if err != nil {
		return out, err
	}
	for _, c := range s.chargeViews(ctx, q, actor, soon, today) {
		if c.Status != domain.ChargePending {
			continue
		}
		trial := c.Kind == ChargeTrial && c.DaysUntil <= 7
		deadline := !c.CancelBy.IsZero() && c.Decision == "" && !c.CancelBy.Before(today) && today.DaysUntil(c.CancelBy) <= 14
		if trial || deadline {
			out.Deadlines = append(out.Deadlines, c)
		}
	}
	if log, err := s.Reminders(ctx, actor, ReminderLogInput{Limit: 300}); err == nil {
		since := s.now().AddDate(0, 0, -7)
		p := DeliveryProblem{}
		for _, e := range log.Entries {
			retrying := e.Status == "pending" && e.Attempts > 0
			if (e.Status != "failed" && !retrying) || e.DueAt.Before(since) {
				continue
			}
			p.Failed++
			switch {
			case e.Error == ErrNoDestination.Error():
				p.NoChannel++
			case p.LastError == "":
				p.LastError = e.Error
			}
		}
		if p.Failed > 0 {
			out.Delivery = &p
		}
	}
	pms, err := q.ListPaymentMethods(ctx, actor.HouseholdID)
	if err != nil {
		return out, err
	}
	for _, r := range pms {
		pm := store.PaymentMethodFromRow(r)
		if !pm.ExpiresOn.IsZero() && today.DaysUntil(pm.ExpiresOn) <= 45 {
			out.ExpiringPaymentMethods = append(out.ExpiringPaymentMethods, pm)
		}
	}
	return out, nil
}

func (s *Service) chargeViews(ctx context.Context, q *db.Queries, actor Actor, rows []db.Charge, today engine.Date) []ChargeView {
	views := map[string]*SubscriptionView{}
	out := []ChargeView{}
	for _, r := range rows {
		v, ok := views[r.SubscriptionID]
		if !ok {
			sub, err := s.subscription(ctx, q, actor, r.SubscriptionID)
			if err != nil {
				views[r.SubscriptionID] = nil
				continue
			}
			sv := s.view(ctx, q, sub, today)
			v = &sv
			views[r.SubscriptionID] = v
		}
		if v == nil {
			continue // not visible to this actor
		}
		out = append(out, s.chargeView(store.ChargeFromRow(r), *v, today))
	}
	return out
}

// PreviewScheduleInput describes a billing rule to preview.
type PreviewScheduleInput struct {
	Interval     int    `json:"interval" minimum:"1"`
	Unit         string `json:"unit" enum:"day,week,month,year"`
	FirstCharge  string `json:"firstCharge" jsonschema:"YYYY-MM-DD"`
	DayRule      string `json:"dayRule,omitempty" jsonschema:"monthly and yearly cycles. day (default): the same day number as the first charge, the last day in shorter months. weekday: the same weekday and week of the month as the first charge, e.g. the third Wednesday (the first charge must be a 1st to 4th weekday of its month). last-weekday: the last such weekday of the month, e.g. the last Friday (the first charge must be in the last 7 days of its month)" enum:"day,weekday,last-weekday"`
	WeekendShift string `json:"weekendShift,omitempty" jsonschema:"charges that fall on a Saturday or Sunday. none (default): kept. next: moved to the next business day (Monday). previous: moved to the previous business day (Friday). Weekends only, no public holidays. Later charges are still counted from the first one, so dates never drift" enum:"none,next,previous"`
	Count        int    `json:"count,omitempty" jsonschema:"how many dates, default 12" maximum:"60"`
}

// SchedulePreview lists the computed charge dates.
type SchedulePreview struct {
	Dates []engine.Date `json:"dates"`
}

// PreviewSchedule computes charge dates with the billing engine. The web
// calls it while a recurrence is being edited, so dates are never computed
// in two places (ADR 0013).
func (s *Service) PreviewSchedule(_ context.Context, _ Actor, in PreviewScheduleInput) (SchedulePreview, error) {
	first, err := engine.ParseDate(in.FirstCharge)
	if err != nil {
		return SchedulePreview{}, domain.BadDate("first charge", in.FirstCharge)
	}
	if in.Interval == 0 {
		in.Interval = 1
	}
	rule, err := newRule(in.Interval, in.Unit, in.DayRule, in.WeekendShift, first)
	if err != nil {
		return SchedulePreview{}, err
	}
	sched, err := engine.NewSchedule(rule)
	if err != nil {
		return SchedulePreview{}, domain.BadRule(in.Interval, in.Unit, err)
	}
	n := in.Count
	if n <= 0 || n > 60 {
		n = 12
	}
	return SchedulePreview{Dates: sched.Upcoming(sched.First(), n)}, nil
}

// SpendingSummary totals recurring spend.
type SpendingSummary struct {
	MainCurrency string        `json:"mainCurrency"`
	Monthly      []money.Money `json:"monthlyByCurrency" jsonschema:"active subscriptions normalized per month, one entry per currency"`
	Yearly       []money.Money `json:"yearlyByCurrency"`
	// MonthlyTotal and YearlyTotal are in the main currency, converting other
	// currencies at the latest ECB reference rate (see RatesDate).
	MonthlyTotal money.Money       `json:"monthlyTotal"`
	YearlyTotal  money.Money       `json:"yearlyTotal"`
	Converted    bool              `json:"converted" jsonschema:"true when other currencies were converted into the main currency"`
	Unconverted  []string          `json:"unconverted" jsonschema:"currencies without a known rate, left out of the totals"`
	Rates        map[string]string `json:"rates" jsonschema:"units of the main currency per 1 unit of each currency"`
	RatesDate    string            `json:"ratesDate,omitempty" jsonschema:"ECB reference date of the rates used"`
	ByCategory   []CategorySpend   `json:"byCategory" jsonschema:"monthly spend per category, in the main currency"`
	ByMethod     []MethodSpend     `json:"byPaymentMethod" jsonschema:"monthly spend per payment method, in the main currency"`
	Active       int               `json:"activeSubscriptions"`
	NextCharges  []ChargeView      `json:"next7Days"`
	// SetAside is the monthly equivalent of everything that is not billed
	// monthly: what to put away each month so yearly bills do not surprise.
	SetAside money.Money `json:"setAsideMonthly" jsonschema:"monthly equivalent of the subscriptions billed less often than monthly (yearly, quarterly…), in the display currency: what to put away each month"`
	// BeforePayday is set when the household has a payday.
	BeforePayday *PaydayOutlook `json:"beforePayday,omitempty" jsonschema:"what is due before the next payday, when the household has one"`
	// Stopped is what cancelling saved: "you stopped paying X per year".
	Stopped StoppedSpend `json:"stopped" jsonschema:"subscriptions cancelled in Exhale (not paused) and what they would cost per year, converted into the display currency; a cancelled one whose service is still paid by an active subscription is a replacement and is left out"`
}

// PaydayOutlook is what must be paid before the next payday.
type PaydayOutlook struct {
	Payday  engine.Date  `json:"payday" jsonschema:"next payday"`
	Due     money.Money  `json:"due" jsonschema:"total of the charges due before it, in the display currency"`
	Charges []ChargeView `json:"charges"`
}

// CategorySpend is the monthly spend of one category.
type CategorySpend struct {
	Category string      `json:"category"`
	Monthly  money.Money `json:"monthly" jsonschema:"in the main currency"`
	Count    int         `json:"count"`
}

// MethodSpend is the monthly spend carried by one payment method.
type MethodSpend struct {
	PaymentMethod string      `json:"paymentMethod"`
	Monthly       money.Money `json:"monthly" jsonschema:"in the main currency"`
	Count         int         `json:"count"`
}

// Summary returns recurring spend per month and year (per currency and
// converted into the main currency), per category and per payment method,
// plus the charges of the next 7 days.
func (s *Service) Summary(ctx context.Context, actor Actor, _ struct{}) (SpendingSummary, error) {
	list, err := s.ListSubscriptions(ctx, actor, ListSubscriptionsInput{Status: string(domain.StatusActive)})
	if err != nil {
		return SpendingSummary{}, err
	}
	h, err := s.household(ctx, s.st.Q, actor.HouseholdID)
	if err != nil {
		return SpendingSummary{}, err
	}
	today := s.today(ctx, s.st.Q, actor)
	conv := s.converter(h.Currency, today.String())
	zero := money.Money{Currency: h.Currency}
	out := SpendingSummary{MainCurrency: h.Currency, Active: len(list.Subscriptions), Monthly: []money.Money{}, Yearly: []money.Money{},
		ByCategory: []CategorySpend{}, ByMethod: []MethodSpend{}, Unconverted: []string{}, MonthlyTotal: zero, SetAside: zero}
	monthly := map[string]money.Money{}
	cats := map[string]*CategorySpend{}
	methods := map[string]*MethodSpend{}
	missing := map[string]bool{}
	for _, v := range list.Subscriptions {
		cur := v.MonthlyCost.Currency
		monthly[cur], _ = addOrInit(monthly[cur], v.MonthlyCost)
		mainCost, ok := conv.toMain(ctx, v.MonthlyCost)
		if !ok {
			missing[cur] = true
			continue
		}
		if cur != h.Currency {
			out.Converted = true
		}
		out.MonthlyTotal, _ = out.MonthlyTotal.Add(mainCost)
		if r := v.Schedule.Current(today); r.Unit == engine.Year || (r.Unit == engine.Month && r.Interval > 1) {
			out.SetAside, _ = out.SetAside.Add(mainCost)
		}
		cat := v.CategoryName
		if cat == "" {
			cat = "Uncategorized"
		}
		if cats[cat] == nil {
			cats[cat] = &CategorySpend{Category: cat, Monthly: zero}
		}
		cats[cat].Monthly, _ = cats[cat].Monthly.Add(mainCost)
		cats[cat].Count++
		pm := v.PaymentMethod
		if pm == "" {
			pm = "No payment method"
		}
		if methods[pm] == nil {
			methods[pm] = &MethodSpend{PaymentMethod: pm, Monthly: zero}
		}
		methods[pm].Monthly, _ = methods[pm].Monthly.Add(mainCost)
		methods[pm].Count++
	}
	for _, cur := range sortedKeys(monthly) {
		out.Monthly = append(out.Monthly, monthly[cur])
		y, _ := monthly[cur].Scale("12")
		out.Yearly = append(out.Yearly, y)
	}
	out.YearlyTotal, _ = out.MonthlyTotal.Scale("12")
	for _, c := range cats {
		out.ByCategory = append(out.ByCategory, *c)
	}
	slices.SortFunc(out.ByCategory, func(a, b CategorySpend) int { return int(b.Monthly.Minor - a.Monthly.Minor) })
	for _, m := range methods {
		out.ByMethod = append(out.ByMethod, *m)
	}
	slices.SortFunc(out.ByMethod, func(a, b MethodSpend) int { return int(b.Monthly.Minor - a.Monthly.Minor) })
	if out.Stopped, err = s.stopped(ctx, actor, list.Subscriptions, conv, h.Currency, missing); err != nil {
		return out, err
	}
	out.Unconverted = sortedKeys(missing)
	out.Rates, out.RatesDate = conv.describe()
	next, err := s.ListCharges(ctx, actor, ListChargesInput{From: today.String(), To: today.AddDays(7).String()})
	if err != nil {
		return out, err
	}
	out.NextCharges = next.Charges
	if payday, ok := h.NextPayday(today); ok {
		due, err := s.ListCharges(ctx, actor, ListChargesInput{From: today.String(), To: payday.AddDays(-1).String()})
		if err != nil {
			return out, err
		}
		o := &PaydayOutlook{Payday: payday, Due: zero, Charges: []ChargeView{}}
		for _, c := range due.Charges {
			if !c.Open() {
				continue
			}
			if m, ok := conv.toMain(ctx, c.Amount); ok {
				o.Due, _ = o.Due.Add(m)
			}
			o.Charges = append(o.Charges, c)
		}
		out.BeforePayday = o
	}
	return out, nil
}

func addOrInit(acc, m money.Money) (money.Money, error) {
	if acc.IsZero() {
		return m, nil
	}
	return acc.Add(m)
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
