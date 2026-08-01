package routes

import (
	"go/ast"
	"strings"
)

// FragmentEntryRefs scans syntactically loaded packages for calls to
// generated standalone fragment entry points — `<name>Fragment(...)`,
// called directly or through a package qualifier — and returns the set
// of base fragment names referenced from hand-written Go source. The
// whole-set analysis treats those fragments as rendered (FR-034's
// handler-explicit path), so GHTMX-W0101 does not fire for them.
//
// The scan is syntax-only and name-based, like route discovery: a call
// to any symbol ending in "Fragment" marks the base name, package
// origin ignored. That can over-suppress a warning when an unrelated
// helper happens to share a fragment entry point's name — acceptable
// for a warning-class check, where a false silence is cheaper than a
// false alarm on every handler-rendered fragment.
func FragmentEntryRefs(pkgs []*Package) map[string]bool {
	refs := map[string]bool{}
	for _, p := range pkgs {
		for _, f := range p.Files {
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				var name string
				switch fn := call.Fun.(type) {
				case *ast.Ident:
					name = fn.Name
				case *ast.SelectorExpr:
					name = fn.Sel.Name
				}
				if base, found := strings.CutSuffix(name, "Fragment"); found && base != "" {
					refs[base] = true
				}
				return true
			})
		}
	}
	return refs
}
