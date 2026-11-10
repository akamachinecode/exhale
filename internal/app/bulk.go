package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// Tools for people with many subscriptions: tags, an alternative price,
// changes to many subscriptions at once and tag renames. Every change is
// audited per subscription; the audit ids of one call can be undone together.

// MaxBulk bounds one bulk change (and one undo of several changes).
const MaxBulk = 500

// --- tags and the alternative price on create and update ---------------------------

// ExtrasInput are the tags and the alternative price of a new subscription.
type ExtrasInput struct {
	Tags     []string `json:"tags,omitempty" jsonschema:"free labels, many per subscription, e.g. work or family. Totals stay by category"`
	AltPrice string   `json:"altPrice,omitempty" jsonschema:"another price the provider offers for the same service, decimal string in the subscription currency, e.g. 99.00 per year for a monthly plan. Only a price the member gave; never guess one"`
	AltUnit  string   `json:"altUnit,omitempty" jsonschema:"the period altPrice covers: month or year (default: year)" enum:"month,year"`
}

func (in ExtrasInput) apply(d *domain.Details, currency string) error {
	tags, err := cleanTags(in.Tags)
	if err != nil {
		return err
	}
	d.Tags = tags
	d.AltPrice, err = altPrice(in.AltPrice, in.AltUnit, "", currency)
	return err
}

// ExtrasUpdate changes the tags or the alternative price.
type ExtrasUpdate struct {
	Tags     []string `json:"tags,omitempty" jsonschema:"replaces every tag; an empty list removes them all"`
	AltPrice *string  `json:"altPrice,omitempty" jsonschema:"another price the provider offers for the same service, decimal string; empty string removes it. Only a price the member gave"`
	AltUnit  *string  `json:"altUnit,omitempty" jsonschema:"the period altPrice covers: month or year" enum:"month,year"`
}

func (in ExtrasUpdate) apply(sub *domain.Subscription) error {
	if in.Tags != nil {
		tags, err := cleanTags(in.Tags)
		if err != nil {
			return err
		}
		sub.Tags = tags
	}
	if in.AltPrice == nil && in.AltUnit == nil {
		return nil
	}
	price, unit, current := "", "", ""
	if sub.AltPrice != nil {
		price, current = sub.AltPrice.Price.Decimal(), string(sub.AltPrice.Unit)
	}
	set(&price, in.AltPrice)
	set(&unit, in.AltUnit)
	var err error
	sub.AltPrice, err = altPrice(price, unit, current, sub.Price.Currency)
	return err
}

func cleanTags(tags []string) ([]string, error) {
	if tags == nil {
		return nil, nil
	}
	out := domain.NormalizeTags(tags)
	for _, t := range out {
		if err := domain.ValidateTag(t); err != nil {
			return nil, err
		}
	}
	if len(out) > domain.MaxTags {
		return nil, fmt.Errorf("%w: at most %d tags", domain.ErrValidation, domain.MaxTags)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func altPrice(price, unit, current, currency string) (*domain.AltPrice, error) {
	price = strings.ReplaceAll(strings.TrimSpace(price), ",", ".")
	if price == "" {
		return nil, nil
	}
	p, err := money.Parse(price, currency)
	if err != nil {
		return nil, fmt.Errorf("%w: other price: %v", domain.ErrValidation, err)
	}
	u := engine.Unit(cmp.Or(unit, current, string(engine.Year)))
	return &domain.AltPrice{Price: p, Unit: u}, nil
}

// yearlyCost is what one rule costs in a year. Monthly and yearly rules are
// computed in whole minor units, so 9.99 a month is exactly 119.88 a year.
func yearlyCost(price money.Money, r engine.Rule) money.Money {
	n := int64(max(r.Interval, 1))
	switch r.Unit {
	case engine.Month:
		return money.Money{Minor: (price.Minor*12 + n/2) / n, Currency: price.Currency}
	case engine.Year:
		return money.Money{Minor: (price.Minor + n/2) / n, Currency: price.Currency}
	case engine.Day, engine.Week:
	}
	m, _ := price.Scale(monthlyFactor(r))
	y, _ := m.Scale("12")
	return y
}

// PlanSwitch compares the current plan with the other price the member
// recorded.
type PlanSwitch struct {
	Current money.Money `json:"currentYearly" jsonschema:"what the current plan costs per year"`
	Other   money.Money `json:"otherYearly" jsonschema:"what the other price costs per year"`
	// Saving is positive when switching saves money, negative when the
	// current plan is already the cheaper one.
	Saving money.Money `json:"yearlySaving" jsonschema:"current minus other, per year: positive means switching saves this much a year; negative means the current plan is cheaper"`
}

func planSwitch(sub domain.Subscription, today engine.Date) *PlanSwitch {
	if sub.AltPrice == nil || sub.AltPrice.Price.Currency != sub.Price.Currency {
		return nil
	}
	cur := yearlyCost(sub.PriceOn(today), sub.Schedule.Current(today))
	other := yearlyCost(sub.AltPrice.Price, engine.Rule{Interval: 1, Unit: sub.AltPrice.Unit})
	return &PlanSwitch{Current: cur, Other: other, Saving: money.Money{Minor: cur.Minor - other.Minor, Currency: cur.Currency}}
}

// --- tag list ----------------------------------------------------------------------

// TagCount is a tag with how many subscriptions carry it.
type TagCount struct {
	Name          string `json:"name"`
	Subscriptions int    `json:"subscriptions"`
}

// tagCounts lists the tags of the subscriptions the actor can see, sorted by
// name. The first spelling met wins.
func (s *Service) tagCounts(ctx context.Context, q *db.Queries, actor Actor) []TagCount {
	subs, _ := s.visibleSubscriptions(ctx, q, actor)
	byKey := map[string]*TagCount{}
	for _, sub := range subs {
		for _, t := range sub.Tags {
			k := strings.ToLower(t)
			if byKey[k] == nil {
				byKey[k] = &TagCount{Name: t}
			}
			byKey[k].Subscriptions++
		}
	}
	out := make([]TagCount, 0, len(byKey))
	for _, tc := range byKey {
		out = append(out, *tc)
	}
	slices.SortFunc(out, func(a, b TagCount) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
	return out
}

// --- bulk changes ------------------------------------------------------------------

// BulkUpdateInput changes many subscriptions in one step.
type BulkUpdateInput struct {
	IDs             []string `json:"ids" jsonschema:"subscription ids (sub_…), at most 500" minItems:"1" maxItems:"500"`
	Action          string   `json:"action,omitempty" jsonschema:"update (default): set the fields given below. pause, resume, cancel_at_period_end, cancel_now or delete apply to every subscription" enum:"update,pause,resume,cancel_at_period_end,cancel_now,delete"`
	CategoryID      *string  `json:"categoryId,omitempty" jsonschema:"with update: the new category; empty string removes it"`
	PaymentMethodID *string  `json:"paymentMethodId,omitempty" jsonschema:"with update: the new payment method; empty string removes it"`
	AddTags         []string `json:"addTags,omitempty" jsonschema:"with update: tags to add to each"`
	RemoveTags      []string `json:"removeTags,omitempty" jsonschema:"with update: tags to remove from each"`
	DryRun          bool     `json:"dryRun,omitempty" jsonschema:"preview what changes for each subscription; nothing is saved"`
}

// FieldChange is one field that changes on one subscription.
type FieldChange struct {
	Field string `json:"field" jsonschema:"category, paymentMethod, tags, status or deleted" enum:"category,paymentMethod,tags,status,deleted"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// BulkItem is what happens (or would happen) to one subscription.
type BulkItem struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Changes []FieldChange `json:"changes" jsonschema:"empty: nothing changes for this one"`
}

// BulkResult reports a change to several subscriptions.
type BulkResult struct {
	Items    []BulkItem `json:"items"`
	Changed  int        `json:"changed" jsonschema:"subscriptions that change"`
	AuditIDs []string   `json:"auditIds" jsonschema:"pass them all to undo (auditIds) to revert the whole change; empty in a dry run"`
	DryRun   bool       `json:"dryRun,omitempty"`
}

// BulkUpdate changes the category, payment method or tags of several
// subscriptions, or pauses, resumes, cancels or deletes them, in one
// transaction: all of them or none.
func (s *Service) BulkUpdate(ctx context.Context, actor Actor, in BulkUpdateInput) (BulkResult, error) {
	out := BulkResult{Items: []BulkItem{}, AuditIDs: []string{}, DryRun: in.DryRun}
	ids := []string{}
	for _, id := range in.IDs {
		if id != "" && !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 || len(ids) > MaxBulk {
		return out, fmt.Errorf("%w: pass between 1 and %d subscription ids", domain.ErrValidation, MaxBulk)
	}
	action := cmp.Or(in.Action, "update")
	switch action {
	case "update":
		if in.CategoryID == nil && in.PaymentMethodID == nil && in.AddTags == nil && in.RemoveTags == nil {
			return out, fmt.Errorf("%w: say what to change: categoryId, paymentMethodId, addTags or removeTags", domain.ErrValidation)
		}
	case "pause", "resume", "cancel_at_period_end", "cancel_now", "delete":
		if in.CategoryID != nil || in.PaymentMethodID != nil || in.AddTags != nil || in.RemoveTags != nil {
			return out, fmt.Errorf("%w: fields can only be changed with action update", domain.ErrValidation)
		}
	default:
		return out, fmt.Errorf("%w: unknown action %q", domain.ErrValidation, action)
	}
	add, err := cleanTags(in.AddTags)
	if err != nil {
		return out, err
	}
	err = s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		names := s.refNames(ctx, q, actor.HouseholdID)
		for _, id := range ids {
			before, err := s.subscription(ctx, q, actor, id)
			if err != nil {
				return err
			}
			item := BulkItem{ID: before.ID, Name: before.Name, Changes: []FieldChange{}}
			var audit string
			switch action {
			case "update":
				sub := before
				set(&sub.CategoryID, in.CategoryID)
				set(&sub.PaymentMethodID, in.PaymentMethodID)
				sub.Tags = editTags(sub.Tags, add, in.RemoveTags)
				item.Changes = names.diff(before, sub)
				if len(item.Changes) == 0 {
					break
				}
				if audit, err = s.saveEdited(ctx, q, c, before, sub); err != nil {
					return err
				}
			case "delete":
				item.Changes = append(item.Changes, FieldChange{Field: "deleted", From: string(before.Status)})
				if audit, err = s.deleteSubscription(ctx, q, c, actor, before); err != nil {
					return err
				}
			default:
				to := map[string]domain.SubscriptionStatus{"pause": domain.StatusPaused, "resume": domain.StatusActive}[action]
				if to == "" {
					to = domain.StatusCancelled
				}
				if before.Status == to || (action == "pause" && before.Status == domain.StatusCancelled) {
					break // already there; a cancelled one is not paused
				}
				if _, audit, err = s.setStatus(ctx, q, c, actor, before, action); err != nil {
					return err
				}
				item.Changes = append(item.Changes, FieldChange{Field: "status", From: string(before.Status), To: string(to)})
			}
			if audit != "" {
				out.AuditIDs = append(out.AuditIDs, audit)
				out.Changed++
			}
			out.Items = append(out.Items, item)
		}
		return nil
	})
	if in.DryRun {
		out.AuditIDs = []string{}
	}
	return out, err
}

// saveEdited saves a subscription whose plain fields changed (no price or
// schedule change, so its charges stay as they are) and audits it.
func (s *Service) saveEdited(ctx context.Context, q *db.Queries, c *change, before, sub domain.Subscription) (string, error) {
	if err := sub.Validate(); err != nil {
		return "", err
	}
	if err := s.checkRefs(ctx, q, sub); err != nil {
		return "", err
	}
	if err := s.saveSubscription(ctx, q, sub); err != nil {
		return "", err
	}
	id, err := c.record(q, "subscription.update", "subscription", sub.ID, before, sub)
	if err != nil {
		return "", err
	}
	c.emit(bus.Event{Type: bus.SubscriptionUpdated, HouseholdID: sub.HouseholdID, SubscriptionID: sub.ID})
	return id, nil
}

// editTags adds and removes tags, keeping the order of the ones kept.
func editTags(tags, add, remove []string) []string {
	out := []string{}
	for _, t := range tags {
		if !domain.HasTag(remove, t) {
			out = append(out, t)
		}
	}
	out = domain.NormalizeTags(append(out, add...))
	if len(out) == 0 {
		return nil
	}
	return out
}

type refNames struct{ categories, methods map[string]string }

func (s *Service) refNames(ctx context.Context, q *db.Queries, household string) refNames {
	n := refNames{categories: map[string]string{}, methods: map[string]string{}}
	for _, c := range mustList(q.ListCategories(ctx, household)) {
		n.categories[c.ID] = c.Name
	}
	for _, p := range mustList(q.ListPaymentMethods(ctx, household)) {
		n.methods[p.ID] = p.Name
	}
	return n
}

// diff lists the fields a bulk edit or a tag change touches, by name.
func (n refNames) diff(a, b domain.Subscription) []FieldChange {
	out := []FieldChange{}
	if a.CategoryID != b.CategoryID {
		out = append(out, FieldChange{Field: "category", From: n.categories[a.CategoryID], To: n.categories[b.CategoryID]})
	}
	if a.PaymentMethodID != b.PaymentMethodID {
		out = append(out, FieldChange{Field: "paymentMethod", From: n.methods[a.PaymentMethodID], To: n.methods[b.PaymentMethodID]})
	}
	if !slices.Equal(a.Tags, b.Tags) {
		out = append(out, FieldChange{Field: "tags", From: strings.Join(a.Tags, ", "), To: strings.Join(b.Tags, ", ")})
	}
	return out
}

// --- rename and delete a tag everywhere ------------------------------------------

// RenameTagInput renames a tag on every subscription that has it.
type RenameTagInput struct {
	Tag    string `json:"tag" jsonschema:"the tag to rename (case does not matter)"`
	To     string `json:"to" jsonschema:"the new name; if a subscription already has it, the two merge"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// RenameTag renames a tag everywhere the actor can see it.
func (s *Service) RenameTag(ctx context.Context, actor Actor, in RenameTagInput) (BulkResult, error) {
	to := strings.Join(strings.Fields(in.To), " ")
	if err := domain.ValidateTag(to); err != nil {
		return BulkResult{}, err
	}
	return s.retag(ctx, actor, in.Tag, in.DryRun, func(tags []string) []string {
		out := slices.Clone(tags)
		for i, t := range out {
			if domain.SameTag(t, in.Tag) {
				out[i] = to
			}
		}
		return domain.NormalizeTags(out)
	})
}

// DeleteTagInput removes a tag from every subscription.
type DeleteTagInput struct {
	Tag    string `json:"tag" jsonschema:"the tag to remove (case does not matter)"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// DeleteTag removes a tag from every subscription the actor can see.
func (s *Service) DeleteTag(ctx context.Context, actor Actor, in DeleteTagInput) (BulkResult, error) {
	return s.retag(ctx, actor, in.Tag, in.DryRun, func(tags []string) []string { return editTags(tags, nil, []string{in.Tag}) })
}

func (s *Service) retag(ctx context.Context, actor Actor, tag string, dryRun bool, edit func([]string) []string) (BulkResult, error) {
	out := BulkResult{Items: []BulkItem{}, AuditIDs: []string{}, DryRun: dryRun}
	if strings.TrimSpace(tag) == "" {
		return out, fmt.Errorf("%w: say which tag", domain.ErrValidation)
	}
	err := s.write(ctx, actor, dryRun, func(q *db.Queries, c *change) error {
		subs, err := s.visibleSubscriptions(ctx, q, actor)
		if err != nil {
			return err
		}
		names := s.refNames(ctx, q, actor.HouseholdID)
		for _, before := range subs {
			if !domain.HasTag(before.Tags, tag) {
				continue
			}
			sub := before
			if sub.Tags = edit(before.Tags); len(sub.Tags) == 0 {
				sub.Tags = nil
			}
			item := BulkItem{ID: sub.ID, Name: sub.Name, Changes: names.diff(before, sub)}
			if len(item.Changes) > 0 {
				id, err := s.saveEdited(ctx, q, c, before, sub)
				if err != nil {
					return err
				}
				out.AuditIDs = append(out.AuditIDs, id)
				out.Changed++
			}
			out.Items = append(out.Items, item)
		}
		if len(out.Items) == 0 {
			return fmt.Errorf("%w: no subscription has the tag %q", domain.ErrValidation, tag)
		}
		return nil
	})
	if dryRun {
		out.AuditIDs = []string{}
	}
	return out, err
}
