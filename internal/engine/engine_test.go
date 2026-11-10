package engine

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

func d(s string) Date {
	v, err := ParseDate(s)
	if err != nil {
		panic(err)
	}
	return v
}

func dates(ss ...string) []Date {
	out := make([]Date, len(ss))
	for i, s := range ss {
		out[i] = d(s)
	}
	return out
}

var dateCmp = cmp.Comparer(func(a, b Date) bool { return a == b })

func TestMonthEndClampingWithoutDrift(t *testing.T) {
	r := Rule{Interval: 1, Unit: Month, Anchor: d("2026-01-31")}
	s, err := NewSchedule(r)
	if err != nil {
		t.Fatal(err)
	}
	got := s.Upcoming(d("2026-01-01"), 6)
	want := dates("2026-01-31", "2026-02-28", "2026-03-31", "2026-04-30", "2026-05-31", "2026-06-30")
	if diff := cmp.Diff(want, got, dateCmp); diff != "" {
		t.Fatalf("monthly from Jan 31 (-want +got):\n%s", diff)
	}
}

func TestLeapDayYearly(t *testing.T) {
	r := Rule{Interval: 1, Unit: Year, Anchor: d("2024-02-29")}
	s, _ := NewSchedule(r)
	got := s.Upcoming(d("2024-01-01"), 5)
	want := dates("2024-02-29", "2025-02-28", "2026-02-28", "2027-02-28", "2028-02-29")
	if diff := cmp.Diff(want, got, dateCmp); diff != "" {
		t.Fatalf("yearly from leap day (-want +got):\n%s", diff)
	}
}

func TestEveryFourWeeksAndQuarterly(t *testing.T) {
	w, _ := NewSchedule(Rule{Interval: 4, Unit: Week, Anchor: d("2026-11-05")})
	if diff := cmp.Diff(dates("2026-11-05", "2026-12-03", "2026-12-31"), w.Upcoming(d("2026-11-01"), 3), dateCmp); diff != "" {
		t.Fatalf("every 4 weeks:\n%s", diff)
	}
	q, _ := NewSchedule(Rule{Interval: 3, Unit: Month, Anchor: d("2026-11-30")})
	if diff := cmp.Diff(dates("2026-11-30", "2027-02-28", "2027-05-30"), q.Upcoming(d("2026-11-01"), 3), dateCmp); diff != "" {
		t.Fatalf("quarterly:\n%s", diff)
	}
}

func TestChangeFromThisAndFollowing(t *testing.T) {
	s, _ := NewSchedule(Rule{Interval: 1, Unit: Month, Anchor: d("2026-01-08")})
	// From the November charge on, it is charged on the 15th.
	s2, err := s.ChangeFrom(d("2026-11-08"), d("2026-11-15"), 1, Month)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Between(d("2026-09-01"), d("2027-01-31"))
	want := dates("2026-09-08", "2026-10-08", "2026-11-15", "2026-12-15", "2027-01-15")
	if diff := cmp.Diff(want, got, dateCmp); diff != "" {
		t.Fatalf("later day (-want +got):\n%s", diff)
	}
	// Moving earlier: from the November charge on, it is charged on the 2nd.
	s3, err := s.ChangeFrom(d("2026-11-08"), d("2026-11-02"), 1, Month)
	if err != nil {
		t.Fatal(err)
	}
	got = s3.Between(d("2026-09-01"), d("2026-12-31"))
	want = dates("2026-09-08", "2026-10-08", "2026-11-02", "2026-12-02")
	if diff := cmp.Diff(want, got, dateCmp); diff != "" {
		t.Fatalf("earlier day (-want +got):\n%s", diff)
	}
	if r := s3.Current(d("2026-10-20")); r.Anchor != d("2026-01-08") {
		t.Fatalf("rule before change = %v", r)
	}
}

func TestLimits(t *testing.T) {
	s, _ := NewSchedule(Rule{Interval: 1, Unit: Month, Anchor: d("2026-01-10")})
	s.MaxCharges = 3
	if got := s.Upcoming(d("2026-02-01"), 10); len(got) != 2 {
		t.Fatalf("max charges: got %v", got)
	}
	s.MaxCharges = 0
	s.Until = d("2026-04-10")
	if diff := cmp.Diff(dates("2026-01-10", "2026-02-10", "2026-03-10", "2026-04-10"), s.Upcoming(d("2026-01-01"), 10), dateCmp); diff != "" {
		t.Fatalf("until (inclusive):\n%s", diff)
	}
	if _, ok := s.NextOnOrAfter(d("2026-04-11")); ok {
		t.Fatal("no charge expected after until")
	}
}

func TestValidation(t *testing.T) {
	bad := []Rule{
		{Interval: 0, Unit: Month, Anchor: d("2026-01-01")},
		{Interval: 1, Unit: "fortnight", Anchor: d("2026-01-01")},
		{Interval: 1, Unit: Month},
	}
	for _, r := range bad {
		if _, err := NewSchedule(r); err == nil {
			t.Errorf("expected error for %+v", r)
		}
	}
	if _, err := NewDate(2026, time.February, 29); err == nil {
		t.Error("2026-02-29 must be invalid")
	}
}

// --- property-based tests --------------------------------------------------

func genDate(t *rapid.T, label string) Date {
	y := rapid.IntRange(1990, 2100).Draw(t, label+"_y")
	m := time.Month(rapid.IntRange(1, 12).Draw(t, label+"_m"))
	day := rapid.IntRange(1, DaysIn(y, m)).Draw(t, label+"_d")
	return MustDate(y, m, day)
}

// genRule draws any valid rule: every unit, day rule and weekend shift.
func genRule(t *rapid.T) Rule {
	r := Rule{
		Interval: rapid.IntRange(1, 18).Draw(t, "interval"),
		Unit:     rapid.SampledFrom([]Unit{Day, Week, Month, Year}).Draw(t, "unit"),
		Anchor:   genDate(t, "anchor"),
		Shift:    rapid.SampledFrom([]Shift{NoShift, NextBusinessDay, PreviousBusinessDay}).Draw(t, "shift"),
	}
	if r.Unit == Day && r.Interval < 3 {
		r.Shift = NoShift
	}
	if r.Unit == Month || r.Unit == Year {
		r.DayRule = rapid.SampledFrom([]DayRule{DayOfMonth, NthWeekday, LastWeekday}).Draw(t, "dayRule")
		a := r.Anchor
		switch r.DayRule {
		case NthWeekday:
			r.Anchor = MustDate(a.Year(), a.Month(), rapid.IntRange(1, 28).Draw(t, "nthDay"))
		case LastWeekday:
			n := DaysIn(a.Year(), a.Month())
			r.Anchor = MustDate(a.Year(), a.Month(), rapid.IntRange(n-6, n).Draw(t, "lastDay"))
		}
	}
	if err := r.Validate(); err != nil {
		t.Fatalf("generated an invalid rule %+v: %v", r, err)
	}
	return r
}

func TestPropOccurrencesStrictlyIncrease(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		r := genRule(t)
		n := rapid.IntRange(0, 300).Draw(t, "n")
		if !r.Occurrence(n + 1).After(r.Occurrence(n)) {
			t.Fatalf("occurrence %d (%s) not after %d (%s)", n+1, r.Occurrence(n+1), n, r.Occurrence(n))
		}
	})
}

func TestPropMonthlyNeverDrifts(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		r := genRule(t)
		r.Unit = rapid.SampledFrom([]Unit{Month, Year}).Draw(t, "munit")
		r.DayRule = DayOfMonth
		n := rapid.IntRange(0, 500).Draw(t, "n")
		o := r.Nominal(n)
		want := min(r.Anchor.Day(), DaysIn(o.Year(), o.Month()))
		if o.Day() != want {
			t.Fatalf("occurrence %d of %+v is %s, want day %d", n, r, o, want)
		}
		step := r.Interval
		if r.Unit == Year {
			step *= 12
		}
		if got := monthsBetween(r.Anchor, o); got != n*step {
			t.Fatalf("occurrence %d is %d months after the anchor, want %d", n, got, n*step)
		}
	})
}

func TestPropNextOnOrAfterIsTight(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		r := genRule(t)
		from := genDate(t, "from")
		next, n := r.NextOnOrAfter(from)
		if next.Before(from) {
			t.Fatalf("next %s is before %s", next, from)
		}
		if next != r.Occurrence(n) {
			t.Fatalf("index mismatch: %s vs occurrence %d = %s", next, n, r.Occurrence(n))
		}
		if n > 0 && !r.Occurrence(n-1).Before(from) {
			t.Fatalf("previous occurrence %s is not before %s", r.Occurrence(n-1), from)
		}
	})
}

func TestPropBetweenMatchesUpcoming(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, err := NewSchedule(genRule(t))
		if err != nil {
			t.Fatal(err)
		}
		from := genDate(t, "from")
		up := s.Upcoming(from, 12)
		if len(up) == 0 {
			t.Fatal("unbounded schedule returned no charges")
		}
		got := s.Between(from, up[len(up)-1])
		if diff := cmp.Diff(up, got, dateCmp); diff != "" {
			t.Fatalf("Between vs Upcoming:\n%s", diff)
		}
	})
}

func TestPropChangeFromKeepsPastAndFollowsNewRule(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, _ := NewSchedule(genRule(t))
		k := rapid.IntRange(1, 40).Draw(t, "k")
		at := s.Upcoming(s.First(), k+1)[k]
		shift := rapid.IntRange(-10, 40).Draw(t, "shift")
		newAnchor := at.AddDays(shift)
		if !newAnchor.After(s.First()) {
			t.Skip("new anchor must be after the first charge")
		}
		nr := genRule(t)
		s2, err := s.ChangeFrom(at, newAnchor, nr.Interval, nr.Unit)
		if err != nil {
			t.Fatal(err)
		}
		cut := min2(at, newAnchor)
		before := s.Between(s.First(), cut.AddDays(-1))
		if diff := cmp.Diff(before, s2.Between(s2.First(), cut.AddDays(-1)), dateCmp); diff != "" {
			t.Fatalf("past charges changed:\n%s", diff)
		}
		next, ok := s2.NextOnOrAfter(cut)
		if !ok || next != newAnchor {
			t.Fatalf("first charge after the change = %s, want %s", next, newAnchor)
		}
	})
}

func min2(a, b Date) Date {
	if a.Before(b) {
		return a
	}
	return b
}

func TestPropDateRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := genDate(t, "a")
		n := rapid.IntRange(-5000, 5000).Draw(t, "n")
		if b := a.AddDays(n); a.DaysUntil(b) != n || b.AddDays(-n) != a {
			t.Fatalf("AddDays/DaysUntil mismatch for %s + %d", a, n)
		}
		p, err := ParseDate(a.String())
		if err != nil || p != a {
			t.Fatalf("parse round trip %s -> %s (%v)", a, p, err)
		}
	})
}
