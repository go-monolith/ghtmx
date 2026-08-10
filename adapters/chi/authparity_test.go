package chi_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// authGlueSurface is the canonical exported-function set every auth
// glue package must carry — the per-framework face of the core auth
// package. It is hardcoded here rather than derived from the auth
// package because the core's net/http surface is a superset under
// different names (Middleware/Require/VerifyCSRF have no per-framework
// mirror; chi and nethttp use the core directly, so there is no chi
// glue in this list).
var authGlueSurface = map[string]bool{
	"New":                true,
	"CSRF":               true,
	"IdentityFrom":       true,
	"SetSessionCookie":   true,
	"ClearSessionCookie": true,
	"SetLoginCSRFCookie": true,
	"ValidLoginCSRF":     true,
}

// exportedFuncs parses one glue source file and returns its exported
// top-level function names. Generic type parameters do not affect
// FuncDecl name parsing.
func exportedFuncs(t *testing.T, path string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !fn.Name.IsExported() {
			continue
		}
		out[fn.Name.Name] = true
	}
	return out
}

// TestAuthGlueParityAcrossAdapters: the auth glue packages must all
// export the same seven-function surface, so an application can move
// between frameworks by swapping one import. The existing option parity
// gate cannot see these files (it parses each adapter's render file),
// so the auth surface gets its own gate, in the same AST style.
func TestAuthGlueParityAcrossAdapters(t *testing.T) {
	glue := map[string]string{
		"ginauth":     filepath.Join("..", "gin", "ginauth", "ginauth.go"),
		"echoauth":    filepath.Join("..", "echo", "echoauth", "echoauth.go"),
		"fiberauth":   filepath.Join("..", "fiber", "fiberauth", "fiberauth.go"),
		"fiberv3auth": filepath.Join("..", "fiberv3", "fiberv3auth", "fiberv3auth.go"),
	}
	for name, path := range glue {
		t.Run(name, func(t *testing.T) {
			got := exportedFuncs(t, path)
			for fn := range authGlueSurface {
				if !got[fn] {
					t.Errorf("the %s package is missing %s; every auth glue package carries the same surface", name, fn)
				}
			}
			for fn := range got {
				if !authGlueSurface[fn] {
					t.Errorf("the %s package exports %s, which is not part of the shared auth glue surface — add it everywhere (and here) or unexport it", name, fn)
				}
			}
		})
	}
}
