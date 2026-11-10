package actions

import (
	"fmt"
	"time"

	"github.com/akamachinecode/exhale/internal/engine"
)

// Messages are written in English in the code and translated here: tr looks
// the English text up in the member's language and falls back to English.
// The test in i18n_test.go fails when a text has no translation, or a
// translation is no longer used.

// tr returns format in the given language, with args applied.
func tr(locale, format string, args ...any) string {
	if t, ok := catalogs[locale][format]; ok {
		format = t
	}
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// catalogs maps a language to its translations, keyed by the English text.
var catalogs = map[string]map[string]string{"es": spanish}

var (
	weekdaysShort = map[string][7]string{
		"en": {"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"},
		"es": {"dom", "lun", "mar", "mié", "jue", "vie", "sáb"},
	}
	monthsShort = map[string][12]string{
		"en": {"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"},
		"es": {"ene", "feb", "mar", "abr", "may", "jun", "jul", "ago", "sep", "oct", "nov", "dic"},
	}
	monthsLong = map[string][12]string{
		"en": {"January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"},
		"es": {"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"},
	}
)

func lang(locale string) string {
	if _, ok := monthsShort[locale]; ok {
		return locale
	}
	return "en"
}

func weekday(d engine.Date, locale string) string {
	return weekdaysShort[lang(locale)][d.At(12, 0, time.UTC).Weekday()]
}

// longDate formats "Mon, 2 Jan 2026" ("lun, 2 ene 2026").
func longDate(d engine.Date, locale string) string {
	return fmt.Sprintf("%s, %d %s %d", weekday(d, locale), d.Day(), monthsShort[lang(locale)][d.Month()-1], d.Year())
}

// shortDate formats "Mon 2 Jan" ("lun 2 ene").
func shortDate(d engine.Date, locale string) string {
	return fmt.Sprintf("%s %d %s", weekday(d, locale), d.Day(), monthsShort[lang(locale)][d.Month()-1])
}

// monthTitle formats "January 2026" ("enero 2026").
func monthTitle(d engine.Date, locale string) string {
	return fmt.Sprintf("%s %d", monthsLong[lang(locale)][d.Month()-1], d.Year())
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

// plural picks one of two English words by count and translates it.
func plural(locale string, n int, one, many string) string {
	if n == 1 {
		return tr(locale, one)
	}
	return tr(locale, many)
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
