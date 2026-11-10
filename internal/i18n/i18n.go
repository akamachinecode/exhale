// Package i18n holds the languages Exhale speaks and the translations of
// what chat and the service say (ADR 0023).
//
// Texts are written in English where they are used; English is the key of
// every translation. A language is registered in languages.json (read by
// the web too) and translated in locales/<code>.json, a flat JSON object
// from the English text to its translation. An entry may be left empty
// (""): the text falls back to English. A text counted with a number may
// give one translation per plural category instead of a string, for
// example {"one": "…", "few": "…", "many": "…", "other": "…"}.
//
// docs/guides/translating.md explains how to add a language.
package i18n

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"strings"
)

// Source is the language the texts are written in.
const Source = "en"

// Language is a registered language.
type Language struct {
	// Code is the ISO 639-1 code: "es", "pt".
	Code string `json:"code"`
	// Name is the language's own name: "Español", "Português".
	Name string `json:"name"`
	// Complete languages must translate every text (a test fails
	// otherwise); an incomplete one ships with English in the gaps.
	Complete bool `json:"complete,omitempty"`
}

// Entry is the translation of one text: a string, or one string per plural
// category.
type Entry struct {
	Text  string
	Forms map[string]string
}

// Empty reports whether the entry has no translation yet.
func (e Entry) Empty() bool {
	if e.Forms == nil {
		return e.Text == ""
	}
	for _, s := range e.Forms {
		if s != "" {
			return false
		}
	}
	return true
}

// UnmarshalJSON reads a string or an object of plural forms.
func (e *Entry) UnmarshalJSON(b []byte) error {
	if bytes.HasPrefix(bytes.TrimSpace(b), []byte("{")) {
		return json.Unmarshal(b, &e.Forms)
	}
	return json.Unmarshal(b, &e.Text)
}

// MarshalJSON writes what UnmarshalJSON reads.
func (e Entry) MarshalJSON() ([]byte, error) {
	if e.Forms != nil {
		return json.Marshal(e.Forms)
	}
	return json.Marshal(e.Text)
}

var (
	//go:embed languages.json
	languagesJSON []byte
	//go:embed locales
	localeFiles embed.FS

	languages []Language
	catalogs  = map[string]map[string]Entry{}
)

func init() {
	if err := json.Unmarshal(languagesJSON, &languages); err != nil {
		panic("i18n: languages.json: " + err.Error())
	}
	for _, l := range languages {
		if l.Code == Source {
			continue
		}
		b, err := localeFiles.ReadFile("locales/" + l.Code + ".json")
		if err != nil {
			continue // an incomplete language may not have a file yet; the test says so
		}
		c := map[string]Entry{}
		if err := json.Unmarshal(b, &c); err != nil {
			panic(fmt.Sprintf("i18n: locales/%s.json: %v", l.Code, err))
		}
		catalogs[l.Code] = c
	}
}

// Languages returns the registered languages, English first.
func Languages() []Language { return append([]Language(nil), languages...) }

// Supported reports whether a language is registered.
func Supported(code string) bool {
	for _, l := range languages {
		if l.Code == code {
			return true
		}
	}
	return false
}

// Name returns the own name of a registered language, or the code.
func Name(code string) string {
	for _, l := range languages {
		if l.Code == code {
			return l.Name
		}
	}
	return code
}

// Match returns the first registered language among codes such as "pt-BR"
// or "es" (what a browser or a chat platform reports), or English.
func Match(codes ...string) string {
	for _, c := range codes {
		base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(c)), "-")
		base, _, _ = strings.Cut(base, "_")
		if base != "" && Supported(base) {
			return base
		}
	}
	return Source
}

// Catalog returns the translations of a language, keyed by the English
// text (nil for English). The map is shared: do not change it.
func Catalog(lang string) map[string]Entry { return catalogs[lang] }

// Translate returns the translation of an English text, or the text itself.
// A text with plural forms gives its "other" form.
func Translate(lang, text string) string {
	e, ok := catalogs[lang][text]
	switch {
	case !ok:
		return text
	case e.Forms != nil:
		if s := e.Forms[Other]; s != "" {
			return s
		}
		return text
	case e.Text != "":
		return e.Text
	}
	return text
}

// Plural picks the text for count n: the English one or other form,
// translated by the plural category of n in the language. When the other
// form has a translation per category, that wins.
func Plural(lang string, n int, one, other string) string {
	c := catalogs[lang]
	cat := PluralCategory(lang, n)
	if e := c[other]; e.Forms != nil {
		if s := e.Forms[cat]; s != "" {
			return s
		}
		if s := e.Forms[Other]; s != "" {
			return s
		}
	}
	if c[one].Empty() && c[other].Empty() {
		cat = oneOther(n) // not translated: the English text, counted in English
	}
	if cat == One {
		return Translate(lang, one)
	}
	return Translate(lang, other)
}
