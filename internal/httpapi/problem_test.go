package httpapi

import (
	"reflect"
	"testing"
)

// A failure of the service carries its sentence as a key and arguments, so
// the web shows it in the reader's language; detail stays English.
func TestProblemCarriesTranslatableText(t *testing.T) {
	srv, _, _ := setup(t, true)
	code, body := do(t, "POST", srv.URL+"/api/v1/subscriptions", map[string]any{"name": "Gym", "price": "10", "currency": "EUR", "firstCharge": "2026-13-40"}, nil)
	if code != 422 {
		t.Fatalf("status %d %v", code, body)
	}
	if body["detail"] != `First charge: "2026-13-40" is not a date like 2026-01-31` {
		t.Errorf("detail = %v", body["detail"])
	}
	want := map[string]any{"key": "{0}: {1} is not a date like 2026-01-31", "args": []any{map[string]any{"key": "first charge", "args": []any{}}, `"2026-13-40"`}}
	if !reflect.DeepEqual(body["text"], want) {
		t.Errorf("text = %#v", body["text"])
	}
}
