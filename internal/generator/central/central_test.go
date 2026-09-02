package central

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/routes"
)

func table(t *testing.T, rs ...routes.Route) *routes.Table {
	t.Helper()
	table := routes.NewTable()
	for _, r := range rs {
		if _, ok := table.Add(r); !ok {
			t.Fatalf("failed to add %+v", r)
		}
	}
	return table
}

func handler(pkg, name string) routes.SymbolRef {
	return routes.SymbolRef{PkgPath: pkg, Name: name}
}

func generate(t *testing.T, rs ...routes.Route) string {
	t.Helper()
	out, err := Generate(table(t, rs...), Options{PackageName: "ghtmxgen"})
	if err != nil {
		t.Fatal(err)
	}
	// Emitted output must parse: NFR-005's generate-time self-check.
	if _, err := parser.ParseFile(token.NewFileSet(), "routes_ghtmx.go", out, 0); err != nil {
		t.Fatalf("generated output does not parse: %v\n%s", err, out)
	}
	return string(out)
}

// TestPositionShiftDoesNotChangeOutput: doc comments carry the
// registration file but never its line/column, so a cosmetic edit
// above a registration site (inserting blank lines) regenerates
// byte-identical output instead of failing the ensure-generated gate
// on an unrelated change.
func TestPositionShiftDoesNotChangeOutput(t *testing.T) {
	at := func(line uint32) []routes.Route {
		return []routes.Route{
			{Verb: routes.POST, Path: "/users", Handler: handler("example.com/app/handlers", "CreateUser"),
				Pos: routes.Position{File: "app/routes.go", Line: line, Col: 2}},
			{Verb: routes.GET, Path: "/users/{id}", Handler: handler("example.com/app/handlers", "GetUser"),
				Params: []routes.RouteParam{{Name: "id"}},
				Pos:    routes.Position{File: "app/routes.go", Line: line + 1, Col: 2}},
		}
	}
	before := generate(t, at(10)...)
	after := generate(t, at(27)...) // 17 blank lines inserted above
	if before != after {
		t.Errorf("a pure line shift changed the generated output:\n--- before\n%s\n--- after\n%s", before, after)
	}
	if !strings.Contains(before, "(app/routes.go)") {
		t.Errorf("doc comments must still name the registration file, got:\n%s", before)
	}
	if strings.Contains(before, "app/routes.go:") {
		t.Errorf("doc comments must not embed line/column positions, got:\n%s", before)
	}
}

func TestPathConstantForNonParameterisedRoute(t *testing.T) {
	got := generate(t,
		routes.Route{Verb: routes.POST, Path: "/users", Handler: handler("example.com/app/handlers", "CreateUser")},
	)
	if !strings.Contains(got, `const CreateUserPath = "/users"`) {
		t.Errorf("expected a path constant, got:\n%s", got)
	}
	if strings.Contains(got, "import") {
		t.Errorf("constant-only output must not import the runtime, got:\n%s", got)
	}
}

// TestMethodHandlerConstructorName pins how a method handler's dotted
// symbol (FR-1) reaches generated code: the dot is not a legal Go
// identifier character, so the existing sanitizer folds it away and the
// constructor is named for the receiver and method together.
func TestMethodHandlerConstructorName(t *testing.T) {
	if got := ConstructorBaseName("Handlers.ListUsers"); got != "HandlersListUsers" {
		t.Errorf("ConstructorBaseName = %q, want HandlersListUsers", got)
	}
	// An already-plain name is unchanged, so existing tables generate
	// byte-identical output.
	if got := ConstructorBaseName("ListUsers"); got != "ListUsers" {
		t.Errorf("ConstructorBaseName = %q, want ListUsers", got)
	}

	got := generate(t,
		routes.Route{
			Verb: routes.GET, Path: "/users/{id}", Handler: handler("example.com/app", "Handlers.GetUser"),
			Params: []routes.RouteParam{{Name: "id"}},
		},
		routes.Route{Verb: routes.GET, Path: "/users", Handler: handler("example.com/app", "Handlers.ListUsers")},
	)
	if !strings.Contains(got, "func HandlersGetUser(id string) ghtmx.SafeURL {") {
		t.Errorf("expected a constructor named for receiver and method, got:\n%s", got)
	}
	if !strings.Contains(got, `const HandlersListUsersPath = "/users"`) {
		t.Errorf("expected a path constant named for receiver and method, got:\n%s", got)
	}
	// The doc comment names the real symbol, so the generated file still
	// points back at the handler.
	if !strings.Contains(got, "example.com/app.Handlers.GetUser") {
		t.Errorf("generated comments must name the dotted symbol, got:\n%s", got)
	}
}

// TestMethodHandlerCollidesWithPlainHandler: two different symbols that
// sanitize to one constructor name go through the existing E0404
// machinery rather than silently generating one of them.
func TestMethodHandlerCollidesWithPlainHandler(t *testing.T) {
	_, conflicts := Naming(table(t,
		routes.Route{Verb: routes.GET, Path: "/a", Handler: handler("example.com/app", "Handlers.List")},
		routes.Route{Verb: routes.GET, Path: "/b", Handler: handler("example.com/app", "HandlersList")},
	))
	if len(conflicts) != 1 {
		t.Fatalf("expected one collision group, got %+v", conflicts)
	}
}

func TestTypedConstructorForParameterisedRoute(t *testing.T) {
	got := generate(t,
		routes.Route{
			Verb: routes.GET, Path: "/users/{id}", Handler: handler("example.com/app/handlers", "GetUser"),
			Params: []routes.RouteParam{{Name: "id"}},
		},
	)
	if !strings.Contains(got, "func GetUser(id string) ghtmx.SafeURL {") {
		t.Errorf("expected a typed constructor, got:\n%s", got)
	}
	if !strings.Contains(got, `ghtmx.SafeURL("/users/" + ghtmx.EscapePathSegment(id))`) {
		t.Errorf("expected URL-escaped substitution, got:\n%s", got)
	}
}

func TestMultiParamConstructorOrder(t *testing.T) {
	got := generate(t,
		routes.Route{
			Verb: routes.GET, Path: "/users/{id}/posts/{post}", Handler: handler("example.com/app/handlers", "GetPost"),
			Params: []routes.RouteParam{{Name: "id"}, {Name: "post"}},
		},
	)
	if !strings.Contains(got, "func GetPost(id string, post string) ghtmx.SafeURL {") {
		t.Errorf("parameters must follow path order, got:\n%s", got)
	}
	if !strings.Contains(got, `"/users/" + ghtmx.EscapePathSegment(id) + "/posts/" + ghtmx.EscapePathSegment(post)`) {
		t.Errorf("expected ordered substitution, got:\n%s", got)
	}
}

func TestWildcardParamUsesWildcardEscaper(t *testing.T) {
	got := generate(t,
		routes.Route{
			Verb: routes.GET, Path: "/files/{path...}", Handler: handler("example.com/app", "Files"),
			Params: []routes.RouteParam{{Name: "path", Wildcard: true}},
		},
	)
	if !strings.Contains(got, "ghtmx.EscapePathWildcard(path)") {
		t.Errorf("wildcard params preserve separators, got:\n%s", got)
	}
}

func TestCrossPackageCollisionPrefixed(t *testing.T) {
	got := generate(t,
		routes.Route{Verb: routes.GET, Path: "/users/{id}", Handler: handler("example.com/app/handlers", "GetUser"), Params: []routes.RouteParam{{Name: "id"}}},
		routes.Route{Verb: routes.GET, Path: "/admin/users/{id}", Handler: handler("example.com/app/admin", "GetUser"), Params: []routes.RouteParam{{Name: "id"}}},
	)
	if !strings.Contains(got, "func HandlersGetUser(") || !strings.Contains(got, "func AdminGetUser(") {
		t.Errorf("cross-package collisions must be package-prefixed, got:\n%s", got)
	}
}

func TestMultiVerbCollisionVerbSuffixed(t *testing.T) {
	got := generate(t,
		routes.Route{Verb: routes.GET, Path: "/form", Handler: handler("example.com/app", "Form")},
		routes.Route{Verb: routes.POST, Path: "/form", Handler: handler("example.com/app", "Form")},
	)
	if !strings.Contains(got, "const FormGetPath") || !strings.Contains(got, "const FormPostPath") {
		t.Errorf("multi-verb collisions must be verb-suffixed, got:\n%s", got)
	}
}

func TestUnresolvableCollisionReported(t *testing.T) {
	// The same handler registered twice for the same verb on different
	// paths cannot be disambiguated.
	_, conflicts := Naming(table(t,
		routes.Route{Verb: routes.GET, Path: "/a", Handler: handler("example.com/app", "H")},
		routes.Route{Verb: routes.GET, Path: "/b", Handler: handler("example.com/app", "H")},
	))
	if len(conflicts) != 1 || len(conflicts[0]) != 2 {
		t.Fatalf("expected one conflict group of two, got %+v", conflicts)
	}
}

func TestDeterministicOutput(t *testing.T) {
	rs := []routes.Route{
		{Verb: routes.GET, Path: "/b/{x}", Handler: handler("example.com/app", "B"), Params: []routes.RouteParam{{Name: "x"}}},
		{Verb: routes.GET, Path: "/a", Handler: handler("example.com/app", "A")},
		{Verb: routes.POST, Path: "/c", Handler: handler("example.com/app/other", "C")},
	}
	first := generate(t, rs...)
	second := generate(t, rs...)
	if first != second {
		t.Error("output must be byte-identical across runs")
	}
	// Symbols appear in sorted order.
	if strings.Index(first, "APath") > strings.Index(first, "func B(") {
		t.Errorf("symbols must be sorted, got:\n%s", first)
	}
}

func TestReservedWordParamName(t *testing.T) {
	got := generate(t,
		routes.Route{
			Verb: routes.GET, Path: "/by-type/{type}", Handler: handler("example.com/app", "ByType"),
			Params: []routes.RouteParam{{Name: "type"}},
		},
	)
	if !strings.Contains(got, "func ByType(typeParam string)") {
		t.Errorf("reserved words must be renamed, got:\n%s", got)
	}
}

func TestEmptyTableEmitsEmptyPackage(t *testing.T) {
	out, err := Generate(routes.NewTable(), Options{PackageName: "ghtmxgen"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "package ghtmxgen") {
		t.Errorf("expected a bare package clause, got:\n%s", out)
	}
}

func generateEvents(t *testing.T, events []Event, rs ...routes.Route) string {
	t.Helper()
	out, err := Generate(table(t, rs...), Options{PackageName: "ghtmxgen", Events: events})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "routes_ghtmx.go", out, 0); err != nil {
		t.Fatalf("generated output does not parse: %v\n%s", err, out)
	}
	return string(out)
}

func TestEventEmitterWithPayload(t *testing.T) {
	out := generateEvents(t, []Event{{
		Name:       "UserCreated",
		WireName:   "user-created",
		Params:     "(id string, name string)",
		DeclaredAt: "events.ghtmx",
	}})
	for _, want := range []string{
		"type UserCreatedPayload struct {",
		"`json:\"id\"`",
		"`json:\"name\"`",
		"func EmitUserCreated(w http.ResponseWriter, p UserCreatedPayload) error {",
		`return ghtmxruntime.AppendTrigger(w, "user-created", p)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

// TestEventEmittersFollowThePinnedHeaders: htmx 4 removed the
// HX-Trigger-After-Settle and HX-Trigger-After-Swap headers, so a 4.x pin
// generates only the plain emitter; the htmx 2 output keeps all three.
func TestEventEmittersFollowThePinnedHeaders(t *testing.T) {
	events := []Event{
		{Name: "UserCreated", WireName: "user-created", Params: "(id string)", DeclaredAt: "events.ghtmx"},
		{Name: "CartCleared", WireName: "cart-cleared", Params: "()", DeclaredAt: "events.ghtmx"},
	}
	out, err := Generate(table(t), Options{PackageName: "ghtmxgen", Events: events, OmitTriggerAfterEmitters: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "routes_ghtmx.go", out, 0); err != nil {
		t.Fatalf("generated output does not parse: %v\n%s", err, out)
	}
	four := string(out)
	for _, want := range []string{
		"func EmitUserCreated(w http.ResponseWriter, p UserCreatedPayload) error {",
		"func EmitCartCleared(w http.ResponseWriter) error {",
	} {
		if !strings.Contains(four, want) {
			t.Errorf("missing %q in htmx 4 output:\n%s", want, four)
		}
	}
	for _, gone := range []string{"AfterSettle", "AfterSwap", "HX-Trigger-After"} {
		if strings.Contains(four, gone) {
			t.Errorf("htmx 4 output must not mention %q:\n%s", gone, four)
		}
	}
	two := generateEvents(t, events)
	for _, want := range []string{"func EmitUserCreatedAfterSettle(", "func EmitUserCreatedAfterSwap(", "func EmitCartClearedAfterSwap("} {
		if !strings.Contains(two, want) {
			t.Errorf("htmx 2 output keeps %q:\n%s", want, two)
		}
	}
}

func TestEventEmitterPayloadLess(t *testing.T) {
	out := generateEvents(t, []Event{{
		Name:       "CartCleared",
		WireName:   "cart-cleared",
		Params:     "()",
		DeclaredAt: "events.ghtmx",
	}})
	if !strings.Contains(out, "func EmitCartCleared(w http.ResponseWriter) error {") {
		t.Errorf("payload-less emitter takes only the writer:\n%s", out)
	}
	if strings.Contains(out, "CartClearedPayload") {
		t.Errorf("payload-less events emit no payload type:\n%s", out)
	}
	if !strings.Contains(out, `AppendTrigger(w, "cart-cleared", nil)`) {
		t.Errorf("payload-less emission passes nil:\n%s", out)
	}
}

func TestEventGroupedParamsExpand(t *testing.T) {
	out := generateEvents(t, []Event{{
		Name:     "PairChanged",
		WireName: "pair-changed",
		Params:   "(a, b int)",
	}})
	if !strings.Contains(out, "A int") || !strings.Contains(out, "B int") || !strings.Contains(out, "`json:\"a\"`") || !strings.Contains(out, "`json:\"b\"`") {
		t.Errorf("grouped parameters expand to individual fields:\n%s", out)
	}
}

func TestEventRouteSymbolCollision(t *testing.T) {
	rs := routes.Route{Verb: "GET", Path: "/emit/{id}", Handler: handler("example.com/app", "emitUserCreated"), Params: []routes.RouteParam{{Name: "id"}}}
	events := []Event{{Name: "UserCreated", WireName: "user-created", Params: "()", DeclaredAt: "events.ghtmx"}}

	byName, _ := Naming(table(t, rs))
	msgs := EventCollisions(byName, events)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "EmitUserCreated") {
		t.Fatalf("expected one collision naming the symbol, got %v", msgs)
	}
	// The colliding event is excluded from emission; the route stays.
	out := generateEvents(t, events, rs)
	if strings.Contains(out, `AppendTrigger`) {
		t.Errorf("colliding event must not be emitted:\n%s", out)
	}
	if !strings.Contains(out, "func EmitUserCreated(id string) ghtmx.SafeURL {") {
		t.Errorf("the route constructor keeps its name:\n%s", out)
	}
}

func TestEventsWithoutRoutesGenerate(t *testing.T) {
	out := generateEvents(t, []Event{{Name: "PingSent", WireName: "ping-sent", Params: "()"}})
	if !strings.Contains(out, "func EmitPingSent(w http.ResponseWriter) error {") {
		t.Errorf("events alone must generate:\n%s", out)
	}
	if strings.Contains(out, `"github.com/go-monolith/ghtmx"`+"\n") && !strings.Contains(out, "ghtmx.") {
		t.Errorf("the root import must not be emitted unused:\n%s", out)
	}
}

func TestPayloadLessEventNoFalsePayloadCollision(t *testing.T) {
	// A route constructor named like <Name>Payload only collides when the
	// event actually generates that type.
	rs := routes.Route{Verb: "GET", Path: "/p/{id}", Handler: handler("example.com/app", "userCreatedPayload"), Params: []routes.RouteParam{{Name: "id"}}}
	events := []Event{{Name: "UserCreated", WireName: "user-created", Params: "()"}}

	byName, _ := Naming(table(t, rs))
	if msgs := EventCollisions(byName, events); len(msgs) != 0 {
		t.Fatalf("payload-less events generate no Payload type, got %v", msgs)
	}
	out := generateEvents(t, events, rs)
	if !strings.Contains(out, "func EmitUserCreated(w http.ResponseWriter) error {") {
		t.Errorf("the emitter must be generated:\n%s", out)
	}
}

func TestEventVersusEventSymbolCollision(t *testing.T) {
	// "EmitX" with params generates the payload type EmitXPayload;
	// payload-less "XPayload" generates the func EmitXPayload. Distinct
	// wire names, so E0305 does not fire — the central check must.
	events := []Event{
		{Name: "EmitX", WireName: "emit-x", Params: "(id string)", DeclaredAt: "a.ghtmx"},
		{Name: "XPayload", WireName: "x-payload", Params: "()", DeclaredAt: "b.ghtmx"},
	}
	byName, _ := Naming(table(t))
	msgs := EventCollisions(byName, events)
	if len(msgs) != 1 || !strings.Contains(msgs[0], "EmitXPayload") || !strings.Contains(msgs[0], "event EmitX") {
		t.Fatalf("expected one event-vs-event collision naming both, got %v", msgs)
	}
	// The later event is excluded; the first one's symbols emit once.
	out := generateEvents(t, events)
	if got := strings.Count(out, "EmitXPayload"); got == 0 {
		t.Errorf("the first event's payload type must be emitted:\n%s", out)
	}
	if strings.Contains(out, `"x-payload"`) {
		t.Errorf("the colliding later event must be excluded:\n%s", out)
	}
}

func TestEventTimingVariantEmitters(t *testing.T) {
	out := generateEvents(t, []Event{{Name: "ItemSaved", WireName: "item-saved", Params: "(id string)", DeclaredAt: "e.ghtmx"}})
	for _, want := range []string{
		"func EmitItemSaved(w http.ResponseWriter, p ItemSavedPayload) error {",
		"func EmitItemSavedAfterSettle(w http.ResponseWriter, p ItemSavedPayload) error {",
		"func EmitItemSavedAfterSwap(w http.ResponseWriter, p ItemSavedPayload) error {",
		`ghtmxruntime.AppendTriggerAfterSettle(w, "item-saved", p)`,
		`ghtmxruntime.AppendTriggerAfterSwap(w, "item-saved", p)`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	if got := strings.Count(out, "type ItemSavedPayload struct {"); got != 1 {
		t.Errorf("one payload type serves all timing variants, got %d", got)
	}
}

func TestHTMXScriptEmission(t *testing.T) {
	out, err := Generate(table(t), Options{PackageName: "ghtmxgen", HtmxVersion: "2.0.10"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "func HTMXScript(opts ...ghtmx.ScriptOption) ghtmx.Component {") {
		t.Errorf("expected the HTMXScript helper:\n%s", got)
	}
	if !strings.Contains(got, `ghtmx.HTMXScriptTag("2.0.10", opts...)`) {
		t.Errorf("the configured version must be baked in:\n%s", got)
	}

	// A route claiming the name suppresses emission (reported E0404 by
	// the caller).
	rs := routes.Route{Verb: "GET", Path: "/s/{x}", Handler: handler("example.com/app", "HTMXScript"), Params: []routes.RouteParam{{Name: "x"}}}
	out, err = Generate(table(t, rs), Options{PackageName: "ghtmxgen", HtmxVersion: "2.0.10"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "HTMXScriptTag") {
		t.Errorf("a colliding route must suppress the helper:\n%s", out)
	}
}

func TestParamlessHTMXScriptRouteCoexists(t *testing.T) {
	// A paramless route named HTMXScript emits HTMXScriptPath, which
	// coexists with the helper func.
	rs := routes.Route{Verb: "GET", Path: "/s", Handler: handler("example.com/app", "HTMXScript")}
	out, err := Generate(table(t, rs), Options{PackageName: "ghtmxgen", HtmxVersion: "2.0.10"})
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)
	if !strings.Contains(got, "const HTMXScriptPath") || !strings.Contains(got, "func HTMXScript(") {
		t.Errorf("path constant and helper must coexist:\n%s", got)
	}
}
