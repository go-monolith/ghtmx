package build

import (
	"reflect"
	"testing"

	"github.com/go-monolith/ghtmx/internal/analyzer"
	"github.com/go-monolith/ghtmx/internal/routes"
)

func graphFixture() *Graph {
	// shared/row.ghtmx declares app/shared.Row; two pages reference it,
	// one of them through an intermediate fragment. list.ghtmx also binds
	// a route; events.ghtmx declares an event that page-a listens for.
	return NewGraph(map[string]analyzer.DependencyFacts{
		"shared/row.ghtmx": {Decls: []string{"app/shared.Row"}},
		"shared/card.ghtmx": {
			Decls: []string{"app/shared.Card"},
			Refs:  []string{"app/shared.Row"},
		},
		"pages/list.ghtmx": {
			Decls:       []string{"app/pages.list"},
			Refs:        []string{"app/shared.Row"},
			BindsRoutes: true,
		},
		"pages/detail.ghtmx": {
			Decls: []string{"app/pages.detail"},
			Refs:  []string{"app/shared.Card"},
		},
		"pages/about.ghtmx": {Decls: []string{"app/pages.about"}},
		"events.ghtmx":      {EventDecls: []string{"item-saved"}},
		"pages/board.ghtmx": {
			Decls:     []string{"app/pages.board"},
			EventRefs: []string{"item-saved"},
		},
	})
}

func TestFragmentEditInvalidatesReferencingPages(t *testing.T) {
	got := graphFixture().OnTemplateChange("shared/row.ghtmx")
	want := []string{
		"pages/detail.ghtmx", // via the intermediate Card fragment
		"pages/list.ghtmx",
		"shared/card.ghtmx",
		"shared/row.ghtmx",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OnTemplateChange = %v, want %v", got, want)
	}
}

func TestUnrelatedTemplateInvalidatesOnlyItself(t *testing.T) {
	got := graphFixture().OnTemplateChange("pages/about.ghtmx")
	if !reflect.DeepEqual(got, []string{"pages/about.ghtmx"}) {
		t.Errorf("an unrelated template invalidates only itself, got %v", got)
	}
}

func TestEventDeclarationEditInvalidatesListeners(t *testing.T) {
	got := graphFixture().OnTemplateChange("events.ghtmx")
	want := []string{"events.ghtmx", "pages/board.ghtmx"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("event listeners must re-diagnose, got %v", got)
	}
}

func TestBoundFilesAreTierOneTargets(t *testing.T) {
	got := graphFixture().BoundFiles()
	if !reflect.DeepEqual(got, []string{"pages/list.ghtmx"}) {
		t.Errorf("BoundFiles = %v", got)
	}
}

func TestCyclicReferencesTerminate(t *testing.T) {
	g := NewGraph(map[string]analyzer.DependencyFacts{
		"a.ghtmx": {Decls: []string{"app.a"}, Refs: []string{"app.b"}},
		"b.ghtmx": {Decls: []string{"app.b"}, Refs: []string{"app.a"}},
	})
	got := g.OnTemplateChange("a.ghtmx")
	if !reflect.DeepEqual(got, []string{"a.ghtmx", "b.ghtmx"}) {
		t.Errorf("cycles must terminate with both units, got %v", got)
	}
}

func TestRoutesChanged(t *testing.T) {
	route := func(verb routes.Verb, path, handler string, params ...string) routes.Route {
		r := routes.Route{Verb: verb, Path: path, Handler: routes.SymbolRef{PkgPath: "app", Name: handler}}
		for _, p := range params {
			r.Params = append(r.Params, routes.RouteParam{Name: p})
		}
		return r
	}
	table := func(rs ...routes.Route) *routes.Table {
		t := routes.NewTable()
		for _, r := range rs {
			t.Add(r)
		}
		return t
	}

	base := table(route(routes.GET, "/items", "list"), route(routes.POST, "/items", "create"))
	if RoutesChanged(base, table(route(routes.POST, "/items", "create"), route(routes.GET, "/items", "list"))) {
		t.Error("order must not matter")
	}
	if !RoutesChanged(base, table(route(routes.GET, "/items", "list"))) {
		t.Error("a removed route is a change")
	}
	if !RoutesChanged(base, table(route(routes.GET, "/items", "list"), route(routes.POST, "/items", "createV2"))) {
		t.Error("a renamed handler is a change")
	}
	if !RoutesChanged(base, table(route(routes.GET, "/items/{id}", "list", "id"), route(routes.POST, "/items", "create"))) {
		t.Error("a re-pathed route is a change")
	}
	if RoutesChanged(nil, nil) {
		t.Error("two nil tables are equal")
	}
	// Watch mode swaps the rediscovered table in only on a fingerprint
	// change, and the whole-set diagnostics read that table — so toggling
	// the nav marker must register even though codegen ignores it.
	withNav := routes.Route{Verb: routes.GET, Path: "/items", Handler: routes.SymbolRef{PkgPath: "app", Name: "list"}, NavOnly: true}
	if !RoutesChanged(base, table(withNav, route(routes.POST, "/items", "create"))) {
		t.Error("a nav-marker toggle is a change")
	}
}

func TestListenerEditInvalidatesDeclaringFile(t *testing.T) {
	got := graphFixture().OnTemplateChange("pages/board.ghtmx")
	want := []string{"events.ghtmx", "pages/board.ghtmx"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("removing the last listener flips W0102 in the declaring file, got %v", got)
	}
}

func TestDuplicateDeclFirstFileWins(t *testing.T) {
	g := NewGraph(map[string]analyzer.DependencyFacts{
		"a/dup.ghtmx": {Decls: []string{"app.Row"}},
		"b/dup.ghtmx": {Decls: []string{"app.Row"}},
		"page.ghtmx":  {Refs: []string{"app.Row"}},
	})
	if got := g.OnTemplateChange("a/dup.ghtmx"); !reflect.DeepEqual(got, []string{"a/dup.ghtmx", "page.ghtmx"}) {
		t.Errorf("the first declaring file in sorted order owns the edge, got %v", got)
	}
	if got := g.OnTemplateChange("b/dup.ghtmx"); !reflect.DeepEqual(got, []string{"b/dup.ghtmx"}) {
		t.Errorf("the losing duplicate has no dependents, got %v", got)
	}
}
