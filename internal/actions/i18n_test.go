package actions

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// dynamicKeys are translated texts whose English form is not a literal at
// the call site (statuses and billing cycles coming from the service).
var dynamicKeys = []string{
	cancelHint, "paid", "pending", "failed", "skipped", "daily", "weekly", "monthly", "yearly",
	"every %d days", "every %d weeks", "every %d months", "every %d years",
	// Command descriptions (app.go: Commands).
	"Open Exhale: spend, next charges, everything", "Your subscriptions", "Charges in the next 30 days",
	"Spend per month, category and payment method", "Add a subscription, step by step",
	"What deserves a look: trials, deadlines, price rises", "Cards and accounts that pay", "Reminder time, payday, currency, language",
}

// TestEveryMessageIsTranslated keeps the Spanish catalog complete: every
// text passed to tr or plural has a translation, and no translation is left
// behind when a text changes.
func TestEveryMessageIsTranslated(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0) //nolint:staticcheck // one package, no build tags
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, k := range dynamicKeys {
		used[k] = true
	}
	for _, c := range Commands {
		if !used[c.Description] {
			t.Errorf("command /%s: add its description to dynamicKeys and to the Spanish catalog", c.Command)
		}
	}
	lit := func(e ast.Expr) (string, bool) {
		b, ok := e.(*ast.BasicLit)
		if !ok || b.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(b.Value)
		return s, err == nil
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				fn, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				switch {
				case fn.Name == "tr" && len(call.Args) >= 2:
					if s, ok := lit(call.Args[1]); ok {
						used[s] = true
					}
				case fn.Name == "plural" && len(call.Args) == 4:
					for _, a := range call.Args[2:] {
						if s, ok := lit(a); ok {
							used[s] = true
						}
					}
				}
				return true
			})
		}
	}
	var missing, stale []string
	for k := range used {
		if _, ok := spanish[k]; !ok {
			missing = append(missing, k)
		}
	}
	for k := range spanish {
		if !used[k] {
			stale = append(stale, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("no Spanish translation for:\n  %s", strings.Join(missing, "\n  "))
	}
	if len(stale) > 0 {
		t.Errorf("Spanish translations no longer used:\n  %s", strings.Join(stale, "\n  "))
	}
	// A translation must take the same arguments as the English text.
	for en, es := range spanish {
		if verbs(en) != verbs(es) {
			t.Errorf("arguments differ:\n  en %q\n  es %q", en, es)
		}
	}
}

// verbs lists the formatting verbs of a text in order ("sd%" …).
func verbs(s string) string {
	var out []byte
	for i := 0; i < len(s)-1; i++ {
		if s[i] == '%' {
			out = append(out, s[i+1])
			i++
		}
	}
	return string(out)
}
