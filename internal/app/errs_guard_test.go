package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"cc-manager/internal/errs"
)

// TestNoAdhocErrorLiterals walks every non-test file in this package and
// requires that error codes passed to the reporting helpers are registered.
// New codes must be added to internal/errs first; raw invented strings are
// how the五套 code systems drifted apart before.
func TestNoAdhocErrorLiterals(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool { return !strings.HasSuffix(fi.Name(), "_test.go") }, 0)
	if err != nil {
		t.Fatal(err)
	}
	helpers := map[string]int{"fail": 2, "apiFail": 1, "serverError": 2, "billBad": 0, "finishJob": 2, "add": 0}
	for _, pkg := range pkgs {
		for name, f := range pkg.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				fn, ok := call.Fun.(*ast.Ident)
				if !ok {
					return true
				}
				idx, watched := helpers[fn.Name]
				if !watched || len(call.Args) <= idx {
					return true
				}
				lit, ok := call.Args[idx].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				code := strings.Trim(lit.Value, `"`)
				if !strings.ContainsAny(code, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") || strings.ContainsAny(code, "abcdefghijklmnopqrstuvwxyz /") {
					return true
				}
				if _, registered := errs.Lookup(code); !registered {
					t.Errorf("%s:%d: %s(%q) uses an unregistered code", name, fset.Position(call.Pos()).Line, fn.Name, code)
				}
				return true
			})
		}
	}
}
