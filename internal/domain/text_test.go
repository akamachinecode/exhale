package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
)

func TestTextForms(t *testing.T) {
	inner := Invalid("%s: %q is not a date like 2026-01-31", Word("first charge"), "x")
	if inner.Error() != `validation failed: first charge: "x" is not a date like 2026-01-31` || !errors.Is(inner, ErrValidation) {
		t.Fatalf("Error() = %q", inner.Error())
	}
	wrapped := fmt.Errorf("saving: %w", Missing("charge %s: not found", "chg_1"))
	if wrapped.Error() != "saving: charge chg_1: not found" || !errors.Is(wrapped, ErrNotFound) {
		t.Fatalf("Missing: %q", wrapped.Error())
	}
	es := map[string]string{"%s: %q is not a date like 2026-01-31": "%s: %q no es una fecha como 2026-01-31", "first charge": "primer cobro", "reminder time %02d:%02d is not a valid time of day": "la hora %02d:%02d no vale"}
	tr := func(f string) string {
		if s, ok := es[f]; ok {
			return s
		}
		return f
	}
	tx, _ := TextOf(inner)
	if got := tx.Render(tr); got != `primer cobro: "x" no es una fecha como 2026-01-31` {
		t.Errorf("Render = %q", got)
	}
	if got := T("reminder time %02d:%02d is not a valid time of day", 7, 5).Render(tr); got != "la hora 07:05 no vale" {
		t.Errorf("Render = %q", got)
	}

	// The web's form: verbs become {n}, values are formatted, words and
	// texts nest.
	b, _ := json.Marshal(T("%d%% of %s: %v", 5, Word("charge"), inner))
	var w map[string]any
	_ = json.Unmarshal(b, &w)
	want := map[string]any{"key": "{0}% of {1}: {2}", "args": []any{"5", map[string]any{"key": "charge", "args": []any{}}, map[string]any{"key": "{0}: {1} is not a date like 2026-01-31", "args": []any{map[string]any{"key": "first charge", "args": []any{}}, `"x"`}}}}
	if !reflect.DeepEqual(w, want) {
		t.Errorf("wire = %s", b)
	}
	if k := WebKey("%[2]s before %[1]s"); k != "{1} before {0}" {
		t.Errorf("WebKey = %q", k)
	}

	// Saved texts survive JSON with their types.
	orig := T("%s, %d items, %s", Word("charge"), 3, T("%s", "x"))
	raw, _ := json.Marshal(orig.Save())
	var s Saved
	_ = json.Unmarshal(raw, &s)
	if got := s.Text().Render(tr); got != orig.Render(tr) || got != "charge, 3 items, x" {
		t.Errorf("saved = %q", got)
	}
}
