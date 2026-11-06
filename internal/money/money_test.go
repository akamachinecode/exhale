package money

import (
	"testing"

	"pgregory.net/rapid"
)

func TestParseAndFormat(t *testing.T) {
	m := MustParse("12.99", "eur")
	if m.Minor != 1299 || m.Currency != "EUR" {
		t.Fatalf("got %+v", m)
	}
	if got := m.Format("es"); got != "12,99\u00a0€" { // CLDR uses a no-break space
		t.Errorf("es format = %q", got)
	}
	if got := m.Format("en"); got != "€12.99" {
		t.Errorf("en format = %q", got)
	}
	jpy := MustParse("1200", "JPY")
	if jpy.Minor != 1200 {
		t.Errorf("JPY has no minor units, got %d", jpy.Minor)
	}
	if _, err := Parse("10", "XXZ"); err == nil {
		t.Error("unknown currency must fail")
	}
}

func TestConvert(t *testing.T) {
	usd := MustParse("10.00", "USD")
	eur, err := usd.Convert("EUR", "0.9241")
	if err != nil {
		t.Fatal(err)
	}
	if eur.String() != "9.24 EUR" {
		t.Errorf("got %s", eur)
	}
	if _, err := usd.Add(eur); err == nil {
		t.Error("adding different currencies must fail")
	}
}

func TestPropMinorRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		code := rapid.SampledFrom([]string{"EUR", "USD", "GBP", "JPY", "KWD"}).Draw(t, "cur")
		minor := rapid.Int64Range(-1_000_000_000, 1_000_000_000).Draw(t, "minor")
		m, err := New(minor, code)
		if err != nil {
			t.Fatal(err)
		}
		back, err := Parse(m.Decimal(), code)
		if err != nil || back != m {
			t.Fatalf("%+v -> %q -> %+v (%v)", m, m.Decimal(), back, err)
		}
	})
}
