// Package engine is the billing time engine (piece ⑨): civil dates and
// billing-style recurrence. It is pure: no I/O, no clock, no time zones
// except when converting a Date to an instant.
package engine

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"time"
)

// Date is a civil calendar date (year, month, day) with no time and no time
// zone. A charge happens on a Date; a reminder happens at an instant.
//
// The zero value is not a valid date; use IsZero to detect it.
type Date struct {
	y int
	m time.Month
	d int
}

// ErrInvalidDate is returned when a year/month/day triple does not exist.
var ErrInvalidDate = errors.New("engine: invalid date")

// NewDate returns the date y-m-d, or ErrInvalidDate if it does not exist.
func NewDate(y int, m time.Month, d int) (Date, error) {
	if y < 1 || y > 9999 || m < time.January || m > time.December || d < 1 || d > DaysIn(y, m) {
		return Date{}, fmt.Errorf("%w: %04d-%02d-%02d", ErrInvalidDate, y, int(m), d)
	}
	return Date{y, m, d}, nil
}

// MustDate is like NewDate but panics on invalid input. Use it for constants
// and tests.
func MustDate(y int, m time.Month, d int) Date {
	dt, err := NewDate(y, m, d)
	if err != nil {
		panic(err)
	}
	return dt
}

// DateOf returns the civil date of t in t's location.
func DateOf(t time.Time) Date {
	y, m, d := t.Date()
	return Date{y, m, d}
}

// ParseDate parses an ISO 8601 calendar date (YYYY-MM-DD).
func ParseDate(s string) (Date, error) {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		return Date{}, fmt.Errorf("%w: %q", ErrInvalidDate, s)
	}
	return DateOf(t), nil
}

// DaysIn returns the number of days in month m of year y.
func DaysIn(y int, m time.Month) int {
	return time.Date(y, m+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func (d Date) Year() int         { return d.y }
func (d Date) Month() time.Month { return d.m }
func (d Date) Day() int          { return d.d }
func (d Date) IsZero() bool      { return d == Date{} }

// Weekday returns the day of the week of d.
func (d Date) Weekday() time.Weekday { return d.utc().Weekday() }

// String formats the date as YYYY-MM-DD.
func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return fmt.Sprintf("%04d-%02d-%02d", d.y, int(d.m), d.d)
}

// At returns the instant at hour:min on this date in loc. Wall-clock times
// that do not exist because of DST are normalized by the time package.
func (d Date) At(hour, min int, loc *time.Location) time.Time {
	return time.Date(d.y, d.m, d.d, hour, min, 0, 0, loc)
}

func (d Date) utc() time.Time { return time.Date(d.y, d.m, d.d, 0, 0, 0, 0, time.UTC) }

// AddDays returns the date n days after d (n may be negative).
func (d Date) AddDays(n int) Date { return DateOf(d.utc().AddDate(0, 0, n)) }

// DaysUntil returns the number of days from d to other (negative if other is
// before d).
func (d Date) DaysUntil(other Date) int {
	return int(other.utc().Sub(d.utc()).Hours() / 24)
}

// Compare returns -1, 0 or +1 depending on whether d is before, equal to or
// after other.
func (d Date) Compare(other Date) int {
	switch {
	case d.y != other.y:
		return cmpInt(d.y, other.y)
	case d.m != other.m:
		return cmpInt(int(d.m), int(other.m))
	default:
		return cmpInt(d.d, other.d)
	}
}

func (d Date) Before(other Date) bool { return d.Compare(other) < 0 }
func (d Date) After(other Date) bool  { return d.Compare(other) > 0 }

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// MarshalText implements encoding.TextMarshaler (used by JSON).
func (d Date) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// UnmarshalText implements encoding.TextUnmarshaler.
func (d *Date) UnmarshalText(b []byte) error {
	if len(b) == 0 {
		*d = Date{}
		return nil
	}
	v, err := ParseDate(string(b))
	if err != nil {
		return err
	}
	*d = v
	return nil
}

// Value implements driver.Valuer: dates are stored as TEXT (YYYY-MM-DD).
func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}
	return d.String(), nil
}

// Scan implements sql.Scanner.
func (d *Date) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*d = Date{}
		return nil
	case string:
		return d.UnmarshalText([]byte(v))
	case []byte:
		return d.UnmarshalText(v)
	case time.Time:
		*d = DateOf(v)
		return nil
	}
	return fmt.Errorf("engine: cannot scan %T into Date", src)
}
