package chi_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"testing"
)

// optionConstructors parses one adapter source file and returns its
// exported functions whose single result is Option.
func optionConstructors(t *testing.T, path string) map[string]bool {
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
		if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
			continue
		}
		if ident, ok := fn.Type.Results.List[0].Type.(*ast.Ident); ok && ident.Name == "Option" {
			out[fn.Name.Name] = true
		}
	}
	return out
}

// TestOptionParityAcrossAdapters: the framework adapters must re-export
// every nethttp option constructor — an option added there without a
// mirror here would silently narrow the framework adapters' surface.
// The wrapper funcs pin signatures at compile time; this pins the set.
func TestOptionParityAcrossAdapters(t *testing.T) {
	want := optionConstructors(t, filepath.Join("..", "nethttp", "nethttp.go"))
	if len(want) == 0 {
		t.Fatal("no option constructors found in the nethttp adapter — the parser lost them")
	}
	adapters := map[string]string{
		"chi":     "chi.go",
		"echo":    filepath.Join("..", "echo", "echo.go"),
		"gin":     filepath.Join("..", "gin", "gin.go"),
		"fiber":   filepath.Join("..", "fiber", "fiber.go"),
		"fiberv3": filepath.Join("..", "fiberv3", "fiberv3.go"),
		"beego":   filepath.Join("..", "beego", "beego.go"),
		"iris":    filepath.Join("..", "iris", "iris.go"),
		"revel":   filepath.Join("..", "revel", "revel.go"),
		"martini": filepath.Join("..", "martini", "martini.go"),
	}
	for name, path := range adapters {
		t.Run(name, func(t *testing.T) {
			got := optionConstructors(t, path)
			for constructor := range want {
				if !got[constructor] {
					t.Errorf("the %s adapter is missing the %s option; mirror it from nethttp", name, constructor)
				}
			}
			for constructor := range got {
				if !want[constructor] {
					t.Errorf("the %s adapter exports %s, which nethttp does not define", name, constructor)
				}
			}
		})
	}
}
