package i18n

import (
	"strings"
	"testing"
)

// TestPluralRulesMatchBrowsers compares the table in plural.go with what
// Intl.PluralRules says (Node 26), so chat and the web count alike. Each
// letter is the category of one number in pluralNumbers: 1 one, F few,
// M many, . other.
func TestPluralRulesMatchBrowsers(t *testing.T) {
	pluralNumbers := []int{0, 1, 2, 3, 4, 5, 7, 11, 12, 14, 21, 22, 25, 100, 101, 102, 111, 112, 1000, 1000000, 2000000, 3000001}
	want := map[string]string{
		"en": ".1....................", "de": ".1....................", "nl": ".1....................",
		"sv": ".1....................", "da": ".1....................", "nb": ".1....................",
		"fi": ".1....................", "tr": ".1....................", "el": ".1....................",
		"hu": ".1....................",
		"it": ".1.................MM.", "es": ".1.................MM.", "ca": ".1.................MM.",
		"pt": "11.................MM.", "fr": "11.................MM.",
		"pl": "M1FFFMMMMMMFMMMFMMMMMM",
		"ru": "M1FFFMMMMM1FMM1FMMMMM1", "uk": "M1FFFMMMMM1FMM1FMMMMM1",
		"cs": ".1FFF.................", "sk": ".1FFF.................",
		"ro": "F1FFFFFFFF....FFFF...F",
		"ja": "......................", "zh": "......................", "ko": "......................",
	}
	letter := map[string]byte{One: '1', Few: 'F', Many: 'M', Other: '.'}
	for lang := range pluralRules {
		w, ok := want[lang]
		if !ok {
			t.Errorf("%s: add what Intl.PluralRules says to this test", lang)
			continue
		}
		var got strings.Builder
		for _, n := range pluralNumbers {
			got.WriteByte(letter[PluralCategory(lang, n)])
		}
		if got.String() != w {
			t.Errorf("%s: got %s, want %s", lang, got.String(), w)
		}
	}
}

func TestEveryLanguageCanBeCounted(t *testing.T) {
	for _, l := range Languages() {
		if !HasPluralRule(l.Code) {
			t.Errorf("%s: add its CLDR plural rule to plural.go", l.Code)
		}
	}
	if Languages()[0].Code != Source {
		t.Errorf("languages.json must list %s first", Source)
	}
}

func TestMatch(t *testing.T) {
	for in, want := range map[string]string{"es-ES": "es", "ES": "es", "es_AR": "es", "en-GB": "en", "xx": "en", "": "en"} {
		if got := Match(in); got != want {
			t.Errorf("Match(%q) = %q, want %q", in, got, want)
		}
	}
	if got := Match("xx", "es-MX"); got != "es" {
		t.Errorf("Match(xx, es-MX) = %q", got)
	}
}

func TestPlural(t *testing.T) {
	catalogs["xx"] = map[string]Entry{
		"%d files": {Forms: map[string]string{One: "%d plik", Few: "%d pliki", Many: "%d plików"}},
		"file":     {Text: "plik"},
		"files":    {Text: "pliki"},
	}
	pluralRules["xx"] = polish
	defer func() { delete(catalogs, "xx"); delete(pluralRules, "xx") }()
	for n, want := range map[int]string{1: "%d plik", 3: "%d pliki", 5: "%d plików"} {
		if got := Plural("xx", n, "%d file", "%d files"); got != want {
			t.Errorf("Plural(%d) = %q, want %q", n, got, want)
		}
	}
	if got := Plural("xx", 1, "file", "files"); got != "plik" {
		t.Errorf("singular = %q", got)
	}
	if got := Plural("es", 1000000, "subscription", "subscriptions"); got != "suscripciones" {
		t.Errorf("es many = %q", got)
	}
	if got := Plural("en", 1, "subscription", "subscriptions"); got != "subscription" {
		t.Errorf("en one = %q", got)
	}
}
