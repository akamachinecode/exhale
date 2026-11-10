package actions

import (
	"fmt"
	"strings"
	"time"

	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/i18n"
)

// Messages are written in English in the code and translated by
// internal/i18n: tr looks the English text up in the member's language and
// falls back to English. The test in i18n_test.go fails when a text has no
// translation in a complete language, or a translation is no longer used.

// tr returns format in the given language, with args applied.
func tr(locale, format string, args ...any) string {
	format = i18n.Translate(locale, format)
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// Names of days and months are translated as one list each, so a language
// can give them in its own form.
const (
	weekdaysShortList = "Sun, Mon, Tue, Wed, Thu, Fri, Sat"
	weekdaysLongList  = "Sunday, Monday, Tuesday, Wednesday, Thursday, Friday, Saturday"
	monthsShortList   = "Jan, Feb, Mar, Apr, May, Jun, Jul, Aug, Sep, Oct, Nov, Dec"
	monthsLongList    = "January, February, March, April, May, June, July, August, September, October, November, December"
)

// listItem returns item i of a translated list; a translation with the
// wrong number of items falls back to English (the test catches it).
func listItem(locale, list string, i int) string {
	items := strings.Split(tr(locale, list), ",")
	if n := strings.Count(list, ",") + 1; len(items) != n {
		items = strings.Split(list, ",")
	}
	return strings.TrimSpace(items[i])
}

func weekday(d engine.Date, locale string) string {
	return listItem(locale, weekdaysShortList, int(d.At(12, 0, time.UTC).Weekday()))
}

func weekdayLong(wd time.Weekday, locale string) string {
	return listItem(locale, weekdaysLongList, int(wd))
}

func monthShort(d engine.Date, locale string) string {
	return listItem(locale, monthsShortList, int(d.Month())-1)
}

// longDate formats "Mon, 2 Jan 2026" ("lun, 2 ene 2026").
func longDate(d engine.Date, locale string) string {
	return fmt.Sprintf("%s, %d %s %d", weekday(d, locale), d.Day(), monthShort(d, locale), d.Year())
}

// shortDate formats "Mon 2 Jan" ("lun 2 ene").
func shortDate(d engine.Date, locale string) string {
	return fmt.Sprintf("%s %d %s", weekday(d, locale), d.Day(), monthShort(d, locale))
}

// monthTitle formats "January 2026" ("enero 2026").
func monthTitle(d engine.Date, locale string) string {
	return fmt.Sprintf("%s %d", listItem(locale, monthsLongList, int(d.Month())-1), d.Year())
}

// when says how far a day is: "today", "tomorrow", "in 3 days", "2 days ago".
func when(days int, locale string) string {
	switch {
	case days == 0:
		return tr(locale, "today")
	case days == 1:
		return tr(locale, "tomorrow")
	case days < 0:
		return tr(locale, "%d days ago", -days)
	}
	return tr(locale, "in %d days", days)
}

// plural picks one of two English words by count and translates it with
// the plural rules of the language.
func plural(locale string, n int, one, many string) string {
	return i18n.Plural(locale, n, one, many)
}

// cadenceText translates the service's description of a billing cycle
// ("monthly", "every 3 months").
func cadenceText(cadence, locale string) string {
	var n int
	var unit string
	if _, err := fmt.Sscanf(cadence, "every %d %s", &n, &unit); err == nil {
		return tr(locale, "every %d "+unit, n)
	}
	return tr(locale, cadence)
}
