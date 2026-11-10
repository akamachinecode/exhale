package actions

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/akamachinecode/exhale/internal/app"
	"github.com/akamachinecode/exhale/internal/domain"
	"github.com/akamachinecode/exhale/internal/i18n"
)

// Texts written outside this package (ADR 0023): the errors of the service
// and the assistant's cards. They are domain.Text values; chat renders them
// with the catalogs of internal/i18n and the web with a copy of them
// written by TestServerTextsAreTranslated. This test finds them by type, not by name:
//
//   - a call to a function with a parameter named format that returns an
//     error or a domain.Text (domain.Invalid, domain.T, store.NotFound…)
//     must pass a constant, which is a text;
//   - a string constant of type domain.Word is a text;
//   - fmt.Errorf wrapping domain.ErrValidation, ErrNotFound, ErrConflict or
//     app.ErrForbidden is refused: it cannot be translated.
//
// The web's copy of their translations (web/src/locales/server) is
// regenerated with go test ./internal/actions -update.

// serverPackages are the packages whose texts reach people.
var serverPackages = []string{"app", "domain", "assistant", "actions", "store", "httpapi"}

// serverDynamicWords are words passed as domain.Word from a variable: charge
// statuses and the names of what a change is about.
var serverDynamicWords = []string{
	"paid", "pending", "failed", "skipped", "active", "paused", "cancelled",
	"subscription", "charge", "category", "payment method", "household", "member", "reminder",
	"daily", "weekly", "monthly", "yearly",
}

var (
	serverOnce  sync.Once
	serverFound map[string]bool
	serverErrs  []string
)

// serverTexts returns every text written outside the tr calls, keyed by
// its English format.
func serverTexts(t *testing.T) map[string]bool {
	t.Helper()
	serverOnce.Do(func() { serverFound, serverErrs = scanServerTexts() })
	for _, e := range serverErrs {
		t.Error(e)
	}
	return serverFound
}

func scanServerTexts() (map[string]bool, []string) {
	found := map[string]bool{}
	for _, w := range serverDynamicWords {
		found[w] = true
	}
	// The titles of the use cases name a change the assistant prepares.
	for _, b := range app.Catalog() {
		found[b.Title] = true
	}
	var problems []string
	root := filepath.Join("..", "..")
	var dirs []string
	for _, p := range serverPackages {
		dirs = append(dirs, "./internal/"+p)
	}
	// Export data of every dependency, so each package is type-checked from
	// source without compiling the world again.
	cmd := exec.Command("go", append([]string{"list", "-export", "-deps", "-f", "{{.ImportPath}}\t{{.Export}}"}, dirs...)...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		return nil, []string{"go list: " + err.Error()}
	}
	exports := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		path, file, _ := strings.Cut(line, "\t")
		exports[path] = file
	}
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		f, ok := exports[path]
		if !ok || f == "" {
			return nil, os.ErrNotExist
		}
		return os.Open(f)
	})
	for _, p := range serverPackages {
		dir := filepath.Join(root, "internal", p)
		entries, _ := os.ReadDir(dir)
		var files []*ast.File
		for _, e := range entries {
			n := e.Name()
			if !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(fset, filepath.Join(dir, n), nil, 0)
			if err != nil {
				return nil, []string{err.Error()}
			}
			files = append(files, f)
		}
		info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Uses: map[*ast.Ident]types.Object{}, Defs: map[*ast.Ident]types.Object{}}
		conf := types.Config{Importer: imp}
		if _, err := conf.Check("github.com/akamachinecode/exhale/internal/"+p, fset, files, info); err != nil {
			return nil, []string{"type-check " + p + ": " + err.Error()}
		}
		for _, f := range files {
			ast.Inspect(f, func(n ast.Node) bool {
				switch n := n.(type) {
				case ast.Expr:
					if tv, ok := info.Types[n]; ok && tv.Value != nil && tv.Value.Kind() == constant.String && isWord(tv.Type) && constant.StringVal(tv.Value) != "" {
						found[constant.StringVal(tv.Value)] = true
					}
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if isErrorf(call, info) {
					for _, a := range call.Args[1:] {
						if isSentinel(a, info) {
							problems = append(problems, fset.Position(call.Pos()).String()+": write this error with domain.Invalid, NotFound, Conflict, Missing or Fail so it can be translated")
						}
					}
					return true
				}
				sig, ok := info.Types[call.Fun].Type.(*types.Signature)
				if !ok {
					return true
				}
				idx := formatParam(sig)
				if idx < 0 || idx >= len(call.Args) {
					return true
				}
				arg := call.Args[idx]
				if tv := info.Types[arg]; tv.Value != nil && tv.Value.Kind() == constant.String {
					found[constant.StringVal(tv.Value)] = true
					return true
				}
				if id, ok := arg.(*ast.Ident); ok && id.Name == "format" {
					return true // a helper passing its own format on
				}
				problems = append(problems, fset.Position(arg.Pos()).String()+": the text must be a constant, so it can be translated")
				return true
			})
		}
	}
	return found, problems
}

func isWord(t types.Type) bool {
	n, ok := t.(*types.Named)
	return ok && n.Obj().Name() == "Word" && n.Obj().Pkg() != nil && strings.HasSuffix(n.Obj().Pkg().Path(), "/internal/domain")
}

// formatParam returns the index of a parameter named format in a function
// that returns an error or a domain.Text, or -1.
func formatParam(sig *types.Signature) int {
	if sig.Results().Len() != 1 {
		return -1
	}
	rt := sig.Results().At(0).Type()
	n, named := rt.(*types.Named)
	isText := named && n.Obj().Name() == "Text" && n.Obj().Pkg() != nil && strings.HasSuffix(n.Obj().Pkg().Path(), "/internal/domain")
	if !isText && rt.String() != "error" {
		return -1
	}
	for i := range sig.Params().Len() {
		p := sig.Params().At(i)
		if p.Name() == "format" && p.Type().String() == "string" {
			return i
		}
	}
	return -1
}

func isErrorf(call *ast.CallExpr, info *types.Info) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Errorf" {
		return false
	}
	obj := info.Uses[sel.Sel]
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == "fmt"
}

func isSentinel(e ast.Expr, info *types.Info) bool {
	var id *ast.Ident
	switch v := e.(type) {
	case *ast.Ident:
		id = v
	case *ast.SelectorExpr:
		id = v.Sel
	default:
		return false
	}
	obj := info.Uses[id]
	if obj == nil || obj.Pkg() == nil {
		return false
	}
	path := obj.Pkg().Path()
	switch obj.Name() {
	case "ErrValidation", "ErrNotFound", "ErrConflict":
		return strings.HasSuffix(path, "/internal/domain")
	case "ErrForbidden":
		return strings.HasSuffix(path, "/internal/app")
	}
	return false
}

// TestServerTextsAreTranslated writes the web's copy of the translated
// server texts, web/src/locales/server/<code>.json: the web renders them
// with {0} placeholders where Go has verbs (domain.WebKey). It fails when a
// copy is stale; go test ./internal/actions -update rewrites them. Whether
// each text is translated is checked with the rest of the catalog
// (TestEveryMessageIsTranslated).
func TestServerTextsAreTranslated(t *testing.T) {
	texts := serverTexts(t)
	dir := filepath.Join("..", "..", "web", "src", "locales", "server")
	for _, l := range i18n.Languages() {
		if l.Code == i18n.Source {
			continue
		}
		cat := i18n.Catalog(l.Code)
		web := map[string]string{}
		for k := range texts {
			if s := i18n.Translate(l.Code, k); s != k || cat[k].Text == k {
				web[domain.WebKey(k)] = domain.WebKey(s)
			}
		}
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "\t")
		if err := enc.Encode(web); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, l.Code+".json")
		if update() {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, b.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		cur, _ := os.ReadFile(path)
		if !bytes.Equal(cur, b.Bytes()) {
			t.Errorf("web/src/locales/server/%s.json is stale: run go test ./internal/actions -update", l.Code)
		}
	}
}
