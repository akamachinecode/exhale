package domain

import (
	"strings"
	"unicode/utf8"

	"github.com/akamachinecode/exhale/internal/engine"
	"github.com/akamachinecode/exhale/internal/money"
)

// Tags are free labels, many per subscription ("work", "family", "to
// review"). They group across categories; totals stay by category, so a
// subscription is never counted twice.
const (
	MaxTags      = 20
	MaxTagLength = 40
)

// NormalizeTags trims each tag, collapses inner spaces, drops empty ones and
// duplicates (case-insensitive, the first spelling wins) and keeps the order.
func NormalizeTags(tags []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.Join(strings.Fields(t), " ")
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	return out
}

// SameTag compares two tags the way Exhale does: ignoring case.
func SameTag(a, b string) bool { return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b)) }

// HasTag reports whether tags contains tag.
func HasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if SameTag(t, tag) {
			return true
		}
	}
	return false
}

// ValidateTag checks one tag. Commas are not allowed: they separate tags in
// CSV files and in chat.
func ValidateTag(t string) error {
	switch {
	case strings.TrimSpace(t) == "":
		return invalid("a tag cannot be empty")
	case utf8.RuneCountInString(t) > MaxTagLength:
		return invalid("tag %q is longer than %d characters", t, MaxTagLength)
	case strings.ContainsAny(t, ",;"):
		return invalid("tag %q cannot contain a comma or semicolon", t)
	}
	return nil
}

// AltPrice is another price the provider offers for the same service, e.g.
// "also 99 € per year" for a monthly plan. Exhale only uses it to show what
// switching would save; it never guesses one.
type AltPrice struct {
	Price money.Money `json:"price"`
	// Unit is the period the price covers: month or year (every 1 unit).
	Unit engine.Unit `json:"unit" enum:"month,year"`
}

// validateExtras checks the tags and the alternative price.
func (d Details) validateExtras() error {
	if len(d.Tags) > MaxTags {
		return invalid("at most %d tags", MaxTags)
	}
	for _, t := range d.Tags {
		if err := ValidateTag(t); err != nil {
			return err
		}
	}
	if a := d.AltPrice; a != nil {
		if a.Price.Minor <= 0 || a.Price.Currency == "" {
			return invalid("the other price must be more than zero")
		}
		if a.Unit != engine.Month && a.Unit != engine.Year {
			return invalid("the other price must be per month or per year")
		}
	}
	return nil
}
