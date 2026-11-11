package actions

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/akamachinecode/exhale/internal/i18n"
)

// dynamicKeys are translated texts whose English form is not a literal at
// the call site (statuses and billing cycles coming from the service).
var dynamicKeys = []string{
	weekdaysShortList, weekdaysLongList, monthsShortList, monthsLongList,
	cancelHint, "paid", "pending", "failed", "skipped", "daily", "weekly", "monthly", "yearly",
	"every %d days", "every %d weeks", "every %d months", "every %d years",
	// Command descriptions (app.go: Commands).
	"Open Exhale: spend, next charges, everything", "Your subscriptions", "Charges in the next 30 days",
	"Spend per month, category and payment method", "Add a subscription, step by step",
	"What deserves a look: trials, deadlines, price rises", "Cards and accounts that pay", "Reminder time, payday, currency, language",
}

// TestEveryMessageIsTranslated checks the catalog of every registered
// language (internal/i18n/locales): every text passed to tr or plural, and
// every text of the service, has a translation; no translation is left
// behind when a text changes; a translation takes the same arguments as its
// English text. A complete language fails on a missing or unused text; an
// incomplete one only lists them (go test -v). With -update, the file of
// every incomplete language gets an empty entry for each missing text and
// loses its unused ones, so a translator sees what is left.
func TestEveryMessageIsTranslated(t *testing.T) {
	used, ones := chatTexts(t)
	for _, c := range Commands {
		if !used[c.Description] {
			t.Errorf("command /%s: add its description to dynamicKeys and to the catalogs", c.Command)
		}
	}
	for _, l := range i18n.Languages() {
		if l.Code == i18n.Source {
			continue
		}
		t.Run(l.Code, func(t *testing.T) {
			checkCatalog(t, l, "internal/i18n/locales/"+l.Code+".json", used, ones)
		})
	}
}

// chatTexts returns every English text chat may translate, and for each
// singular text passed to plural, its plural.
func chatTexts(t *testing.T) (map[string]bool, map[string]string) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, 0) //nolint:staticcheck // one package, no build tags
	if err != nil {
		t.Fatal(err)
	}
	// The desktop app's dialogs share the chat catalogs (ADR 0029).
	desk, err := parser.ParseDir(fset, filepath.Join("..", "desktop"), nil, 0) //nolint:staticcheck // every OS's files
	if err != nil {
		t.Fatal(err)
	}
	maps.Copy(pkgs, desk)
	used := map[string]bool{}
	ones := map[string]string{}
	for _, k := range dynamicKeys {
		used[k] = true
	}
	for k := range serverTexts(t) {
		used[k] = true
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
					one, ok1 := lit(call.Args[2])
					many, ok2 := lit(call.Args[3])
					if ok1 && ok2 {
						used[one], used[many] = true, true
						ones[one] = many
					}
				}
				return true
			})
		}
	}
	return used, ones
}

// checkCatalog checks one language's catalog against the texts in use.
func checkCatalog(t *testing.T, l i18n.Language, path string, used map[string]bool, ones map[string]string) {
	t.Helper()
	cat := i18n.Catalog(l.Code)
	if !i18n.HasPluralRule(l.Code) {
		t.Errorf("no plural rule for %s: add its CLDR rule to internal/i18n/plural.go", l.Code)
	}
	plurals := map[string]bool{}
	for _, many := range ones {
		plurals[many] = true
	}
	categories := map[string]bool{}
	for _, c := range i18n.PluralCategories(l.Code) {
		categories[c] = true
	}
	var missing, stale, problems []string
	for k := range used {
		e, ok := cat[k]
		if ok && !e.Empty() {
			continue
		}
		// The singular is not needed when the plural gives every form, or
		// when the language has no singular.
		if many, isOne := ones[k]; isOne && (cat[many].Forms != nil || !categories[i18n.One]) {
			continue
		}
		missing = append(missing, k)
	}
	for k, e := range cat {
		if !used[k] {
			stale = append(stale, k)
			continue
		}
		if e.Forms == nil {
			if e.Text != "" && verbs(k) != verbs(e.Text) {
				problems = append(problems, fmt.Sprintf("arguments differ:\n  en %q\n  %s %q", k, l.Code, e.Text))
			}
			continue
		}
		if !plurals[k] {
			problems = append(problems, fmt.Sprintf("%q is not counted with plural(): give one translation, not plural forms", k))
		}
		if e.Forms[i18n.Other] == "" && !e.Empty() {
			problems = append(problems, fmt.Sprintf("%q: plural forms need an %q form", k, i18n.Other))
		}
		for c, s := range e.Forms {
			if !categories[c] {
				problems = append(problems, fmt.Sprintf("%q: %s has no plural form %q (it uses %s)", k, l.Code, c, strings.Join(i18n.PluralCategories(l.Code), ", ")))
			}
			if s != "" && verbs(k) != verbs(s) {
				problems = append(problems, fmt.Sprintf("arguments differ:\n  en %q\n  %s %s %q", k, l.Code, c, s))
			}
		}
	}
	for _, list := range []string{weekdaysShortList, weekdaysLongList, monthsShortList, monthsLongList} {
		if e := cat[list]; e.Text != "" && strings.Count(e.Text, ",") != strings.Count(list, ",") {
			problems = append(problems, fmt.Sprintf("%q: give %d names separated by commas, not %q", list, strings.Count(list, ",")+1, e.Text))
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	sort.Strings(problems)
	for _, p := range problems {
		t.Error(p)
	}
	if update() && !l.Complete {
		if err := fillCatalog(filepath.Join("..", "..", filepath.FromSlash(path)), missing, stale); err != nil {
			t.Fatal(err)
		}
		return
	}
	report := t.Errorf
	if !l.Complete {
		report = t.Logf
		if len(missing) > 0 || len(stale) > 0 {
			t.Logf("%s is incomplete: %d of %d texts translated (go test -v ./internal/actions -run TestEveryMessageIsTranslated lists the rest)", l.Code, len(used)-len(missing), len(used))
		}
	}
	if cat == nil {
		report("%s has no translations: run go test ./internal/actions -update to create %s", l.Code, path)
		return
	}
	if len(missing) > 0 {
		report("no %s translation in %s for:\n  %s", l.Code, path, strings.Join(quoteAll(missing), "\n  "))
	}
	if len(stale) > 0 {
		report("%s translations in %s no longer used:\n  %s", l.Code, path, strings.Join(quoteAll(stale), "\n  "))
	}
}

func quoteAll(s []string) []string {
	out := make([]string, len(s))
	for i, v := range s {
		out[i] = strconv.Quote(v)
	}
	return out
}

func update() bool {
	f := flag.Lookup("update")
	return f != nil && f.Value.String() == "true"
}

// fillCatalog rewrites a catalog file in its own order, without the stale
// texts and with an empty entry for each missing one at the end.
func fillCatalog(path string, missing, stale []string) error {
	keys, entries, err := readOrdered(path)
	if err != nil {
		return err
	}
	drop := map[string]bool{}
	for _, k := range stale {
		drop[k] = true
	}
	var b bytes.Buffer
	b.WriteString("{")
	n := 0
	write := func(k string, v json.RawMessage) {
		if n > 0 {
			b.WriteString(",")
		}
		b.WriteString("\n\t" + jsonString(k) + ": " + string(v))
		n++
	}
	for _, k := range keys {
		if !drop[k] {
			write(k, entries[k])
		}
	}
	for _, k := range missing {
		if _, ok := entries[k]; !ok {
			write(k, json.RawMessage(`""`))
		}
	}
	b.WriteString("\n}\n")
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// readOrdered reads a JSON object's keys in order, with their raw values,
// each written on one line.
func readOrdered(path string) ([]string, map[string]json.RawMessage, error) {
	entries := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, entries, nil
	}
	if err != nil {
		return nil, nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", path, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		k, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		var compact bytes.Buffer
		if err := json.Compact(&compact, v); err != nil {
			return nil, nil, err
		}
		keys = append(keys, k)
		entries[k] = bytes.Clone(compact.Bytes())
	}
	return keys, entries, nil
}

func jsonString(s string) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}

// verbs lists the arguments of a text, as "1s,2d": the argument each verb
// takes and the verb, in argument order, so a translation may reorder them
// with indexed verbs (%[2]d … %[1]s).
func verbs(s string) string {
	var out []string
	n := 0
	for _, m := range verbRe.FindAllStringSubmatch(s, -1) {
		if m[3] == "%" {
			continue
		}
		i := n
		if m[2] != "" {
			i, _ = strconv.Atoi(m[2])
			i--
		}
		n = i + 1
		out = append(out, strconv.Itoa(i+1)+m[3])
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

var verbRe = regexp.MustCompile(`%(\[(\d+)\])?[-+# 0]*[0-9]*(?:\.[0-9]+)?([a-zA-Z%])`)

func TestVerbs(t *testing.T) {
	for _, c := range [][2]string{{"%s owes %d", "%[2]d debe %[1]s"}, {"100%% %s", "%s al 100%%"}, {"%-6s|%q", "%-6s|%q"}} {
		if verbs(c[0]) != verbs(c[1]) {
			t.Errorf("%q and %q should take the same arguments: %s, %s", c[0], c[1], verbs(c[0]), verbs(c[1]))
		}
	}
	if verbs("%s %d") == verbs("%d %s") {
		t.Error("swapped verbs without indexes must differ")
	}
}
