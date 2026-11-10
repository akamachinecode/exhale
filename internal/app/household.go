package app

import (
	"cmp"
	"context"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/i18n"
	"github.com/akamachinecode/exhale/internal/money"
	"github.com/akamachinecode/exhale/internal/store"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// BootstrapInput creates the household on first run.
type BootstrapInput struct {
	HouseholdName string
	OwnerName     string
	OwnerEmail    string
	Currency      string
	Timezone      string
	Locale        string
}

// Owner returns the first owner of the household as a trusted actor, or
// ErrNotFound if no household exists yet.
func (s *Service) Owner(ctx context.Context) (Actor, error) {
	r, err := s.st.Q.FirstHousehold(ctx)
	if err != nil {
		return Actor{}, domain.NotFound("no household yet")
	}
	members, err := s.st.Q.ListMembers(ctx, r.ID)
	if err != nil {
		return Actor{}, err
	}
	for _, m := range members {
		if m.Role == string(domain.RoleOwner) {
			return MemberActor(store.MemberFromRow(m), ScopeTrusted), nil
		}
	}
	return Actor{}, domain.NotFound("household %s has no owner", r.ID)
}

// Bootstrap creates the household, its owner and default categories if no
// household exists yet. If one exists it returns its owner. It returns the
// owner as a system-usable actor.
func (s *Service) Bootstrap(ctx context.Context, in BootstrapInput) (Actor, error) {
	if a, err := s.Owner(ctx); err == nil {
		return a, nil
	}
	in.HouseholdName = cmp.Or(strings.TrimSpace(in.HouseholdName), "Home")
	if strings.TrimSpace(in.OwnerName) == "" {
		return Actor{}, domain.Invalid("the owner's name is required")
	}
	if in.Currency == "" {
		return Actor{}, domain.Invalid("the main currency is required")
	}
	if _, err := money.New(0, in.Currency); err != nil {
		return Actor{}, domain.Invalid("unknown currency %q", in.Currency)
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "" {
		return Actor{}, domain.Invalid("unknown time zone %q", in.Timezone)
	}
	in.Locale = i18n.Match(in.Locale) // a registered language, else English
	h := domain.Household{ID: domain.NewID(domain.PrefixHousehold), Name: in.HouseholdName, Currency: strings.ToUpper(in.Currency), Timezone: in.Timezone, Locale: in.Locale}
	m := domain.Member{ID: domain.NewID(domain.PrefixMember), HouseholdID: h.ID, Name: in.OwnerName, Email: in.OwnerEmail, Role: domain.RoleOwner}
	now := store.FormatTime(s.now())
	err := s.st.Tx(ctx, func(q *db.Queries) error {
		if err := q.CreateHousehold(ctx, db.CreateHouseholdParams{ID: h.ID, Name: h.Name, Currency: h.Currency, Timezone: h.Timezone, Locale: h.Locale, CreatedAt: now}); err != nil {
			return err
		}
		if err := q.CreateMember(ctx, db.CreateMemberParams{ID: m.ID, HouseholdID: h.ID, Name: m.Name, Email: m.Email, Role: string(m.Role), CreatedAt: now}); err != nil {
			return err
		}
		for _, name := range domain.DefaultCategories {
			if err := q.CreateCategory(ctx, db.CreateCategoryParams{ID: domain.NewID(domain.PrefixCategory), HouseholdID: h.ID, Name: name}); err != nil {
				return err
			}
		}
		return nil
	})
	return MemberActor(m, ScopeTrusted), err
}

// MemberActor returns an actor for a household member.
func MemberActor(m domain.Member, scope Scope) Actor {
	return Actor{Kind: ActorMember, MemberID: m.ID, HouseholdID: m.HouseholdID, Label: m.Name, Scope: scope}
}

// HouseholdInfo is the household with its members, categories and payment
// methods.
type HouseholdInfo struct {
	Household      domain.Household       `json:"household"`
	Members        []domain.Member        `json:"members"`
	Categories     []domain.Category      `json:"categories"`
	PaymentMethods []domain.PaymentMethod `json:"paymentMethods"`
	Today          engine.Date            `json:"today" jsonschema:"today's date in the household time zone"`
	Tags           []TagCount             `json:"tags" jsonschema:"every tag in use, with how many subscriptions carry it"`
}

// LanguageOf returns the language to speak to a member: their own, else
// the household's.
func (i HouseholdInfo) LanguageOf(memberID string) string {
	for _, m := range i.Members {
		if m.ID == memberID && m.Locale != "" {
			return m.Locale
		}
	}
	return i.Household.Locale
}

// GetHousehold returns the household reference data.
func (s *Service) GetHousehold(ctx context.Context, actor Actor, _ struct{}) (HouseholdInfo, error) {
	q := s.st.Q
	h, err := s.household(ctx, q, actor.HouseholdID)
	if err != nil {
		return HouseholdInfo{}, err
	}
	out := HouseholdInfo{Household: h, Members: []domain.Member{}, Categories: []domain.Category{}, PaymentMethods: []domain.PaymentMethod{}, Today: s.today(ctx, q, actor)}
	ms, err := q.ListMembers(ctx, h.ID)
	if err != nil {
		return out, err
	}
	for _, m := range ms {
		out.Members = append(out.Members, store.MemberFromRow(m))
	}
	cs, err := q.ListCategories(ctx, h.ID)
	if err != nil {
		return out, err
	}
	for _, c := range cs {
		out.Categories = append(out.Categories, store.CategoryFromRow(c))
	}
	ps, err := q.ListPaymentMethods(ctx, h.ID)
	if err != nil {
		return out, err
	}
	for _, p := range ps {
		out.PaymentMethods = append(out.PaymentMethods, store.PaymentMethodFromRow(p))
	}
	out.Tags = s.tagCounts(ctx, q, actor)
	return out, nil
}

// UpdateHouseholdInput changes household preferences. Only the fields you
// pass change.
type UpdateHouseholdInput struct {
	Name         *string `json:"name,omitempty" maxLength:"60"`
	Currency     *string `json:"currency,omitempty" jsonschema:"display currency (ISO 4217): totals are converted into it"`
	Timezone     *string `json:"timezone,omitempty" jsonschema:"IANA time zone, e.g. Europe/Madrid"`
	Locale       *string `json:"locale,omitempty" jsonschema:"language of reminders, and of everyone who has not chosen their own: a registered code such as en or es"`
	ReminderTime *string `json:"reminderTime,omitempty" jsonschema:"default time of day for reminders, HH:MM; subscriptions still on the previous default move with it"`
	Payday       *int    `json:"payday,omitempty" jsonschema:"day of the month the household gets paid, 1-31 (31 = last day); 0 clears it. Used to show what is due before the next payday" minimum:"0" maximum:"31"`
	// Repeat until handled: how often an unanswered "was it charged?" comes back.
	ConfirmTimes     *int `json:"confirmTimes,omitempty" jsonschema:"how many times in all to ask whether a charge went through while nobody answers (1: once, no repeats); subscriptions still on the previous setting follow" minimum:"1" maximum:"10"`
	ConfirmEveryDays *int `json:"confirmEveryDays,omitempty" jsonschema:"days between two of those questions" minimum:"1" maximum:"30"`
	DryRun           bool `json:"dryRun,omitempty"`
}

// UpdateHousehold changes the display currency, time zone, language, name
// or default reminder time. Owners only.
func (s *Service) UpdateHousehold(ctx context.Context, actor Actor, in UpdateHouseholdInput) (HouseholdInfo, error) {
	moved := false
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		if actor.Kind != ActorSystem {
			m, err := q.GetMember(ctx, actor.MemberID)
			if err != nil || m.Role != string(domain.RoleOwner) {
				return domain.Fail(ErrForbidden, "only a household owner can change its settings")
			}
		}
		before, err := s.household(ctx, q, actor.HouseholdID)
		if err != nil {
			return err
		}
		h := before
		if in.Name != nil {
			h.Name = strings.TrimSpace(*in.Name)
			if h.Name == "" {
				return domain.Invalid("the name cannot be empty")
			}
		}
		if in.Currency != nil {
			h.Currency = strings.ToUpper(strings.TrimSpace(*in.Currency))
			if _, err := money.New(0, h.Currency); err != nil || h.Currency == "" {
				return domain.Invalid("unknown currency %q", *in.Currency)
			}
		}
		if in.Timezone != nil {
			if _, err := time.LoadLocation(*in.Timezone); err != nil || *in.Timezone == "" {
				return domain.Invalid("unknown time zone %q", *in.Timezone)
			}
			h.Timezone = *in.Timezone
		}
		if in.Locale != nil {
			if !i18n.Supported(*in.Locale) {
				return domain.Invalid("unknown language %q", *in.Locale)
			}
			h.Locale = *in.Locale
		}
		if in.ReminderTime != nil {
			t, err := time.Parse("15:04", strings.TrimSpace(*in.ReminderTime))
			if err != nil {
				return domain.Invalid("reminder time must look like 09:00")
			}
			h.ReminderHour, h.ReminderMinute = t.Hour(), t.Minute()
		}
		if in.Payday != nil {
			if *in.Payday < 0 || *in.Payday > 31 {
				return domain.Invalid("payday must be a day of the month (1-31), or 0 for none")
			}
			h.Payday = *in.Payday
		}
		if in.ConfirmTimes != nil {
			h.ConfirmTimes = *in.ConfirmTimes
		}
		if in.ConfirmEveryDays != nil {
			h.ConfirmEveryDays = *in.ConfirmEveryDays
		}
		if err := domain.ValidateConfirmRepeat(h.ConfirmTimes, h.ConfirmEveryDays); err != nil {
			return err
		}
		if err := q.UpdateHousehold(ctx, db.UpdateHouseholdParams{
			Name: h.Name, Currency: h.Currency, Timezone: h.Timezone, Locale: h.Locale,
			ReminderHour: int64(h.ReminderHour), ReminderMinute: int64(h.ReminderMinute), Payday: int64(h.Payday), ID: h.ID,
		}); err != nil {
			return err
		}
		repeat := h.ConfirmTimes != before.ConfirmTimes || h.ConfirmEveryDays != before.ConfirmEveryDays
		if repeat {
			if err := q.SetHouseholdConfirmRepeat(ctx, db.SetHouseholdConfirmRepeatParams{ConfirmTimes: int64(h.ConfirmTimes), ConfirmEveryDays: int64(h.ConfirmEveryDays), ID: h.ID}); err != nil {
				return err
			}
		}
		if repeat || h.ReminderHour != before.ReminderHour || h.ReminderMinute != before.ReminderMinute {
			rows, err := q.ListSubscriptions(ctx, h.ID)
			if err != nil {
				return err
			}
			for _, r := range rows {
				sub, err := store.SubscriptionFromRow(r)
				if err != nil {
					return err
				}
				changed := false
				if sub.Reminders.Hour == before.ReminderHour && sub.Reminders.Minute == before.ReminderMinute &&
					(sub.Reminders.Hour != h.ReminderHour || sub.Reminders.Minute != h.ReminderMinute) {
					sub.Reminders.Hour, sub.Reminders.Minute = h.ReminderHour, h.ReminderMinute
					changed = true
				}
				if repeat && sub.Reminders.ConfirmTimes == before.ConfirmTimes && sub.Reminders.ConfirmEveryDays == before.ConfirmEveryDays {
					sub.Reminders.ConfirmTimes, sub.Reminders.ConfirmEveryDays = h.ConfirmTimes, h.ConfirmEveryDays
					changed = true
				}
				if changed {
					if err := s.saveSubscription(ctx, q, sub); err != nil {
						return err
					}
					moved = true
				}
			}
		}
		moved = moved || h.Timezone != before.Timezone
		if _, err := c.record(q, "household.update", "household", h.ID, before, h); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.HouseholdUpdated, HouseholdID: h.ID})
		return nil
	})
	if err != nil {
		return HouseholdInfo{}, err
	}
	if moved && !in.DryRun {
		// Reminder times changed: re-plan so queued reminders follow.
		if err := s.Plan(ctx); err != nil {
			s.log.Warn("re-plan after household update failed", "err", err)
		}
	}
	return s.GetHousehold(ctx, actor, struct{}{})
}

// SavePaymentMethodInput creates (no id) or updates a payment method.
type SavePaymentMethodInput struct {
	ID        string `json:"id,omitempty" jsonschema:"existing payment method id (pm_…) to update; empty to create"`
	Name      string `json:"name" jsonschema:"e.g. Revolut, Visa, PayPal"`
	Kind      string `json:"kind" enum:"card,bank,paypal,other"`
	Last4     string `json:"last4,omitempty" jsonschema:"last four digits" maxLength:"4"`
	ExpiresOn string `json:"expiresOn,omitempty" jsonschema:"last valid day, YYYY-MM-DD (for cards: last day of the expiry month)"`
	OwnerID   string `json:"ownerId,omitempty"`
	DryRun    bool   `json:"dryRun,omitempty"`
}

// SavePaymentMethod creates or updates a payment method.
func (s *Service) SavePaymentMethod(ctx context.Context, actor Actor, in SavePaymentMethodInput) (domain.PaymentMethod, error) {
	pm := domain.PaymentMethod{ID: in.ID, HouseholdID: actor.HouseholdID, Name: strings.TrimSpace(in.Name), Kind: domain.PaymentMethodKind(in.Kind), Last4: in.Last4, OwnerID: in.OwnerID}
	switch pm.Kind {
	case domain.KindCard, domain.KindBank, domain.KindPayPal, domain.KindOther:
	default:
		return pm, domain.Invalid("unknown kind %q", in.Kind)
	}
	if pm.Name == "" || len(pm.Last4) > 4 {
		return pm, domain.Invalid("name is required and last4 has at most 4 digits")
	}
	if in.ExpiresOn != "" {
		var err error
		if pm.ExpiresOn, err = engine.ParseDate(in.ExpiresOn); err != nil {
			return pm, domain.BadDate("expires", in.ExpiresOn)
		}
	}
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		var before any
		if pm.ID == "" {
			pm.ID = domain.NewID(domain.PrefixPaymentMethod)
			if err := q.CreatePaymentMethod(ctx, db.CreatePaymentMethodParams{ID: pm.ID, HouseholdID: pm.HouseholdID, Name: pm.Name, Kind: string(pm.Kind), Last4: pm.Last4, ExpiresOn: store.DatePtr(pm.ExpiresOn), OwnerID: store.Ptr(pm.OwnerID)}); err != nil {
				return err
			}
		} else {
			old, err := q.GetPaymentMethod(ctx, db.GetPaymentMethodParams{ID: pm.ID, HouseholdID: actor.HouseholdID})
			if err != nil {
				return store.NotFound(err, "payment method %s: not found", pm.ID)
			}
			before = store.PaymentMethodFromRow(old)
			if err := q.UpdatePaymentMethod(ctx, db.UpdatePaymentMethodParams{Name: pm.Name, Kind: string(pm.Kind), Last4: pm.Last4, ExpiresOn: store.DatePtr(pm.ExpiresOn), OwnerID: store.Ptr(pm.OwnerID), ID: pm.ID, HouseholdID: actor.HouseholdID}); err != nil {
				return err
			}
		}
		if _, err := c.record(q, "payment_method.save", "payment_method", pm.ID, before, pm); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.PaymentMethodSaved, HouseholdID: pm.HouseholdID, EntityID: pm.ID})
		return nil
	})
	return pm, err
}

// CreateCategoryInput creates a category.
type CreateCategoryInput struct {
	Name   string `json:"name"`
	Color  string `json:"color,omitempty" jsonschema:"hex color, e.g. #7c6cff"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// CreateCategory adds a category.
func (s *Service) CreateCategory(ctx context.Context, actor Actor, in CreateCategoryInput) (domain.Category, error) {
	cat := domain.Category{ID: domain.NewID(domain.PrefixCategory), HouseholdID: actor.HouseholdID, Name: strings.TrimSpace(in.Name), Color: in.Color}
	if cat.Name == "" {
		return cat, domain.Invalid("name is required")
	}
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		if err := q.CreateCategory(ctx, db.CreateCategoryParams{ID: cat.ID, HouseholdID: cat.HouseholdID, Name: cat.Name, Color: cat.Color}); err != nil {
			return err
		}
		if _, err := c.record(q, "category.create", "category", cat.ID, nil, cat); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.CategorySaved, HouseholdID: cat.HouseholdID, EntityID: cat.ID})
		return nil
	})
	return cat, err
}

// ReassignPaymentMethodInput moves every subscription from one payment
// method to another.
type ReassignPaymentMethodInput struct {
	FromID string `json:"fromId" jsonschema:"payment method the subscriptions use now (pm_…)"`
	ToID   string `json:"toId,omitempty" jsonschema:"payment method to move them to; empty leaves them without one"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// ReassignResult says how many subscriptions moved.
type ReassignResult struct {
	Moved   int    `json:"moved" jsonschema:"subscriptions that changed payment method"`
	Deleted string `json:"deleted,omitempty" jsonschema:"payment method that was deleted, if any"`
}

// ReassignPaymentMethod moves all subscriptions paid with one method to
// another: the one-step answer to "this card expires next month".
func (s *Service) ReassignPaymentMethod(ctx context.Context, actor Actor, in ReassignPaymentMethodInput) (ReassignResult, error) {
	var out ReassignResult
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		n, err := s.reassign(ctx, q, c, actor, in.FromID, in.ToID)
		out.Moved = n
		return err
	})
	return out, err
}

func (s *Service) reassign(ctx context.Context, q *db.Queries, c *change, actor Actor, from, to string) (int, error) {
	if from == to {
		return 0, domain.Invalid("choose a different payment method to move to")
	}
	for _, id := range []string{from, to} {
		if id == "" {
			continue
		}
		if _, err := q.GetPaymentMethod(ctx, db.GetPaymentMethodParams{ID: id, HouseholdID: actor.HouseholdID}); err != nil {
			return 0, store.NotFound(err, "payment method %s: not found", id)
		}
	}
	n, err := q.ReassignPaymentMethod(ctx, db.ReassignPaymentMethodParams{ToID: store.Ptr(to), UpdatedAt: store.FormatTime(s.now()), HouseholdID: actor.HouseholdID, FromID: store.Ptr(from)})
	if err != nil {
		return 0, err
	}
	if _, err := c.record(q, "payment_method.reassign", "payment_method", from, map[string]string{"from": from}, map[string]any{"to": to, "moved": n}); err != nil {
		return 0, err
	}
	c.emit(bus.Event{Type: bus.PaymentMethodSaved, HouseholdID: actor.HouseholdID, EntityID: from})
	return int(n), nil
}

// DeletePaymentMethodInput removes a payment method.
type DeletePaymentMethodInput struct {
	ID     string `json:"id" jsonschema:"payment method to delete (pm_…)"`
	MoveTo string `json:"moveTo,omitempty" jsonschema:"payment method that takes over its subscriptions; empty leaves them without one"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// DeletePaymentMethod deletes a payment method, moving its subscriptions to
// another one first when asked.
func (s *Service) DeletePaymentMethod(ctx context.Context, actor Actor, in DeletePaymentMethodInput) (ReassignResult, error) {
	var out ReassignResult
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		n, err := s.reassign(ctx, q, c, actor, in.ID, in.MoveTo)
		if err != nil {
			return err
		}
		if err := q.DeletePaymentMethod(ctx, db.DeletePaymentMethodParams{ID: in.ID, HouseholdID: actor.HouseholdID}); err != nil {
			return err
		}
		if _, err := c.record(q, "payment_method.delete", "payment_method", in.ID, map[string]string{"id": in.ID}, nil); err != nil {
			return err
		}
		out = ReassignResult{Moved: n, Deleted: in.ID}
		return nil
	})
	return out, err
}

// UpdateCategoryInput renames or recolours a category.
type UpdateCategoryInput struct {
	ID     string  `json:"id" jsonschema:"category id (cat_…)"`
	Name   *string `json:"name,omitempty"`
	Color  *string `json:"color,omitempty" jsonschema:"hex color, e.g. #7c6cff"`
	DryRun bool    `json:"dryRun,omitempty"`
}

// UpdateCategory renames or recolours a category.
func (s *Service) UpdateCategory(ctx context.Context, actor Actor, in UpdateCategoryInput) (domain.Category, error) {
	var cat domain.Category
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		found := false
		for _, r := range mustList(q.ListCategories(ctx, actor.HouseholdID)) {
			if r.ID == in.ID {
				cat, found = store.CategoryFromRow(r), true
			}
		}
		if !found {
			return domain.Missing("category %s: not found", in.ID)
		}
		before := cat
		set(&cat.Name, in.Name)
		set(&cat.Color, in.Color)
		if cat.Name = strings.TrimSpace(cat.Name); cat.Name == "" {
			return domain.Invalid("name is required")
		}
		if err := q.UpdateCategory(ctx, db.UpdateCategoryParams{Name: cat.Name, Color: cat.Color, ID: cat.ID, HouseholdID: actor.HouseholdID}); err != nil {
			return domain.Invalid("a category named %q already exists", cat.Name)
		}
		if _, err := c.record(q, "category.update", "category", cat.ID, before, cat); err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.CategorySaved, HouseholdID: cat.HouseholdID, EntityID: cat.ID})
		return nil
	})
	return cat, err
}

// DeleteCategoryInput removes a category.
type DeleteCategoryInput struct {
	ID     string `json:"id" jsonschema:"category id (cat_…)"`
	DryRun bool   `json:"dryRun,omitempty"`
}

// DeleteCategory removes a category; its subscriptions become uncategorized.
func (s *Service) DeleteCategory(ctx context.Context, actor Actor, in DeleteCategoryInput) (DeleteResult, error) {
	var out DeleteResult
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		if err := q.DeleteCategory(ctx, db.DeleteCategoryParams{ID: in.ID, HouseholdID: actor.HouseholdID}); err != nil {
			return err
		}
		id, err := c.record(q, "category.delete", "category", in.ID, map[string]string{"id": in.ID}, nil)
		if err != nil {
			return err
		}
		c.emit(bus.Event{Type: bus.CategorySaved, HouseholdID: actor.HouseholdID, EntityID: in.ID})
		out = DeleteResult{Deleted: in.ID, AuditID: id}
		return nil
	})
	return out, err
}
