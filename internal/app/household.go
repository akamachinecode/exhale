package app

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/bus"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
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
		return Actor{}, fmt.Errorf("%w: no household yet", domain.ErrNotFound)
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
	return Actor{}, fmt.Errorf("%w: household %s has no owner", domain.ErrNotFound, r.ID)
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
		return Actor{}, fmt.Errorf("%w: the owner's name is required", domain.ErrValidation)
	}
	if in.Currency == "" {
		return Actor{}, fmt.Errorf("%w: the main currency is required", domain.ErrValidation)
	}
	if _, err := money.New(0, in.Currency); err != nil {
		return Actor{}, fmt.Errorf("%w: %v", domain.ErrValidation, err)
	}
	if _, err := time.LoadLocation(in.Timezone); err != nil || in.Timezone == "" {
		return Actor{}, fmt.Errorf("%w: unknown time zone %q", domain.ErrValidation, in.Timezone)
	}
	in.Locale = cmp.Or(in.Locale, "en")
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
	return out, nil
}

// UpdateHouseholdInput changes household preferences. Only the fields you
// pass change.
type UpdateHouseholdInput struct {
	Name         *string `json:"name,omitempty" maxLength:"60"`
	Currency     *string `json:"currency,omitempty" jsonschema:"display currency (ISO 4217): totals are converted into it"`
	Timezone     *string `json:"timezone,omitempty" jsonschema:"IANA time zone, e.g. Europe/Madrid"`
	Locale       *string `json:"locale,omitempty" jsonschema:"language of reminders: en or es"`
	ReminderTime *string `json:"reminderTime,omitempty" jsonschema:"default time of day for reminders, HH:MM; subscriptions still on the previous default move with it"`
	DryRun       bool    `json:"dryRun,omitempty"`
}

// UpdateHousehold changes the display currency, time zone, language, name
// or default reminder time. Owners only.
func (s *Service) UpdateHousehold(ctx context.Context, actor Actor, in UpdateHouseholdInput) (HouseholdInfo, error) {
	moved := false
	err := s.write(ctx, actor, in.DryRun, func(q *db.Queries, c *change) error {
		if actor.Kind != ActorSystem {
			m, err := q.GetMember(ctx, actor.MemberID)
			if err != nil || m.Role != string(domain.RoleOwner) {
				return fmt.Errorf("%w: only a household owner can change its settings", ErrForbidden)
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
				return fmt.Errorf("%w: the name cannot be empty", domain.ErrValidation)
			}
		}
		if in.Currency != nil {
			h.Currency = strings.ToUpper(strings.TrimSpace(*in.Currency))
			if _, err := money.New(0, h.Currency); err != nil || h.Currency == "" {
				return fmt.Errorf("%w: unknown currency %q", domain.ErrValidation, *in.Currency)
			}
		}
		if in.Timezone != nil {
			if _, err := time.LoadLocation(*in.Timezone); err != nil || *in.Timezone == "" {
				return fmt.Errorf("%w: unknown time zone %q", domain.ErrValidation, *in.Timezone)
			}
			h.Timezone = *in.Timezone
		}
		if in.Locale != nil {
			if *in.Locale != "en" && *in.Locale != "es" {
				return fmt.Errorf("%w: language must be en or es", domain.ErrValidation)
			}
			h.Locale = *in.Locale
		}
		if in.ReminderTime != nil {
			t, err := time.Parse("15:04", strings.TrimSpace(*in.ReminderTime))
			if err != nil {
				return fmt.Errorf("%w: reminder time must look like 09:00", domain.ErrValidation)
			}
			h.ReminderHour, h.ReminderMinute = t.Hour(), t.Minute()
		}
		if err := q.UpdateHousehold(ctx, db.UpdateHouseholdParams{
			Name: h.Name, Currency: h.Currency, Timezone: h.Timezone, Locale: h.Locale,
			ReminderHour: int64(h.ReminderHour), ReminderMinute: int64(h.ReminderMinute), ID: h.ID,
		}); err != nil {
			return err
		}
		if h.ReminderHour != before.ReminderHour || h.ReminderMinute != before.ReminderMinute {
			rows, err := q.ListSubscriptions(ctx, h.ID)
			if err != nil {
				return err
			}
			for _, r := range rows {
				sub, err := store.SubscriptionFromRow(r)
				if err != nil {
					return err
				}
				if sub.Reminders.Hour == before.ReminderHour && sub.Reminders.Minute == before.ReminderMinute {
					sub.Reminders.Hour, sub.Reminders.Minute = h.ReminderHour, h.ReminderMinute
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
		return pm, fmt.Errorf("%w: unknown kind %q", domain.ErrValidation, in.Kind)
	}
	if pm.Name == "" || len(pm.Last4) > 4 {
		return pm, fmt.Errorf("%w: name is required and last4 has at most 4 digits", domain.ErrValidation)
	}
	if in.ExpiresOn != "" {
		var err error
		if pm.ExpiresOn, err = engine.ParseDate(in.ExpiresOn); err != nil {
			return pm, fmt.Errorf("%w: %v", domain.ErrValidation, err)
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
				return store.NotFound(err, "payment method "+pm.ID)
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
		return cat, fmt.Errorf("%w: name is required", domain.ErrValidation)
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
