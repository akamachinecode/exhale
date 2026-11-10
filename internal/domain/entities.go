package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
)

// ErrValidation wraps every business-rule violation.
var ErrValidation = errors.New("validation failed")

// ErrNotFound is returned when an entity does not exist (or is not visible).
var ErrNotFound = errors.New("not found")

// ErrConflict is returned on concurrent modification.
var ErrConflict = errors.New("conflict")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}

// Household is one Exhale instance's family or group (ADR 0006).
type Household struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Currency string `json:"currency"` // main currency, ISO 4217
	Timezone string `json:"timezone"` // IANA name, e.g. "Europe/Madrid"
	Locale   string `json:"locale"`   // e.g. "es", "en"
	// Default time of day for reminders, in Timezone (09:00 by default).
	ReminderHour   int `json:"reminderHour"`
	ReminderMinute int `json:"reminderMinute"`
	// Payday is the day of the month the household gets paid (0: not set;
	// 31 means the last day of shorter months).
	Payday int `json:"payday,omitempty"`
	// An unanswered "was it charged?" is asked ConfirmTimes times in all,
	// every ConfirmEveryDays days. New subscriptions copy it.
	ConfirmTimes     int `json:"confirmTimes"`
	ConfirmEveryDays int `json:"confirmEveryDays"`
}

// NextPayday returns the next payday strictly after day d, if one is set.
func (h Household) NextPayday(d engine.Date) (engine.Date, bool) {
	if h.Payday < 1 || h.Payday > 31 {
		return engine.Date{}, false
	}
	y, m := d.Year(), d.Month()
	for range 3 {
		day := min(h.Payday, engine.DaysIn(y, m))
		if p := engine.MustDate(y, m, day); p.After(d) {
			return p, true
		}
		if m++; m > 12 {
			y, m = y+1, 1
		}
	}
	return engine.Date{}, false
}

// Location returns the household time zone (UTC if unknown).
func (h Household) Location() *time.Location {
	if loc, err := time.LoadLocation(h.Timezone); err == nil {
		return loc
	}
	return time.UTC
}

// Role of a household member.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

// Member is a person in the household.
type Member struct {
	ID          string `json:"id"`
	HouseholdID string `json:"householdId"`
	Name        string `json:"name"`
	Email       string `json:"email,omitempty"`
	Role        Role   `json:"role"`
}

// PaymentMethodKind classifies payment methods.
type PaymentMethodKind string

const (
	KindCard   PaymentMethodKind = "card"
	KindBank   PaymentMethodKind = "bank"
	KindPayPal PaymentMethodKind = "paypal"
	KindOther  PaymentMethodKind = "other"
)

// PaymentMethod is a card, account or wallet that pays subscriptions.
type PaymentMethod struct {
	ID          string            `json:"id"`
	HouseholdID string            `json:"householdId"`
	Name        string            `json:"name"` // "Revolut", "Visa"
	Kind        PaymentMethodKind `json:"kind"`
	Last4       string            `json:"last4,omitempty"`
	ExpiresOn   engine.Date       `json:"expiresOn,omitzero"` // last valid day
	OwnerID     string            `json:"ownerId,omitempty"`
}

// Label returns "Revolut ••0937".
func (p PaymentMethod) Label() string {
	if p.Last4 == "" {
		return p.Name
	}
	return p.Name + " ••" + p.Last4
}

// Category groups subscriptions for statistics.
type Category struct {
	ID          string `json:"id"`
	HouseholdID string `json:"householdId"`
	Name        string `json:"name"`
	Color       string `json:"color,omitempty"`
}

// DefaultCategories are seeded for new households.
var DefaultCategories = []string{"Streaming", "Music", "Software", "Cloud & Dev", "AI", "News", "Gaming", "Health & Fitness", "Utilities", "Other"}

// Visibility of a subscription inside the household.
type Visibility string

const (
	VisibilityShared  Visibility = "shared"
	VisibilityPrivate Visibility = "private"
)

// SubscriptionStatus is the lifecycle state of a subscription.
type SubscriptionStatus string

const (
	StatusActive    SubscriptionStatus = "active"
	StatusPaused    SubscriptionStatus = "paused"
	StatusCancelled SubscriptionStatus = "cancelled"
)

// ReminderPolicy says when to remind about each charge.
type ReminderPolicy struct {
	DaysBefore []int `json:"daysBefore"` // e.g. [3, 0]: three days before and on the day
	Hour       int   `json:"hour"`       // local time of day in the household zone
	Minute     int   `json:"minute"`
	// ConfirmAfterDays asks "was it charged?" this many days after the due
	// date; < 0 disables the question.
	ConfirmAfterDays int `json:"confirmAfterDays"`
	// ConfirmTimes is how many times "was it charged?" is asked while nobody
	// answers (1: once), ConfirmEveryDays the days between two asks.
	ConfirmTimes     int `json:"confirmTimes"`
	ConfirmEveryDays int `json:"confirmEveryDays"`
}

// DefaultReminderPolicy: three days before and on the day at 09:00, then ask
// for confirmation one day after.
func DefaultReminderPolicy() ReminderPolicy {
	return ReminderPolicy{DaysBefore: []int{3, 0}, Hour: 9, Minute: 0, ConfirmAfterDays: 1, ConfirmTimes: DefaultConfirmTimes, ConfirmEveryDays: DefaultConfirmEveryDays}
}

// The unanswered "was it charged?" question is asked again every day, up to
// three times in all, unless the household chooses otherwise.
const (
	DefaultConfirmTimes     = 3
	DefaultConfirmEveryDays = 1
	MaxConfirmTimes         = 10
	MaxConfirmEveryDays     = 30
)

// ValidateConfirmRepeat checks how often an unanswered question repeats.
func ValidateConfirmRepeat(times, everyDays int) error {
	if times < 1 || times > MaxConfirmTimes {
		return invalid("the question can be asked 1 to %d times, got %d", MaxConfirmTimes, times)
	}
	if everyDays < 1 || everyDays > MaxConfirmEveryDays {
		return invalid("days between questions must be between 1 and %d, got %d", MaxConfirmEveryDays, everyDays)
	}
	return nil
}

// ConfirmAsks returns the dates "was it charged?" is asked for a charge due
// on due: the first ask and its repeats.
func (p ReminderPolicy) ConfirmAsks(due engine.Date) []engine.Date {
	if p.ConfirmAfterDays < 0 {
		return nil
	}
	times, every := max(p.ConfirmTimes, 1), max(p.ConfirmEveryDays, 1)
	out := make([]engine.Date, 0, times)
	for i := range times {
		out = append(out, due.AddDays(p.ConfirmAfterDays+i*every))
	}
	return out
}

// Validate checks the policy.
func (p ReminderPolicy) Validate() error {
	if p.Hour < 0 || p.Hour > 23 || p.Minute < 0 || p.Minute > 59 {
		return invalid("reminder time %02d:%02d is not a valid time of day", p.Hour, p.Minute)
	}
	for _, d := range p.DaysBefore {
		if d < 0 || d > 60 {
			return invalid("reminder days before must be between 0 and 60, got %d", d)
		}
	}
	if p.ConfirmAfterDays > 30 {
		return invalid("confirmation delay must be at most 30 days")
	}
	if p.ConfirmTimes != 0 || p.ConfirmEveryDays != 0 {
		return ValidateConfirmRepeat(p.ConfirmTimes, p.ConfirmEveryDays)
	}
	return nil
}

// Renewal says what happens when a period ends.
type Renewal string

const (
	// RenewalAuto: the provider charges again by itself (the default).
	RenewalAuto Renewal = "auto"
	// RenewalManual: it expires unless someone renews it (domains bought for
	// one year, licences, prepaid plans, a passport).
	RenewalManual Renewal = "manual"
)

// Share is the part of a subscription's cost one member carries.
type Share struct {
	MemberID string `json:"memberId"`
	// Weight is relative: two members with weight 1 pay half each; weights 2
	// and 1 split two thirds and one third.
	Weight int `json:"weight"`
}

// PriceChange is a price that starts applying on a known date.
type PriceChange struct {
	From  engine.Date `json:"from"`
	Price money.Money `json:"price"`
}

// Usage is the member's last answer to "still using this?".
type Usage struct {
	Using bool      `json:"using"`
	At    time.Time `json:"at"`
}

// Details are the contract facts of a subscription beyond price and
// schedule. They decide which dates matter: the end of a free trial, the
// last day to cancel, the day something expires.
type Details struct {
	// StartedOn is when the member first subscribed (may be long before the
	// first charge Exhale knows about).
	StartedOn engine.Date `json:"startedOn,omitzero"`
	// TrialEndsOn is the day a free trial turns into the first paid charge.
	TrialEndsOn engine.Date `json:"trialEndsOn,omitzero"`
	// NoticeDays is how many days before a renewal the provider must be told
	// to cancel (0: cancel any time before the charge).
	NoticeDays int     `json:"noticeDays,omitempty"`
	Renewal    Renewal `json:"renewal,omitempty"`
	// CancelURL is where the subscription is cancelled or managed.
	CancelURL string `json:"cancelUrl,omitempty"`
	// Account is the login the subscription lives under ("ana@work.com").
	Account string `json:"account,omitempty"`
	// Shares split the cost between members; empty means the payer carries it.
	Shares []Share `json:"shares,omitempty"`
	// NextPrice is a scheduled price change.
	NextPrice *PriceChange `json:"nextPrice,omitempty"`
	Usage     *Usage       `json:"usage,omitempty"`
	// Tags are free labels (tags.go).
	Tags []string `json:"tags,omitempty"`
	// AltPrice is another price the provider offers, e.g. yearly (tags.go).
	AltPrice *AltPrice `json:"altPrice,omitempty"`
}

// Subscription is a recurring payment.
type Subscription struct {
	ID              string             `json:"id"`
	HouseholdID     string             `json:"householdId"`
	Name            string             `json:"name"`
	Price           money.Money        `json:"price"`
	Schedule        engine.Schedule    `json:"schedule"`
	CategoryID      string             `json:"categoryId,omitempty"`
	PaymentMethodID string             `json:"paymentMethodId,omitempty"`
	PayerID         string             `json:"payerId,omitempty"`
	Visibility      Visibility         `json:"visibility"`
	Status          SubscriptionStatus `json:"status"`
	URL             string             `json:"url,omitempty"`
	Notes           string             `json:"notes,omitempty"`
	AutoConfirm     bool               `json:"autoConfirm"` // mark charges paid automatically on the due date
	Reminders       ReminderPolicy     `json:"reminders"`
	Details
	CreatedBy string    `json:"createdBy,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	Version   int       `json:"version"` // optimistic locking
}

// ManualRenewal reports whether the subscription expires unless renewed.
func (s Subscription) ManualRenewal() bool { return s.Renewal == RenewalManual }

// InTrial reports whether the free trial is still running on day d.
func (s Subscription) InTrial(d engine.Date) bool {
	return !s.TrialEndsOn.IsZero() && d.Before(s.TrialEndsOn)
}

// CancelDeadline returns the last day to cancel before the charge due on day d.
func (s Subscription) CancelDeadline(d engine.Date) engine.Date { return d.AddDays(-s.NoticeDays) }

// PriceOn returns the price of a charge scheduled on day d.
func (s Subscription) PriceOn(d engine.Date) money.Money {
	if s.NextPrice != nil && !d.Before(s.NextPrice.From) {
		return s.NextPrice.Price
	}
	return s.Price
}

// Validate checks the contract facts.
func (d Details) Validate() error {
	switch {
	case d.NoticeDays < 0 || d.NoticeDays > 366:
		return invalid("notice period must be between 0 and 366 days")
	case d.Renewal != "" && d.Renewal != RenewalAuto && d.Renewal != RenewalManual:
		return invalid("renewal must be auto or manual, got %q", d.Renewal)
	case len(d.CancelURL) > 500 || len(d.Account) > 200:
		return invalid("cancel link or account is too long")
	case d.CancelURL != "" && !strings.HasPrefix(d.CancelURL, "https://") && !strings.HasPrefix(d.CancelURL, "http://"):
		return invalid("cancel link must start with https:// or http://")
	}
	seen := map[string]bool{}
	for _, sh := range d.Shares {
		if sh.MemberID == "" || sh.Weight < 1 || sh.Weight > 1000 || seen[sh.MemberID] {
			return invalid("each share needs a different member and a weight between 1 and 1000")
		}
		seen[sh.MemberID] = true
	}
	return d.validateExtras()
}

// Validate checks the subscription's own fields.
func (s Subscription) Validate() error {
	name := strings.TrimSpace(s.Name)
	switch {
	case name == "" || len(name) > 120:
		return invalid("name must be 1-120 characters")
	case s.Price.IsZero() || s.Price.Minor < 0:
		return invalid("price must be zero or positive and have a currency")
	case s.Visibility != VisibilityShared && s.Visibility != VisibilityPrivate:
		return invalid("unknown visibility %q", s.Visibility)
	}
	switch s.Status {
	case StatusActive, StatusPaused, StatusCancelled:
	default:
		return invalid("unknown status %q", s.Status)
	}
	if err := s.Schedule.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrValidation, err)
	}
	if err := s.Details.Validate(); err != nil {
		return err
	}
	if s.NextPrice != nil && s.NextPrice.Price.Currency != s.Price.Currency {
		return invalid("a scheduled price must be in %s", s.Price.Currency)
	}
	if s.AltPrice != nil && s.AltPrice.Price.Currency != s.Price.Currency {
		return invalid("the other price must be in %s", s.Price.Currency)
	}
	return s.Reminders.Validate()
}

// VisibleTo reports whether member m may see the subscription.
func (s Subscription) VisibleTo(memberID string) bool {
	return s.Visibility == VisibilityShared || s.CreatedBy == memberID || s.PayerID == memberID
}
