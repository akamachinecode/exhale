// Package money (piece ⑩) represents exact amounts of money in a currency.
//
// Amounts are stored as integer minor units (cents) plus an ISO 4217 code,
// so they are exact, sortable and summable per currency. Arithmetic,
// conversion and locale formatting use bojanz/currency (decimal, CLDR data).
package money

import (
	"errors"
	"fmt"
	"strings"

	"github.com/bojanz/currency"
)

// Money is an exact amount in a currency. The zero value is "no amount".
type Money struct {
	Minor    int64  `json:"minor"`
	Currency string `json:"currency"`
}

// ErrInvalid is returned for unknown currencies or malformed amounts.
var ErrInvalid = errors.New("money: invalid amount")

// New returns minor units of the given currency.
func New(minor int64, code string) (Money, error) {
	code = strings.ToUpper(code)
	if !currency.IsValid(code) {
		return Money{}, fmt.Errorf("%w: unknown currency %q", ErrInvalid, code)
	}
	return Money{Minor: minor, Currency: code}, nil
}

// Parse parses a decimal string such as "12.99" in the given currency.
// The value is rounded to the currency's minor units.
func Parse(decimal, code string) (Money, error) {
	a, err := currency.NewAmount(strings.TrimSpace(decimal), strings.ToUpper(code))
	if err != nil {
		return Money{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return fromAmount(a.Round())
}

// MustParse is like Parse but panics on error. Use it in tests.
func MustParse(decimal, code string) Money {
	m, err := Parse(decimal, code)
	if err != nil {
		panic(err)
	}
	return m
}

func fromAmount(a currency.Amount) (Money, error) {
	minor, err := a.Int64()
	if err != nil {
		return Money{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return Money{Minor: minor, Currency: a.CurrencyCode()}, nil
}

// IsZero reports whether m is the zero value.
func (m Money) IsZero() bool { return m == Money{} }

// Amount returns the decimal amount.
func (m Money) Amount() currency.Amount {
	a, err := currency.NewAmountFromInt64(m.Minor, m.Currency)
	if err != nil {
		panic(fmt.Sprintf("money: invalid value %+v: %v", m, err))
	}
	return a
}

// Decimal returns the amount as a decimal string, e.g. "12.99".
func (m Money) Decimal() string { return m.Amount().Number() }

// String returns "12.99 EUR".
func (m Money) String() string {
	if m.IsZero() {
		return ""
	}
	return m.Decimal() + " " + m.Currency
}

// Format formats the amount for a locale such as "es" or "en-US"
// ("12,99 €" / "€12.99").
func (m Money) Format(locale string) string {
	return currency.NewFormatter(currency.NewLocale(locale)).Format(m.Amount())
}

// Add returns m + o. Both must share a currency.
func (m Money) Add(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, fmt.Errorf("%w: cannot add %s to %s", ErrInvalid, o.Currency, m.Currency)
	}
	return Money{Minor: m.Minor + o.Minor, Currency: m.Currency}, nil
}

// Convert converts m into currency `to` using rate (units of `to` per unit
// of m.Currency, as a decimal string). The result is rounded to the minor
// units of `to`.
func (m Money) Convert(to, rate string) (Money, error) {
	if strings.EqualFold(to, m.Currency) {
		return m, nil
	}
	a, err := m.Amount().Convert(strings.ToUpper(to), rate)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return fromAmount(a.Round())
}

// Scale returns m multiplied by a decimal factor (e.g. "0.0833333" to
// normalize a yearly price per month), rounded to minor units.
func (m Money) Scale(factor string) (Money, error) {
	a, err := m.Amount().Mul(factor)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	return fromAmount(a.Round())
}

// Sum adds amounts of the same currency.
func Sum(code string, ms ...Money) (Money, error) {
	total, err := New(0, code)
	if err != nil {
		return Money{}, err
	}
	for _, m := range ms {
		if total, err = total.Add(m); err != nil {
			return Money{}, err
		}
	}
	return total, nil
}
