package routes

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

func TestFragmentEntryRefs(t *testing.T) {
	src := `package app

import "example.com/app/views"

func h(t any) {
	todoEditRowFragment(t)        // direct call: counts
	views.ItemRowFragment(1, "x") // qualified call: counts
	f := statsPanelFragment       // bare reference, not a call: ignored
	_ = f
	Fragment()   // bare "Fragment": no base name
	loadPage()   // unrelated call: ignored
}

func rowDetailFragment(v any) {} // declaration alone, never called: ignored
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "handlers.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	refs := FragmentEntryRefs([]*Package{{
		PkgPath: "example.com/app",
		Name:    "app",
		Fset:    fset,
		Files:   []*ast.File{file},
	}})
	for _, want := range []string{"todoEditRow", "ItemRow"} {
		if !refs[want] {
			t.Errorf("expected %q in refs, got %v", want, refs)
		}
	}
	for _, banned := range []string{"statsPanel", "rowDetail", "", "loadPage"} {
		if refs[banned] {
			t.Errorf("unexpected %q in refs: %v", banned, refs)
		}
	}
}
