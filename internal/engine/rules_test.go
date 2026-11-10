package engine

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"pgregory.net/rapid"
)

// --- weekday of the month and weekend shifts: known calendars -----------------

func upcoming(t *testing.T, r Rule, from string, n int) []Date {
	t.Helper()
	s, err := NewSchedule(r)
	if err != nil {
		t.Fatal(err)
	}
	return s.Upcoming(d(from), n)
}

func TestThirdWednesday(t *testing.T) {
	r := Rule{Interval: 1, Unit: Month, Anchor: d("2026-01-21"), DayRule: NthWeekday}
	if r.Week() != 3 {
		t.Fatalf("week = %d", r.Week())
	}
	want := dates("2026-01-21", "2026-02-18", "2026-03-18", "2026-04-15", "2026-05-20", "2026-06-17",
		"2026-07-15", "2026-08-19", "2026-09-16", "2026-10-21", "2026-11-18", "2026-12-16", "2027-01-20")
	if diff := cmp.Diff(want, upcoming(t, r, "2026-01-01", 13), dateCmp); diff != "" {
		t.Fatalf("third Wednesday (-want +got):\n%s", diff)
	}
}

func TestLastFridayEveryTwoMonths(t *testing.T) {
	r := Rule{Interval: 2, Unit: Month, Anchor: d("2026-01-30"), DayRule: LastWeekday}
	want := dates("2026-01-30", "2026-03-27", "2026-05-29", "2026-07-31", "2026-09-25", "2026-11-27", "2027-01-29")
	if diff := cmp.Diff(want, upcoming(t, r, "2026-01-01", 7), dateCmp); diff != "" {
		t.Fatalf("last Friday every 2 months (-want +got):\n%s", diff)
	}
}

func TestFourthThursdayOfNovemberYearly(t *testing.T) {
	r := Rule{Interval: 1, Unit: Year, Anchor: d("2026-11-26"), DayRule: NthWeekday}
	want := dates("2026-11-26", "2027-11-25", "2028-11-23", "2029-11-22", "2030-11-28")
	if diff := cmp.Diff(want, upcoming(t, r, "2026-01-01", 5), dateCmp); diff != "" {
		t.Fatalf("fourth Thursday of November (-want +got):\n%s", diff)
	}
}

func TestLastWeekdayInFebruary(t *testing.T) {
	// The last Sunday: February 2026 has exactly four weeks.
	r := Rule{Interval: 1, Unit: Month, Anchor: d("2026-02-22"), DayRule: LastWeekday}
	want := dates("2026-02-22", "2026-03-29", "2026-04-26")
	if diff := cmp.Diff(want, upcoming(t, r, "2026-02-01", 3), dateCmp); diff != "" {
		t.Fatalf("last Sunday (-want +got):\n%s", diff)
	}
	if got := WeekdayOfMonth(2028, time.February, time.Tuesday, -1); got != d("2028-02-29") {
		t.Fatalf("last Tuesday of Feb 2028 = %s", got)
	}
}

func TestWeekendShift(t *testing.T) {
	// The 15th: Aug 15 2026 is a Saturday, Nov 15 a Sunday.
	next := Rule{Interval: 1, Unit: Month, Anchor: d("2026-07-15"), Shift: NextBusinessDay}
	want := dates("2026-07-15", "2026-08-17", "2026-09-15", "2026-10-15", "2026-11-16", "2026-12-15")
	if diff := cmp.Diff(want, upcoming(t, next, "2026-07-01", 6), dateCmp); diff != "" {
		t.Fatalf("next business day (-want +got):\n%s", diff)
	}
	prev := next
	prev.Shift = PreviousBusinessDay
	want = dates("2026-07-15", "2026-08-14", "2026-09-15", "2026-10-15", "2026-11-13", "2026-12-15")
	if diff := cmp.Diff(want, upcoming(t, prev, "2026-07-01", 6), dateCmp); diff != "" {
		t.Fatalf("previous business day (-want +got):\n%s", diff)
	}
	// Weekly on Saturdays, moved to Mondays: still one charge a week.
	weekly := Rule{Interval: 1, Unit: Week, Anchor: d("2026-11-07"), Shift: NextBusinessDay}
	want = dates("2026-11-09", "2026-11-16", "2026-11-23")
	if diff := cmp.Diff(want, upcoming(t, weekly, "2026-11-01", 3), dateCmp); diff != "" {
		t.Fatalf("weekly (-want +got):\n%s", diff)
	}
}

func TestShiftNeverDrifts(t *testing.T) {
	// Anchored on Saturday Jan 31 2026: shifted back to Friday, but February
	// is still charged on its last day and March on the 31st.
	r := Rule{Interval: 1, Unit: Month, Anchor: d("2026-01-31"), Shift: PreviousBusinessDay}
	want := dates("2026-01-30", "2026-02-27", "2026-03-31", "2026-04-30", "2026-05-29", "2026-06-30")
	if diff := cmp.Diff(want, upcoming(t, r, "2026-01-01", 6), dateCmp); diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}
	s, _ := NewSchedule(r)
	if s.First() != d("2026-01-30") {
		t.Fatalf("first charge = %s", s.First())
	}
	if n, ok := s.NominalOf(d("2026-05-29")); !ok || n != d("2026-05-31") {
		t.Fatalf("nominal of May 29 = %s %v", n, ok)
	}
	if _, ok := s.NominalOf(d("2026-05-31")); ok {
		t.Fatal("nothing is due on May 31")
	}
}

func TestReanchor(t *testing.T) {
	r := Rule{Interval: 1, Unit: Month, Anchor: d("2026-01-21"), DayRule: NthWeekday, Shift: NextBusinessDay}
	cases := []struct {
		anchor string
		rule   DayRule
		week   int
	}{
		{"2026-03-12", NthWeekday, 2},   // second Thursday
		{"2026-03-26", NthWeekday, 4},   // fourth (and last) Thursday
		{"2026-03-31", LastWeekday, -1}, // fifth Tuesday: becomes the last
	}
	for _, c := range cases {
		got := r.Reanchor(d(c.anchor))
		if got.DayRule != c.rule || got.Week() != c.week || got.Shift != NextBusinessDay || got.Validate() != nil {
			t.Errorf("reanchor %s = %+v (week %d)", c.anchor, got, got.Week())
		}
	}
	last := Rule{Interval: 1, Unit: Month, Anchor: d("2026-01-30"), DayRule: LastWeekday}
	if got := last.Reanchor(d("2026-03-26")); got.DayRule != LastWeekday {
		t.Errorf("a last-weekday rule moved to another last week stays last: %+v", got)
	}
	plain := Rule{Interval: 1, Unit: Month, Anchor: d("2026-01-30")}
	if got := plain.Reanchor(d("2026-03-26")); got.DayRule != DayOfMonth {
		t.Errorf("a day-of-month rule stays one: %+v", got)
	}
}

func TestChangeRuleKeepsPast(t *testing.T) {
	s, _ := NewSchedule(Rule{Interval: 1, Unit: Month, Anchor: d("2026-01-15")})
	// From the August charge on: the third Wednesday.
	r := Rule{Interval: 1, Unit: Month, Anchor: d("2026-08-19"), DayRule: NthWeekday}
	s2, err := s.ChangeRule(d("2026-08-15"), r)
	if err != nil {
		t.Fatal(err)
	}
	want := dates("2026-06-15", "2026-07-15", "2026-08-19", "2026-09-16")
	if diff := cmp.Diff(want, s2.Between(d("2026-06-01"), d("2026-09-30")), dateCmp); diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}
	// A shift to the previous business day can make the new version start
	// before its anchor; the old version stops before it.
	r = Rule{Interval: 1, Unit: Month, Anchor: d("2026-08-16"), Shift: PreviousBusinessDay} // a Sunday
	s3, err := s.ChangeRule(d("2026-08-15"), r)
	if err != nil {
		t.Fatal(err)
	}
	want = dates("2026-07-15", "2026-08-14", "2026-09-16")
	if diff := cmp.Diff(want, s3.Between(d("2026-07-01"), d("2026-09-30")), dateCmp); diff != "" {
		t.Fatalf("(-want +got):\n%s", diff)
	}
}

func TestRuleValidation(t *testing.T) {
	bad := []Rule{
		{Interval: 1, Unit: Week, Anchor: d("2026-01-21"), DayRule: NthWeekday},
		{Interval: 1, Unit: Month, Anchor: d("2026-01-29"), DayRule: NthWeekday}, // fifth Thursday
		{Interval: 1, Unit: Month, Anchor: d("2026-01-21"), DayRule: LastWeekday},
		{Interval: 1, Unit: Month, Anchor: d("2026-01-21"), DayRule: "second"},
		{Interval: 1, Unit: Month, Anchor: d("2026-01-21"), Shift: "nearest"},
		{Interval: 2, Unit: Day, Anchor: d("2026-01-21"), Shift: NextBusinessDay},
	}
	for _, r := range bad {
		if _, err := NewSchedule(r); err == nil {
			t.Errorf("expected error for %+v", r)
		}
	}
}

// --- properties --------------------------------------------------------------

// Every month of every year has a 1st–4th and a last of every weekday, and
// WeekdayOfMonth finds it.
func TestWeekdayOfMonthAllMonths(t *testing.T) {
	for y := 1990; y <= 2100; y++ {
		for m := time.January; m <= time.December; m++ {
			for wd := time.Sunday; wd <= time.Saturday; wd++ {
				for _, n := range []int{1, 2, 3, 4, -1} {
					got := WeekdayOfMonth(y, m, wd, n)
					if got.Year() != y || got.Month() != m || got.Weekday() != wd {
						t.Fatalf("%d-%02d %s #%d = %s", y, m, wd, n, got)
					}
					if n > 0 && (got.Day()-1)/7+1 != n {
						t.Fatalf("%d-%02d %s #%d = %s, wrong week", y, m, wd, n, got)
					}
					if n == -1 && got.Day()+7 <= DaysIn(y, m) {
						t.Fatalf("%d-%02d last %s = %s, not the last", y, m, wd, got)
					}
				}
			}
		}
	}
}

// A weekday rule never drifts: the n-th date is in the anchor's month plus
// n intervals, on the anchor's weekday, in the same week of the month.
func TestPropWeekdayRuleNeverDrifts(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		r := genWeekdayRule(t)
		n := rapid.IntRange(0, 600).Draw(t, "n")
		o := r.Nominal(n)
		step := r.Interval
		if r.Unit == Year {
			step *= 12
		}
		if got := monthsBetween(r.Anchor, o); got != n*step {
			t.Fatalf("nominal %d of %+v is %s, %d months after the anchor, want %d", n, r, o, got, n*step)
		}
		if o.Weekday() != r.Anchor.Weekday() {
			t.Fatalf("nominal %d of %+v is a %s", n, r, o.Weekday())
		}
		last := o.Day()+7 > DaysIn(o.Year(), o.Month())
		switch {
		case r.DayRule == LastWeekday && !last:
			t.Fatalf("nominal %d of %+v (%s) is not the last of its month", n, r, o)
		case r.DayRule == NthWeekday && (o.Day()-1)/7 != (r.Anchor.Day()-1)/7:
			t.Fatalf("nominal %d of %+v (%s) is not in week %d", n, r, o, r.Week())
		}
	})
}

func genWeekdayRule(t *rapid.T) Rule {
	a := genDate(t, "anchor")
	r := Rule{
		Interval: rapid.IntRange(1, 18).Draw(t, "interval"),
		Unit:     rapid.SampledFrom([]Unit{Month, Year}).Draw(t, "unit"),
		DayRule:  rapid.SampledFrom([]DayRule{NthWeekday, LastWeekday}).Draw(t, "dayRule"),
		Shift:    rapid.SampledFrom([]Shift{NoShift, NextBusinessDay, PreviousBusinessDay}).Draw(t, "shift"),
	}
	if n := DaysIn(a.Year(), a.Month()); r.DayRule == LastWeekday {
		r.Anchor = MustDate(a.Year(), a.Month(), rapid.IntRange(n-6, n).Draw(t, "day"))
	} else {
		r.Anchor = MustDate(a.Year(), a.Month(), rapid.IntRange(1, 28).Draw(t, "day"))
	}
	return r
}

// The shift only moves a due date off the weekend, by at most two days in its
// direction; the dates it is computed from are the same with or without it.
func TestPropShiftMovesOnlyTheDueDate(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		r := genRule(t)
		n := rapid.IntRange(0, 600).Draw(t, "n")
		plain := r
		plain.Shift = NoShift
		nominal, due := r.Nominal(n), r.Occurrence(n)
		if nominal != plain.Occurrence(n) {
			t.Fatalf("the shift changed the nominal date: %s vs %s", nominal, plain.Occurrence(n))
		}
		wd := due.Weekday()
		weekend := wd == time.Saturday || wd == time.Sunday
		moved := nominal.DaysUntil(due)
		switch r.Shift {
		case NoShift:
			if moved != 0 {
				t.Fatalf("moved without a shift")
			}
		case NextBusinessDay:
			if weekend || moved < 0 || moved > 2 {
				t.Fatalf("%s → %s is not the next business day", nominal, due)
			}
		case PreviousBusinessDay:
			if weekend || moved > 0 || moved < -2 {
				t.Fatalf("%s → %s is not the previous business day", nominal, due)
			}
		}
		if nw := nominal.Weekday(); nw != time.Saturday && nw != time.Sunday && moved != 0 {
			t.Fatalf("a business day %s was moved to %s", nominal, due)
		}
	})
}

// Every schedule, including ones changed mid-way with other rules, yields
// strictly increasing due dates (never two charges on one day, never out of
// order), and every due date maps back to its nominal date.
func TestPropScheduleStrictlyIncreasing(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s, err := NewSchedule(genRule(t))
		if err != nil {
			t.Fatal(err)
		}
		for i := range rapid.IntRange(0, 3).Draw(t, "changes") {
			up := s.Upcoming(s.First(), 30)
			at := up[rapid.IntRange(1, len(up)-1).Draw(t, "at")]
			nr := genRule(t).Reanchor(at.AddDays(rapid.IntRange(-3, 40).Draw(t, "move")))
			if nr.Validate() != nil || !start(nr).After(s.First()) {
				continue
			}
			if s, err = s.ChangeRule(at, nr); err != nil {
				t.Fatalf("change %d: %v", i, err)
			}
			if next, _ := s.NextOnOrAfter(min2(at, start(nr))); next != nr.Occurrence(0) {
				t.Fatalf("first charge of the new rule = %s, want %s", next, nr.Occurrence(0))
			}
		}
		got := s.Upcoming(s.First(), 200)
		for i := 1; i < len(got); i++ {
			if !got[i].After(got[i-1]) {
				t.Fatalf("charge %d (%s) is not after %s: %+v", i, got[i], got[i-1], s)
			}
		}
		for _, due := range got {
			if nominal, ok := s.NominalOf(due); !ok || s.Current(due).Shift.Apply(nominal) != due {
				t.Fatalf("nominal of %s = %s %v", due, nominal, ok)
			}
		}
	})
}
