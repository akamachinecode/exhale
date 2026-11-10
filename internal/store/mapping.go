package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
	"github.com/akamachinecode/exhale/internal/store/db"
)

// TimeFormat is how instants are stored (UTC, sortable).
const TimeFormat = time.RFC3339Nano

// FormatTime formats an instant for storage.
func FormatTime(t time.Time) string { return t.UTC().Format(TimeFormat) }

// ParseTime parses a stored instant.
func ParseTime(s string) time.Time {
	t, _ := time.Parse(TimeFormat, s)
	return t
}

// NotFound converts sql.ErrNoRows into domain.ErrNotFound, with a sentence
// such as "subscription %s: not found" (see domain.Missing).
func NotFound(err error, format string, args ...any) error {
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Missing(format, args...)
	}
	return err
}

// Ptr returns a pointer to s, or nil if s is empty.
func Ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// Str dereferences a nullable string.
func Str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func DatePtr(d engine.Date) *string { return Ptr(d.String()) }

func mustDate(s string) engine.Date {
	d, _ := engine.ParseDate(s)
	return d
}

func HouseholdFromRow(r db.Household) domain.Household {
	return domain.Household{ID: r.ID, Name: r.Name, Currency: r.Currency, Timezone: r.Timezone, Locale: r.Locale,
		ReminderHour: int(r.ReminderHour), ReminderMinute: int(r.ReminderMinute), Payday: int(r.Payday),
		ConfirmTimes: int(r.ConfirmTimes), ConfirmEveryDays: int(r.ConfirmEveryDays)}
}

func MemberFromRow(r db.Member) domain.Member {
	return domain.Member{ID: r.ID, HouseholdID: r.HouseholdID, Name: r.Name, Email: r.Email, Role: domain.Role(r.Role)}
}

func CategoryFromRow(r db.Category) domain.Category {
	return domain.Category{ID: r.ID, HouseholdID: r.HouseholdID, Name: r.Name, Color: r.Color}
}

func PaymentMethodFromRow(r db.PaymentMethod) domain.PaymentMethod {
	return domain.PaymentMethod{
		ID: r.ID, HouseholdID: r.HouseholdID, Name: r.Name, Kind: domain.PaymentMethodKind(r.Kind),
		Last4: r.Last4, ExpiresOn: mustDate(Str(r.ExpiresOn)), OwnerID: Str(r.OwnerID),
	}
}

func SubscriptionFromRow(r db.Subscription) (domain.Subscription, error) {
	s := domain.Subscription{
		ID: r.ID, HouseholdID: r.HouseholdID, Name: r.Name,
		Price:      money.Money{Minor: r.PriceMinor, Currency: r.Currency},
		CategoryID: Str(r.CategoryID), PaymentMethodID: Str(r.PaymentMethodID), PayerID: Str(r.PayerID),
		Visibility: domain.Visibility(r.Visibility), Status: domain.SubscriptionStatus(r.Status),
		URL: r.Url, Notes: r.Notes, AutoConfirm: r.AutoConfirm != 0, CreatedBy: r.CreatedBy,
		CreatedAt: ParseTime(r.CreatedAt), UpdatedAt: ParseTime(r.UpdatedAt), Version: int(r.Version),
	}
	if err := json.Unmarshal([]byte(r.Schedule), &s.Schedule); err != nil {
		return s, fmt.Errorf("subscription %s: schedule: %w", r.ID, err)
	}
	// Policies saved before repeats existed ask three times, daily.
	s.Reminders.ConfirmTimes, s.Reminders.ConfirmEveryDays = domain.DefaultConfirmTimes, domain.DefaultConfirmEveryDays
	if err := json.Unmarshal([]byte(r.Reminders), &s.Reminders); err != nil {
		return s, fmt.Errorf("subscription %s: reminders: %w", r.ID, err)
	}
	if r.Details != "" {
		if err := json.Unmarshal([]byte(r.Details), &s.Details); err != nil {
			return s, fmt.Errorf("subscription %s: details: %w", r.ID, err)
		}
	}
	return s, nil
}

// SubscriptionJSON returns the JSON columns of a subscription.
func SubscriptionJSON(s domain.Subscription) (schedule, reminders, details string, err error) {
	sb, err := json.Marshal(s.Schedule)
	if err != nil {
		return "", "", "", err
	}
	rb, err := json.Marshal(s.Reminders)
	if err != nil {
		return "", "", "", err
	}
	db, err := json.Marshal(s.Details)
	return string(sb), string(rb), string(db), err
}

func ChargeFromRow(r db.Charge) domain.Charge {
	c := domain.Charge{
		ID: r.ID, SubscriptionID: r.SubscriptionID,
		OriginalDate: mustDate(r.OriginalDate), DueDate: mustDate(r.DueDate),
		Amount: money.Money{Minor: r.AmountMinor, Currency: r.Currency},
		Status: domain.ChargeStatus(r.Status), Note: r.Note, Decision: r.Decision,
		UpdatedAt: ParseTime(r.UpdatedAt), Version: int(r.Version),
	}
	if r.ChargedMinor != nil && r.ChargedCurrency != nil {
		c.ChargedAmount = money.Money{Minor: *r.ChargedMinor, Currency: *r.ChargedCurrency}
	}
	if r.ResolvedAt != nil {
		t := ParseTime(*r.ResolvedAt)
		c.ResolvedAt = &t
	}
	return c
}

// ChargeUpdate builds the update parameters for c (optimistic locking on
// c.Version).
func ChargeUpdate(c domain.Charge, now time.Time) db.UpdateChargeParams {
	p := db.UpdateChargeParams{
		DueDate: c.DueDate.String(), AmountMinor: c.Amount.Minor, Currency: c.Amount.Currency,
		Status: string(c.Status), Note: c.Note, Decision: c.Decision, UpdatedAt: FormatTime(now), ID: c.ID, Version: int64(c.Version),
	}
	if !c.ChargedAmount.IsZero() {
		m, cur := c.ChargedAmount.Minor, c.ChargedAmount.Currency
		p.ChargedMinor, p.ChargedCurrency = &m, &cur
	}
	if c.ResolvedAt != nil {
		p.ResolvedAt = Ptr(FormatTime(*c.ResolvedAt))
	}
	return p
}
