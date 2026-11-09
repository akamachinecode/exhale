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
}

// DefaultReminderPolicy: three days before and on the day at 09:00, then ask
// for confirmation one day after.
func DefaultReminderPolicy() ReminderPolicy {
	return ReminderPolicy{DaysBefore: []int{3, 0}, Hour: 9, Minute: 0, ConfirmAfterDays: 1}
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
	return nil
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
	CreatedBy       string             `json:"createdBy,omitempty"`
	CreatedAt       time.Time          `json:"createdAt"`
	UpdatedAt       time.Time          `json:"updatedAt"`
	Version         int                `json:"version"` // optimistic locking
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
	return s.Reminders.Validate()
}

// VisibleTo reports whether member m may see the subscription.
func (s Subscription) VisibleTo(memberID string) bool {
	return s.Visibility == VisibilityShared || s.CreatedBy == memberID || s.PayerID == memberID
}
