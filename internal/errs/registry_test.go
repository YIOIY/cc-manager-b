package errs

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var codeLiteral = regexp.MustCompile(`^[A-Z][A-Z0-9_]+$`)

// scanCodes collects every error-code string literal passed to the reporting
// helpers in a package directory, plus `issues = append(issues, "X")`.
func scanCodes(t *testing.T, dir string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	helpers := map[string]int{"fail": 2, "apiFail": 1, "serverError": 2, "billBad": 0, "dbError": 3, "dbStepConflict": 1, "finishJob": 2, "finishJobWithMessage": 2, "add": 0, "New": 0, "Adhoc": 0}
	used := map[string]bool{}
	lit := func(e ast.Expr) (string, bool) {
		b, ok := e.(*ast.BasicLit)
		if !ok || b.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(b.Value)
		return s, err == nil && codeLiteral.MatchString(s)
	}
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				if cl, ok := n.(*ast.CompositeLit); ok {
					if id, ok := cl.Type.(*ast.Ident); ok && id.Name == "billAPIError" && len(cl.Elts) == 3 {
						if s, ok := lit(cl.Elts[1]); ok {
							used[s] = true
						}
					}
					return true
				}
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := ""
				switch fn := call.Fun.(type) {
				case *ast.Ident:
					name = fn.Name
				case *ast.SelectorExpr:
					name = fn.Sel.Name
				}
				if name == "append" && len(call.Args) == 2 {
					if id, ok := call.Args[0].(*ast.Ident); ok && id.Name == "issues" {
						if s, ok := lit(call.Args[1]); ok {
							used[s] = true
						}
					}
					return true
				}
				idx, ok := helpers[name]
				if !ok || len(call.Args) <= idx {
					return true
				}
				if s, ok := lit(call.Args[idx]); ok {
					used[s] = true
				}
				return true
			})
		}
	}
	return used
}

func TestRegistryCoversEveryCodeUsedInApp(t *testing.T) {
	used := scanCodes(t, filepath.Join("..", "app"))
	if len(used) < 60 {
		t.Fatalf("scanner found only %d codes; scanner broken?", len(used))
	}
	missing := []string{}
	for code := range used {
		if _, ok := Lookup(code); !ok {
			missing = append(missing, code)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("%d codes used in internal/app but not registered:\n%s", len(missing), strings.Join(missing, "\n"))
	}
}

func TestEveryDefHasDoc(t *testing.T) {
	for _, d := range All() {
		if strings.TrimSpace(d.Doc) == "" {
			t.Errorf("%s has no Doc", d.Code)
		}
	}
}
