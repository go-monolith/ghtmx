package proxy

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/generator/central"
	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
	"github.com/go-monolith/ghtmx/internal/routes"
)

func navServer(t *testing.T) *Server {
	t.Helper()
	s := completionServer(t)
	s.TemplSource = newDocumentContents(s.Log)
	return s
}

func setLine(s *Server, uri, line string) {
	s.TemplSource.Set(uri, &Document{Log: s.Log, Lines: []string{line}})
}

// TestEventDefinitionOpensDeclaration: FR-084 — go-to-definition on an
// event reference opens its .ghtmx declaration.
func TestEventDefinitionOpensDeclaration(t *testing.T) {
	s := navServer(t)
	const doc = "file:///page.ghtmx"
	setLine(s, doc, `	<div hx-on:item-saved="go()"></div>`)

	// Cursor on the wire name inside hx-on:item-saved.
	locations, handled := s.ghtmxDefinition(doc, lsp.Position{Line: 0, Character: 14})
	if !handled || len(locations) != 1 {
		t.Fatalf("expected one event declaration location, got handled=%v %v", handled, locations)
	}
	loc := locations[0]
	if !strings.HasSuffix(string(loc.URI), "events.ghtmx") {
		t.Errorf("must open the declaring .ghtmx file, got %s", loc.URI)
	}
	if loc.Range.Start.Line != 2 {
		t.Errorf("declaration is on 0-based line 2, got %+v", loc.Range.Start)
	}
}

// TestEventHoverShowsPayload: FR-083 — hovering an event reference shows
// the declared payload type.
func TestEventHoverShowsPayload(t *testing.T) {
	s := navServer(t)
	const doc = "file:///page.ghtmx"
	setLine(s, doc, `	<div hx-trigger="item-saved from:body"></div>`)

	hover, handled := s.ghtmxHover(doc, lsp.Position{Line: 0, Character: 20})
	if !handled || hover == nil {
		t.Fatalf("expected an event hover, got handled=%v", handled)
	}
	if !strings.Contains(hover.Contents.Value, "event ItemSaved(id string)") {
		t.Errorf("hover must show the declared payload, got %q", hover.Contents.Value)
	}

	// Payload-less events say so.
	setLine(s, doc, `	<div hx-on:cart-cleared="x()"></div>`)
	hover, handled = s.ghtmxHover(doc, lsp.Position{Line: 0, Character: 14})
	if !handled || hover == nil || !strings.Contains(hover.Contents.Value, "no payload") {
		t.Errorf("payload-less hover must say so, got %+v", hover)
	}
}

// TestBoundHandlerDefinitionOpensGo: FR-084 — go-to-definition on a bound
// handler opens its Go registration site.
func TestBoundHandlerDefinitionOpensGo(t *testing.T) {
	s := navServer(t)
	table, _, _ := s.routeState()
	table.Add(routes.Route{
		Verb: routes.POST, Path: "/things",
		Handler: routes.SymbolRef{PkgPath: "example.com/app/handlers", Name: "MakeThing"},
		Pos:     routes.Position{File: "/app/main.go", Line: 12, Col: 2},
	})
	const doc = "file:///page.ghtmx"
	setLine(s, doc, `	<button hx-post={ handlers.MakeThing }>Go</button>`)

	locations, handled := s.ghtmxDefinition(doc, lsp.Position{Line: 0, Character: 25})
	if !handled || len(locations) != 1 {
		t.Fatalf("expected the Go registration site, got handled=%v %v", handled, locations)
	}
	loc := locations[0]
	if !strings.HasSuffix(string(loc.URI), "main.go") || loc.Range.Start.Line != 11 {
		t.Errorf("expected main.go line 11 (0-based), got %s %+v", loc.URI, loc.Range.Start)
	}
}

// TestRouteHoverShowsVerbAndPath: FR-083 — hovering a bound symbol shows
// the route's verb, path, and handler.
func TestRouteHoverShowsVerbAndPath(t *testing.T) {
	s := navServer(t)
	const doc = "file:///page.ghtmx"
	setLine(s, doc, `	<button hx-post={ ghtmxgen.SaveItem(id) }>Go</button>`)

	hover, handled := s.ghtmxHover(doc, lsp.Position{Line: 0, Character: 30})
	if !handled || hover == nil {
		t.Fatalf("expected a route hover, got handled=%v", handled)
	}
	if !strings.Contains(hover.Contents.Value, "POST /items/{id}") {
		t.Errorf("hover must show verb and path, got %q", hover.Contents.Value)
	}
}

// TestEventRefAtNamespaceContract: navigation resolves exactly the
// references the analyzer's contract accepts — DOM events, htmx-namespace
// listeners, qualified names, trigger modifiers, and filter contents all
// fall through — with inclusive token boundaries.
func TestEventRefAtNamespaceContract(t *testing.T) {
	tests := []struct {
		name      string
		line      string
		character int
		wantWire  string
		wantOK    bool
	}{
		{"hx-on declared suffix", `<div hx-on:item-saved="f()">`, 14, "item-saved", true},
		{"suffix start boundary", `<div hx-on:item-saved="f()">`, 11, "item-saved", true},
		{"suffix end boundary", `<div hx-on:item-saved="f()">`, 21, "item-saved", true},
		{"hx-on prefix falls through", `<div hx-on:item-saved="f()">`, 8, "", false},
		{"past the attribute falls through", `<div hx-on:item-saved="f()">`, 23, "", false},
		{"hx-on:: htmx shorthand", `<div hx-on::after-request="f()">`, 15, "", false},
		{"DOM event", `<div hx-on:click="f()">`, 13, "", false},
		{"qualified htmx namespace", `<div hx-on:htmx:after-request="f()">`, 20, "", false},
		{"htmx- shorthand", `<div hx-on:htmx-after-request="f()">`, 20, "", false},
		{"dot-qualified custom event", `<div hx-on:my-event.custom="f()">`, 13, "", false},
		{"dashed hx-on- form", `<div hx-on-item-saved="f()">`, 14, "item-saved", true},
		{"hx-on-- htmx shorthand", `<div hx-on--item-saved="f()">`, 14, "", false},
		{"trigger event token", `<div hx-trigger="item-saved from:#save-btn">`, 20, "item-saved", true},
		{"trigger modifier selector", `<div hx-trigger="item-saved from:#save-btn">`, 36, "", false},
		{"trigger second spec", `<div hx-trigger="click delay:500ms, item-saved">`, 38, "item-saved", true},
		{"trigger bracket filter", `<div hx-trigger="item-saved[window.flag-x]">`, 36, "", false},
		{"trigger polling form", `<div hx-trigger="every 2s">`, 19, "", false},
		{"trigger DOM event", `<div hx-trigger="click">`, 19, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wire, ok := eventRefAt(tt.line, tt.character)
			if wire != tt.wantWire || ok != tt.wantOK {
				t.Errorf("eventRefAt(%q, %d) = %q, %v; want %q, %v",
					tt.line, tt.character, wire, ok, tt.wantWire, tt.wantOK)
			}
		})
	}
}

// TestUndeclaredEventReportsNothing: a compiler-owned wire name with no
// declaration is handled with no locations — silence beats a wrong gopls
// guess.
func TestUndeclaredEventReportsNothing(t *testing.T) {
	s := navServer(t)
	const doc = "file:///page.ghtmx"
	setLine(s, doc, `	<div hx-on:not-declared="f()"></div>`)

	locations, handled := s.ghtmxDefinition(doc, lsp.Position{Line: 0, Character: 15})
	if !handled || locations != nil {
		t.Errorf("undeclared owned wire must be handled with no locations, got handled=%v %v", handled, locations)
	}
	hover, handled := s.ghtmxHover(doc, lsp.Position{Line: 0, Character: 15})
	if !handled || hover != nil {
		t.Errorf("undeclared owned wire hover must be handled and empty, got handled=%v %v", handled, hover)
	}
}

// TestHandlerQualifierDisambiguates: two packages register a same-named
// handler; the binding's package qualifier picks the right one even when
// the other sorts first in the table.
func TestHandlerQualifierDisambiguates(t *testing.T) {
	s := navServer(t)
	table, _, _ := s.routeState()
	// "/admin/things" sorts before "/things": a name-only first match
	// would resolve to the admin package.
	table.Add(routes.Route{
		Verb: routes.POST, Path: "/admin/things",
		Handler: routes.SymbolRef{PkgPath: "example.com/app/admin", Name: "MakeThing"},
		Pos:     routes.Position{File: "/app/admin/routes.go", Line: 30, Col: 2},
	})
	table.Add(routes.Route{
		Verb: routes.POST, Path: "/things",
		Handler: routes.SymbolRef{PkgPath: "example.com/app/handlers", Name: "MakeThing"},
		Pos:     routes.Position{File: "/app/main.go", Line: 12, Col: 2},
	})
	const doc = "file:///page.ghtmx"
	setLine(s, doc, `	<button hx-post={ handlers.MakeThing }>Go</button>`)

	locations, handled := s.ghtmxDefinition(doc, lsp.Position{Line: 0, Character: 25})
	if !handled || len(locations) != 1 {
		t.Fatalf("expected one location, got handled=%v %v", handled, locations)
	}
	if !strings.HasSuffix(string(locations[0].URI), "/app/main.go") {
		t.Errorf("qualifier handlers must pick the handlers package, got %s", locations[0].URI)
	}
}

// TestVerbPicksTheRightRegistration: one handler registered under two
// verbs; the attribute's verb selects the matching route for both hover
// and definition.
func TestVerbPicksTheRightRegistration(t *testing.T) {
	s := navServer(t)
	table, _, _ := s.routeState()
	// "/items/all" sorts before "/items/{id}": a verb-blind first match
	// would resolve hx-delete to the GET route.
	table.Add(routes.Route{
		Verb: routes.GET, Path: "/items/all",
		Handler: routes.SymbolRef{PkgPath: "example.com/app/handlers", Name: "Item"},
		Pos:     routes.Position{File: "/app/get.go", Line: 5, Col: 1},
	})
	table.Add(routes.Route{
		Verb: routes.DELETE, Path: "/items/{id}",
		Handler: routes.SymbolRef{PkgPath: "example.com/app/handlers", Name: "Item"},
		Pos:     routes.Position{File: "/app/delete.go", Line: 9, Col: 1},
	})
	const doc = "file:///page.ghtmx"
	setLine(s, doc, `	<button hx-delete={ handlers.Item }>X</button>`)

	hover, handled := s.ghtmxHover(doc, lsp.Position{Line: 0, Character: 25})
	if !handled || hover == nil || !strings.Contains(hover.Contents.Value, "DELETE /items/{id}") {
		t.Fatalf("hx-delete must hover the DELETE registration, got %+v", hover)
	}
	locations, _ := s.ghtmxDefinition(doc, lsp.Position{Line: 0, Character: 25})
	if len(locations) != 1 || !strings.HasSuffix(string(locations[0].URI), "delete.go") {
		t.Errorf("hx-delete must jump to the DELETE registration, got %v", locations)
	}
}

// TestConstructorDefinitionOpensGo: FR-084 — go-to-definition on a
// generated-constructor call opens the route's Go registration site.
func TestConstructorDefinitionOpensGo(t *testing.T) {
	s := navServer(t)
	table, _, _ := s.routeState()
	table.Add(routes.Route{
		Verb: routes.DELETE, Path: "/widgets/{id}",
		Handler: routes.SymbolRef{PkgPath: "example.com/app/handlers", Name: "DropWidget"},
		Params:  []routes.RouteParam{{Name: "id"}},
		Pos:     routes.Position{File: "/app/widgets.go", Line: 40, Col: 3},
	})
	byName, _ := central.Naming(table)
	s.constructors = byName
	const doc = "file:///page.ghtmx"
	setLine(s, doc, `	<button hx-delete={ ghtmxgen.DropWidget(id) }>X</button>`)

	locations, handled := s.ghtmxDefinition(doc, lsp.Position{Line: 0, Character: 25})
	if !handled || len(locations) != 1 {
		t.Fatalf("expected the registration site, got handled=%v %v", handled, locations)
	}
	loc := locations[0]
	if !strings.HasSuffix(string(loc.URI), "widgets.go") || loc.Range.Start.Line != 39 || loc.Range.Start.Character != 2 {
		t.Errorf("expected widgets.go 39:2 (0-based), got %s %+v", loc.URI, loc.Range.Start)
	}
}

// TestHoverWithoutPositionOmitsRegisteredAt: routes with no recorded
// position must not render a zero position.
func TestHoverWithoutPositionOmitsRegisteredAt(t *testing.T) {
	s := navServer(t)
	const doc = "file:///page.ghtmx"
	// The fixture's ListItems route carries no Pos.
	setLine(s, doc, `	<a hx-get={ handlers.ListItems }>All</a>`)

	hover, handled := s.ghtmxHover(doc, lsp.Position{Line: 0, Character: 20})
	if !handled || hover == nil || !strings.Contains(hover.Contents.Value, "GET /items") {
		t.Fatalf("expected a route hover, got handled=%v %+v", handled, hover)
	}
	if strings.Contains(hover.Contents.Value, "registered at") {
		t.Errorf("a position-less route must omit the registration clause, got %q", hover.Contents.Value)
	}
}

// TestLspLocationForms: registry positions arrive as plain paths (config
// seeding) or file:// URIs (live didOpen); both convert, and unset
// positions guard to zero.
func TestLspLocationForms(t *testing.T) {
	tests := []struct {
		name      string
		file      string
		line, col uint32
		wantURI   string
		want      lsp.Position
	}{
		{"plain path", "/x/events.ghtmx", 3, 1, "file:///x/events.ghtmx", lsp.Position{Line: 2, Character: 0}},
		{"uri form preserved", "file:///x/events.ghtmx", 3, 1, "file:///x/events.ghtmx", lsp.Position{Line: 2, Character: 0}},
		{"unset guards to zero", "/x/events.ghtmx", 0, 0, "file:///x/events.ghtmx", lsp.Position{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc := lspLocation(tt.file, tt.line, tt.col)
			if string(loc.URI) != tt.wantURI || loc.Range.Start != tt.want {
				t.Errorf("lspLocation(%q, %d, %d) = %s %+v; want %s %+v",
					tt.file, tt.line, tt.col, loc.URI, loc.Range.Start, tt.wantURI, tt.want)
			}
		})
	}
}

// TestNonGhtmxPositionsFallThrough: plain Go expressions stay with gopls.
func TestNonGhtmxPositionsFallThrough(t *testing.T) {
	s := navServer(t)
	const doc = "file:///page.ghtmx"
	setLine(s, doc, `	<p>{ user.Name }</p>`)

	if _, handled := s.ghtmxDefinition(doc, lsp.Position{Line: 0, Character: 8}); handled {
		t.Error("plain Go expressions must fall through to gopls")
	}
	if _, handled := s.ghtmxHover(doc, lsp.Position{Line: 0, Character: 8}); handled {
		t.Error("plain Go hover must fall through to gopls")
	}
}
