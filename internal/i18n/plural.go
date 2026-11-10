package i18n

// Plural categories, named as CLDR names them.
const (
	Zero  = "zero"
	One   = "one"
	Two   = "two"
	Few   = "few"
	Many  = "many"
	Other = "other"
)

// pluralRules gives the CLDR plural category of a whole number, per
// language. Chat only counts whole things, so the rules for fractions are
// left out. Copied from
// https://www.unicode.org/cldr/charts/latest/supplemental/language_plural_rules.html;
// plural_test.go compares them with what browsers say (Intl.PluralRules).
// A language that is registered must have a rule here.
var pluralRules = map[string]func(n int) string{
	"en": oneOther, "de": oneOther, "nl": oneOther, "sv": oneOther, "da": oneOther, "nb": oneOther,
	"fi": oneOther, "tr": oneOther, "el": oneOther, "hu": oneOther,
	"es": romance(false), "it": romance(false), "ca": romance(false),
	"pt": romance(true), "fr": romance(true),
	"pl": polish, "ru": eastSlavic, "uk": eastSlavic,
	"cs": westSlavic, "sk": westSlavic,
	"ro": romanian,
	"ja": onlyOther, "zh": onlyOther, "ko": onlyOther,
}

func onlyOther(int) string { return Other }

// oneOther: one is 1, everything else is other.
func oneOther(n int) string {
	if n == 1 {
		return One
	}
	return Other
}

// romance: one is 1 (or 0 and 1 when zeroIsOne), many is a whole number of
// millions ("1 millón de suscripciones"), the rest other.
func romance(zeroIsOne bool) func(int) string {
	return func(n int) string {
		switch {
		case n == 1, zeroIsOne && n == 0:
			return One
		case n != 0 && n%1000000 == 0:
			return Many
		}
		return Other
	}
}

func polish(n int) string {
	n = abs(n)
	switch d, h := n%10, n%100; {
	case n == 1:
		return One
	case d >= 2 && d <= 4 && (h < 12 || h > 14):
		return Few
	}
	return Many
}

func eastSlavic(n int) string {
	n = abs(n)
	switch d, h := n%10, n%100; {
	case d == 1 && h != 11:
		return One
	case d >= 2 && d <= 4 && (h < 12 || h > 14):
		return Few
	}
	return Many
}

func westSlavic(n int) string {
	switch {
	case n == 1:
		return One
	case n >= 2 && n <= 4:
		return Few
	}
	return Other
}

func romanian(n int) string {
	n = abs(n)
	switch h := n % 100; {
	case n == 1:
		return One
	case n == 0 || (h >= 1 && h <= 19):
		return Few
	}
	return Other
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// PluralCategory returns the plural category of n in a language. A language
// without a rule counts like English.
func PluralCategory(lang string, n int) string {
	if r, ok := pluralRules[lang]; ok {
		return r(n)
	}
	return oneOther(n)
}

// PluralCategories lists the categories a language uses, "other" last.
func PluralCategories(lang string) []string {
	seen := map[string]bool{}
	for _, n := range []int{0, 1, 2, 3, 5, 11, 12, 21, 22, 25, 101, 111, 1000000} {
		seen[PluralCategory(lang, n)] = true
	}
	var out []string
	for _, c := range []string{Zero, One, Two, Few, Many} {
		if seen[c] {
			out = append(out, c)
		}
	}
	return append(out, Other)
}

// HasPluralRule reports whether the table above knows the language.
func HasPluralRule(lang string) bool { _, ok := pluralRules[lang]; return ok }
