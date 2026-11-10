package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Sentences people read, in any language (ADR 0023).
//
// A Text keeps a sentence as its English format and its arguments, so each
// edge renders it in the reader's language: chat with the catalogs of
// internal/i18n, the web with their copy in web/src/locales/server. Error()
// stays English, which is what MCP agents and logs get.
//
// Write errors people may see with Invalid, NotFound, Conflict, Missing or
// Fail, never fmt.Errorf with a sentinel: the translation test finds every
// format written this way and fails when one has no Spanish.

// Text is a sentence: Format is the English text with fmt verbs and the key
// of its translations; Args fill the verbs. An argument that is a Word, a
// Text or an error carrying a Text is translated too.
type Text struct {
	Format string
	Args   []any
}

// T returns a Text.
func T(format string, args ...any) Text { return Text{Format: format, Args: args} }

// Word is an argument from a closed set (a status, the name of a field)
// that is translated as well.
type Word string

// String is the sentence in English.
func (t Text) String() string { return t.Render(nil) }

// IsZero reports whether t is empty.
func (t Text) IsZero() bool { return t.Format == "" }

// Render writes the sentence, looking every format and word up with
// translate (nil: English).
func (t Text) Render(translate func(string) string) string {
	format := t.Format
	if translate != nil {
		format = translate(format)
	}
	if len(t.Args) == 0 {
		return format
	}
	args := make([]any, len(t.Args))
	for i, a := range t.Args {
		args[i] = renderArg(a, translate)
	}
	return fmt.Sprintf(format, args...)
}

func renderArg(a any, translate func(string) string) any {
	switch v := a.(type) {
	case Word:
		if translate != nil {
			return translate(string(v))
		}
		return string(v)
	case Text:
		return v.Render(translate)
	case error:
		if t, ok := TextOf(v); ok {
			return t.Render(translate)
		}
		return v.Error()
	}
	return a
}

// Error is a failure people can be shown in their language. Kind is the
// sentinel errors.Is matches: ErrValidation, ErrNotFound, ErrConflict, or
// another one such as app.ErrForbidden.
type Error struct {
	Kind error
	Text Text
	// bare: Error() is the text alone, without the kind in front.
	bare bool
}

func (e *Error) Error() string {
	if e.bare || e.Kind == nil {
		return e.Text.String()
	}
	return e.Kind.Error() + ": " + e.Text.String()
}

// Unwrap returns the kind, for errors.Is.
func (e *Error) Unwrap() error { return e.Kind }

// Invalid is a validation error: "validation failed: <text>".
func Invalid(format string, args ...any) error {
	return &Error{Kind: ErrValidation, Text: T(format, args...)}
}

// NotFound is "not found: <text>".
func NotFound(format string, args ...any) error {
	return &Error{Kind: ErrNotFound, Text: T(format, args...)}
}

// Missing is a not-found error whose text says it all, for example
// "subscription sub_x: not found".
func Missing(format string, args ...any) error {
	return &Error{Kind: ErrNotFound, Text: T(format, args...), bare: true}
}

// Conflict is "conflict: <text>".
func Conflict(format string, args ...any) error {
	return &Error{Kind: ErrConflict, Text: T(format, args...)}
}

// Fail is an error of another kind (app.ErrForbidden): "<kind>: <text>".
func Fail(kind error, format string, args ...any) error {
	return &Error{Kind: kind, Text: T(format, args...)}
}

// TextOf returns what an error says to people, if it was written with one
// of the constructors above (possibly wrapped).
func TextOf(err error) (Text, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e.Text, true
	}
	return Text{}, false
}

// --- the web's form ------------------------------------------------------------

// Wire is a Text as the web receives it: Key is the format with {0}, {1}…
// in place of the verbs (the key of the web's catalogs), and each argument
// is either a string, already formatted, or a nested Wire to translate.
type Wire struct {
	Key  string `json:"key"`
	Args []any  `json:"args"`
}

var verbRe = regexp.MustCompile(`%(\[(\d+)\])?([-+# 0]*[0-9]*(?:\.[0-9]+)?)([a-zA-Z%])`)

// WebKey turns a Go format into the web's: "unknown currency %q" becomes
// "unknown currency {0}".
func WebKey(format string) string {
	n := 0
	return verbRe.ReplaceAllStringFunc(format, func(v string) string {
		m := verbRe.FindStringSubmatch(v)
		if m[4] == "%" {
			return "%"
		}
		i := n
		if m[2] != "" {
			i, _ = strconv.Atoi(m[2])
			i--
		}
		n = i + 1
		return "{" + strconv.Itoa(i) + "}"
	})
}

// Wire returns the web's form of t.
func (t Text) Wire() Wire {
	w := Wire{Key: WebKey(t.Format), Args: []any{}}
	n := 0
	for _, m := range verbRe.FindAllStringSubmatch(t.Format, -1) {
		if m[4] == "%" {
			continue
		}
		i := n
		if m[2] != "" {
			i, _ = strconv.Atoi(m[2])
			i--
		}
		n = i + 1
		for len(w.Args) <= i {
			w.Args = append(w.Args, "")
		}
		if i >= len(t.Args) {
			continue
		}
		switch v := t.Args[i].(type) {
		case Word:
			w.Args[i] = Wire{Key: string(v), Args: []any{}}
		case Text:
			w.Args[i] = v.Wire()
		case error:
			if inner, ok := TextOf(v); ok {
				w.Args[i] = inner.Wire()
			} else {
				w.Args[i] = v.Error()
			}
		default:
			w.Args[i] = fmt.Sprintf("%"+m[3]+m[4], v)
		}
	}
	return w
}

// MarshalJSON sends the web's form.
func (t Text) MarshalJSON() ([]byte, error) { return json.Marshal(t.Wire()) }

// --- saving a Text --------------------------------------------------------------

// Saved is a Text in a form that survives a round trip through JSON, for
// texts kept in the database (a proposal waiting for confirmation).
type Saved struct {
	F string     `json:"f"`
	A []savedArg `json:"a,omitempty"`
}

type savedArg struct {
	S *string  `json:"s,omitempty"`
	I *int64   `json:"i,omitempty"`
	N *float64 `json:"n,omitempty"`
	W *string  `json:"w,omitempty"`
	T *Saved   `json:"t,omitempty"`
}

// Save returns the savable form of t. Arguments that are not strings,
// numbers, words or texts are saved as their English text.
func (t Text) Save() Saved {
	s := Saved{F: t.Format}
	for _, a := range t.Args {
		var sa savedArg
		switch v := a.(type) {
		case Word:
			w := string(v)
			sa.W = &w
		case Text:
			in := v.Save()
			sa.T = &in
		case error:
			if inner, ok := TextOf(v); ok {
				in := inner.Save()
				sa.T = &in
			} else {
				e := v.Error()
				sa.S = &e
			}
		case string:
			sa.S = &v
		case int:
			i := int64(v)
			sa.I = &i
		case int64:
			sa.I = &v
		case float64:
			sa.N = &v
		default:
			str := fmt.Sprint(v)
			sa.S = &str
		}
		s.A = append(s.A, sa)
	}
	return s
}

// Text returns the saved Text.
func (s Saved) Text() Text {
	t := Text{Format: s.F}
	for _, a := range s.A {
		switch {
		case a.W != nil:
			t.Args = append(t.Args, Word(*a.W))
		case a.T != nil:
			t.Args = append(t.Args, a.T.Text())
		case a.I != nil:
			t.Args = append(t.Args, *a.I)
		case a.N != nil:
			t.Args = append(t.Args, *a.N)
		case a.S != nil:
			t.Args = append(t.Args, *a.S)
		default:
			t.Args = append(t.Args, "")
		}
	}
	return t
}

// Capitalize upper-cases the first letter, for a sentence shown alone.
func Capitalize(s string) string {
	for i, r := range s {
		return strings.ToUpper(string(r)) + s[i+len(string(r)):]
	}
	return s
}
