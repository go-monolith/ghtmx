package routes

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/diag"
)

// discoverSrc parses the given files as one package and runs discovery.
// Imports never need to resolve: discovery is syntax-only by construction.
func discoverSrc(t *testing.T, files map[string]string) (*Table, []diag.Diagnostic) {
	t.Helper()
	fset := token.NewFileSet()
	pkg := &Package{PkgPath: "example.com/app", Name: "app", Fset: fset}
	for name, src := range files {
		f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
		if err != nil {
			t.Fatalf("fixture %s does not parse: %v", name, err)
		}
		pkg.Files = append(pkg.Files, f)
	}
	sink := diag.NewSink(nil)
	table := Discover([]*Package{pkg}, sink)
	return table, sink.Diagnostics()
}

func requireRoute(t *testing.T, table *Table, verb Verb, path string, handler string) Route {
	t.Helper()
	for _, r := range table.All() {
		if r.Verb == verb && r.Path == path && r.Handler.String() == handler {
			return r
		}
	}
	t.Fatalf("route %s %s -> %s not found; table: %+v", verb, path, handler, table.All())
	return Route{}
}

func requireNoDiagnostics(t *testing.T, diags []diag.Diagnostic) {
	t.Helper()
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", diags)
	}
}

func TestNetHTTPServeMux(t *testing.T) {
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"net/http"

	"example.com/app/handlers"
)

func routes() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", handlers.GetUser)
	mux.Handle("POST /users", http.HandlerFunc(handlers.CreateUser))
	mux.HandleFunc("GET /files/{path...}", handlers.Files)
	mux.HandleFunc("GET /static/", handlers.Static)
	mux.HandleFunc("/health", handlers.Health)
	http.HandleFunc("DELETE /sessions/{id}", handlers.DeleteSession)
}
`})
	requireNoDiagnostics(t, diags)

	r := requireRoute(t, table, GET, "/users/{id}", "example.com/app/handlers.GetUser")
	if len(r.Params) != 1 || r.Params[0].Name != "id" || r.Params[0].Wildcard {
		t.Errorf("params = %+v", r.Params)
	}
	if r.Recognizer != "nethttp" || r.Origin != Discovered {
		t.Errorf("recognizer/origin = %s/%s", r.Recognizer, r.Origin)
	}
	// Handle with a conversion resolves to the underlying symbol.
	requireRoute(t, table, POST, "/users", "example.com/app/handlers.CreateUser")
	// Wildcard patterns keep their semantics.
	r = requireRoute(t, table, GET, "/files/{path...}", "example.com/app/handlers.Files")
	if len(r.Params) != 1 || !r.Params[0].Wildcard {
		t.Errorf("wildcard params = %+v", r.Params)
	}
	// Trailing-slash pattern is preserved.
	requireRoute(t, table, GET, "/static/", "example.com/app/handlers.Static")
	// A pattern without a method registers for any verb and is bindable
	// from each verb attribute.
	requireRoute(t, table, AnyVerb, "/health", "example.com/app/handlers.Health")
	if _, ok := table.Lookup(GET, SymbolRef{"example.com/app/handlers", "Health"}); !ok {
		t.Error("AnyVerb route must match GET lookups")
	}
	// Package-level http.HandleFunc registers on the DefaultServeMux.
	requireRoute(t, table, DELETE, "/sessions/{id}", "example.com/app/handlers.DeleteSession")
}

func TestChi(t *testing.T) {
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/go-chi/chi/v5"

	"example.com/app/handlers"
	"example.com/app/mw"
)

const usersPath = "/users"

func routes() {
	r := chi.NewRouter()
	r.Get("/users/{id}", handlers.GetUser)
	r.Post(usersPath, handlers.CreateUser)
	r.Put("/users/{id}", handlers.UpdateUser)
	r.Patch("/users/{id}", handlers.PatchUser)
	r.Delete("/users/{id}", handlers.DeleteUser)
	r.MethodFunc("GET", "/profile/{id:[0-9]+}", handlers.Profile)
	r.With(mw.Auth).Get("/admin", handlers.Admin)
	r.Route("/api", func(r chi.Router) {
		r.Get("/ping", handlers.Ping)
		r.Route("/v2", func(r chi.Router) {
			r.Get("/deep", handlers.Deep)
		})
	})
	r.Group(func(r chi.Router) {
		r.Get("/grouped", handlers.Grouped)
	})
	sub := chi.NewRouter()
	sub.Get("/list", handlers.List)
	r.Mount("/admin/sub", sub)
	r.Get("/wrapped", mw.Auth(handlers.Wrapped))
}
`})
	requireNoDiagnostics(t, diags)

	for verb, h := range map[Verb]string{
		GET: "GetUser", PUT: "UpdateUser", PATCH: "PatchUser", DELETE: "DeleteUser",
	} {
		requireRoute(t, table, verb, "/users/{id}", "example.com/app/handlers."+h)
	}
	// Package const path.
	requireRoute(t, table, POST, "/users", "example.com/app/handlers.CreateUser")
	// MethodFunc with verb string; regex constraint normalized away.
	r := requireRoute(t, table, GET, "/profile/{id}", "example.com/app/handlers.Profile")
	if r.Params[0].Name != "id" {
		t.Errorf("params = %+v", r.Params)
	}
	// With() middleware chain does not hide the route (FR-014).
	requireRoute(t, table, GET, "/admin", "example.com/app/handlers.Admin")
	// Route nesting composes prefixes to arbitrary depth (FR-013).
	requireRoute(t, table, GET, "/api/ping", "example.com/app/handlers.Ping")
	requireRoute(t, table, GET, "/api/v2/deep", "example.com/app/handlers.Deep")
	// Group without prefix.
	requireRoute(t, table, GET, "/grouped", "example.com/app/handlers.Grouped")
	// Mounted same-function sub-router.
	requireRoute(t, table, GET, "/admin/sub/list", "example.com/app/handlers.List")
	// Middleware-wrapped handler resolves to the underlying symbol.
	requireRoute(t, table, GET, "/wrapped", "example.com/app/handlers.Wrapped")
}

func TestEcho(t *testing.T) {
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/labstack/echo/v4"

	"example.com/app/handlers"
)

func routes() {
	e := echo.New()
	e.GET("/users/:id", handlers.GetUser)
	e.POST("/users", handlers.CreateUser)
	e.PUT("/users/:id", handlers.UpdateUser)
	e.PATCH("/users/:id", handlers.PatchUser)
	e.DELETE("/users/:id", handlers.DeleteUser)
	g := e.Group("/api")
	g.GET("/ping", handlers.Ping)
	v2 := g.Group("/v2")
	v2.GET("/deep", handlers.Deep)
	e.GET("/files/*", handlers.Files)
}
`})
	requireNoDiagnostics(t, diags)

	// Echo's :param syntax is normalized (FR-012) with the original kept.
	r := requireRoute(t, table, GET, "/users/{id}", "example.com/app/handlers.GetUser")
	if r.OriginalPath != "/users/:id" {
		t.Errorf("OriginalPath = %q", r.OriginalPath)
	}
	if r.Recognizer != "echo" {
		t.Errorf("recognizer = %s", r.Recognizer)
	}
	requireRoute(t, table, POST, "/users", "example.com/app/handlers.CreateUser")
	requireRoute(t, table, GET, "/api/ping", "example.com/app/handlers.Ping")
	requireRoute(t, table, GET, "/api/v2/deep", "example.com/app/handlers.Deep")
	r = requireRoute(t, table, GET, "/files/{rest...}", "example.com/app/handlers.Files")
	if !r.Params[0].Wildcard {
		t.Errorf("params = %+v", r.Params)
	}
}

func TestGin(t *testing.T) {
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/gin-gonic/gin"

	"example.com/app/handlers"
	"example.com/app/mw"
)

func routes() {
	r := gin.Default()
	r.GET("/users/:id", handlers.GetUser)
	r.POST("/users", mw.Auth, handlers.CreateUser)
	r.Handle("DELETE", "/users/:id", handlers.DeleteUser)
	v1 := r.Group("/v1")
	{
		v1.GET("/ping", handlers.Ping)
	}
	r.GET("/files/*filepath", handlers.Files)
}
`})
	requireNoDiagnostics(t, diags)

	requireRoute(t, table, GET, "/users/{id}", "example.com/app/handlers.GetUser")
	// gin's handler is the last argument; middleware precedes it.
	requireRoute(t, table, POST, "/users", "example.com/app/handlers.CreateUser")
	requireRoute(t, table, DELETE, "/users/{id}", "example.com/app/handlers.DeleteUser")
	requireRoute(t, table, GET, "/v1/ping", "example.com/app/handlers.Ping")
	r := requireRoute(t, table, GET, "/files/{filepath...}", "example.com/app/handlers.Files")
	if r.Params[0].Name != "filepath" || !r.Params[0].Wildcard {
		t.Errorf("params = %+v", r.Params)
	}
}

func TestFiber(t *testing.T) {
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/gofiber/fiber/v2"

	"example.com/app/handlers"
)

func routes() {
	app := fiber.New()
	app.Get("/users/:id", handlers.GetUser)
	app.Post("/users", handlers.CreateUser)
	api := app.Group("/api")
	api.Get("/ping", handlers.Ping)
	app.Get("/files/*", handlers.Files)
}
`})
	requireNoDiagnostics(t, diags)

	requireRoute(t, table, GET, "/users/{id}", "example.com/app/handlers.GetUser")
	requireRoute(t, table, POST, "/users", "example.com/app/handlers.CreateUser")
	requireRoute(t, table, GET, "/api/ping", "example.com/app/handlers.Ping")
	requireRoute(t, table, GET, "/files/{rest...}", "example.com/app/handlers.Files")
}

func TestFlavourIsPerRegistrationSite(t *testing.T) {
	// Two routers from different flavours in one function (FR-012: flavour
	// determined per site, never assumed globally).
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/go-chi/chi/v5"
	"github.com/labstack/echo/v4"

	"example.com/app/handlers"
)

func routes() {
	r := chi.NewRouter()
	r.Get("/chi/{id}", handlers.A)
	e := echo.New()
	e.GET("/echo/:id", handlers.B)
}
`})
	requireNoDiagnostics(t, diags)
	a := requireRoute(t, table, GET, "/chi/{id}", "example.com/app/handlers.A")
	b := requireRoute(t, table, GET, "/echo/{id}", "example.com/app/handlers.B")
	if a.Recognizer != "chi" || b.Recognizer != "echo" {
		t.Errorf("recognizers = %s, %s", a.Recognizer, b.Recognizer)
	}
}

func TestRouterParamSeeding(t *testing.T) {
	// A function receiving a router parameter registers root-relative
	// routes.
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"example.com/app/handlers"
)

func RegisterMux(mux *http.ServeMux) {
	mux.HandleFunc("GET /a", handlers.A)
}

func RegisterChi(r chi.Router) {
	r.Get("/b", handlers.B)
}
`})
	requireNoDiagnostics(t, diags)
	requireRoute(t, table, GET, "/a", "example.com/app/handlers.A")
	requireRoute(t, table, GET, "/b", "example.com/app/handlers.B")
}

func TestUnresolvableRegistrations(t *testing.T) {
	_, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/go-chi/chi/v5"

	"example.com/app/handlers"
	"example.com/app/other"
)

func dynamicPath() string { return "/x" }

func routes(h SomeStruct) {
	r := chi.NewRouter()
	r.Get(dynamicPath(), handlers.A)
	r.Get("/anon", func(w, r any) {})
	r.Get("/method-value", h.Users)
	r.Route("/named", other.Mount)
}

type SomeStruct struct{}
`})
	if len(diags) != 4 {
		t.Fatalf("expected 4 unresolvable diagnostics, got %+v", diags)
	}
	for _, d := range diags {
		if d.ID != diag.UnresolvableRoute {
			t.Errorf("expected %s, got %s: %s", diag.UnresolvableRoute, d.ID, d.Message)
		}
		if !strings.Contains(d.Suggest, "//ghtmx:route") {
			t.Errorf("diagnostic must point at the escape hatch, got %q", d.Suggest)
		}
		if d.Pos.File == "" || d.Pos.Line == 0 {
			t.Errorf("diagnostic must carry a position: %+v", d.Pos)
		}
	}
}

func TestUnknownReceiversAreIgnoredSilently(t *testing.T) {
	_, diags := discoverSrc(t, map[string]string{"main.go": `
package app

type notARouter struct{}

func (n notARouter) Get(path string, h any) {}

func routes() {
	n := notARouter{}
	n.Get("/looks/like/a/route", nil)
}
`})
	requireNoDiagnostics(t, diags)
}

func TestAnnotations(t *testing.T) {
	t.Run("valid declarations enter the table as declared routes", func(t *testing.T) {
		table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import "example.com/app/handlers"

//ghtmx:route GET /admin/users/{id} handlers.AdminUserShow
//ghtmx:route POST /webhooks localHook
//ghtmx:route GET /admin/audit handlers.AuditLog nav

func localHook() {}
`})
		requireNoDiagnostics(t, diags)
		r := requireRoute(t, table, GET, "/admin/users/{id}", "example.com/app/handlers.AdminUserShow")
		if r.Origin != Declared || r.Recognizer != "annotation" {
			t.Errorf("origin/recognizer = %s/%s", r.Origin, r.Recognizer)
		}
		if r.NavOnly {
			t.Error("an unmarked annotation must not be nav-only")
		}
		// A bare symbol resolves to the file's own package.
		requireRoute(t, table, POST, "/webhooks", "example.com/app.localHook")
		// The trailing nav marker declares a navigation-only route.
		if r := requireRoute(t, table, GET, "/admin/audit", "example.com/app/handlers.AuditLog"); !r.NavOnly {
			t.Error("the nav marker must set NavOnly")
		}
	})
	t.Run("malformed annotations are E0403", func(t *testing.T) {
		_, diags := discoverSrc(t, map[string]string{"main.go": `
package app

//ghtmx:route FETCH /x handlers.A
//ghtmx:route GET nopath handlers.A
//ghtmx:route GET /x notimported.A
//ghtmx:route GET /x
//ghtmx:route GET /x localHook wat
//ghtmx:route GET /y localHook NAV

func localHook() {}
`})
		// The marker set is closed and case-sensitive: "wat" and "NAV" are
		// both unknown markers, not silently-ignored tokens.
		if len(diags) != 6 {
			t.Fatalf("expected 6 diagnostics, got %+v", diags)
		}
		for _, d := range diags {
			if d.ID != diag.MalformedAnnotation {
				t.Errorf("expected %s, got %s: %s", diag.MalformedAnnotation, d.ID, d.Message)
			}
		}
	})
}

func TestRoutePrefix(t *testing.T) {
	t.Run("prefixes discovered and declared routes alike", func(t *testing.T) {
		table, diags := discoverSrc(t, map[string]string{"main.go": `
//ghtmx:routeprefix /admin/user
package app

import (
	"github.com/go-chi/chi/v5"

	"example.com/app/handlers"
)

//ghtmx:route GET /preferences handlers.Preferences

func routes(r chi.Router) {
	r.Get("/profiles/{id}", handlers.Profile)
	r.Route("/mcp", func(r chi.Router) {
		r.Post("/servers", handlers.AddServer)
	})
}
`})
		requireNoDiagnostics(t, diags)
		// A route registered inside a sub-app mounted at /admin/user is
		// discovered at its sub-app-relative path; the directive supplies
		// the mount point no syntactic recognizer could find.
		r := requireRoute(t, table, GET, "/admin/user/profiles/{id}", "example.com/app/handlers.Profile")
		if len(r.Params) != 1 || r.Params[0].Name != "id" {
			t.Errorf("prefixing must not disturb params, got %+v", r.Params)
		}
		if r.OriginalPath != "/admin/user/profiles/{id}" {
			t.Errorf("OriginalPath must carry the prefix too, got %q", r.OriginalPath)
		}
		// Group prefixes compose under the package prefix.
		requireRoute(t, table, POST, "/admin/user/mcp/servers", "example.com/app/handlers.AddServer")
		// Annotations are prefixed as well, so the two stay consistent.
		requireRoute(t, table, GET, "/admin/user/preferences", "example.com/app/handlers.Preferences")
	})

	t.Run("applies across every file in the package", func(t *testing.T) {
		table, diags := discoverSrc(t, map[string]string{
			"prefix.go": `
//ghtmx:routeprefix /billing
package app
`,
			"routes.go": `
package app

//ghtmx:route GET /invoices localHook

func localHook() {}
`})
		requireNoDiagnostics(t, diags)
		requireRoute(t, table, GET, "/billing/invoices", "example.com/app.localHook")
	})

	t.Run("a trailing slash is not a path segment", func(t *testing.T) {
		table, diags := discoverSrc(t, map[string]string{"main.go": `
//ghtmx:routeprefix /admin/
package app

//ghtmx:route GET /users localHook

func localHook() {}
`})
		requireNoDiagnostics(t, diags)
		requireRoute(t, table, GET, "/admin/users", "example.com/app.localHook")
	})

	t.Run("a root prefix adds nothing", func(t *testing.T) {
		table, diags := discoverSrc(t, map[string]string{"main.go": `
//ghtmx:routeprefix /
package app

//ghtmx:route GET /users localHook

func localHook() {}
`})
		requireNoDiagnostics(t, diags)
		requireRoute(t, table, GET, "/users", "example.com/app.localHook")
	})

	t.Run("malformed directives are E0403 and apply no prefix", func(t *testing.T) {
		for name, src := range map[string]string{
			"no argument":     "//ghtmx:routeprefix",
			"two arguments":   "//ghtmx:routeprefix /a /b",
			"not rooted":      "//ghtmx:routeprefix admin",
			"parameterised":   "//ghtmx:routeprefix /tenants/{id}",
			"colon parameter": "//ghtmx:routeprefix /tenants/:id",
		} {
			t.Run(name, func(t *testing.T) {
				table, diags := discoverSrc(t, map[string]string{"main.go": src + `
package app

//ghtmx:route GET /users localHook

func localHook() {}
`})
				if len(diags) != 1 || diags[0].ID != diag.MalformedAnnotation {
					t.Fatalf("expected one E0403, got %+v", diags)
				}
				if !strings.Contains(diags[0].Suggest, "routeprefix") {
					t.Errorf("the remedy must show the directive's form, got %q", diags[0].Suggest)
				}
				// No guessed prefix: silently moving every route in the
				// package would be worse than leaving them where they are.
				requireRoute(t, table, GET, "/users", "example.com/app.localHook")
			})
		}
	})

	t.Run("conflicting prefixes are E0403 naming both sites", func(t *testing.T) {
		_, diags := discoverSrc(t, map[string]string{
			"a.go": `
//ghtmx:routeprefix /admin
package app
`,
			"b.go": `
//ghtmx:routeprefix /billing
package app
`})
		if len(diags) != 1 || diags[0].ID != diag.MalformedAnnotation {
			t.Fatalf("expected one E0403, got %+v", diags)
		}
		for _, want := range []string{"/admin", "/billing", ".go"} {
			if !strings.Contains(diags[0].Message, want) {
				t.Errorf("message must name both sites, missing %q in %q", want, diags[0].Message)
			}
		}
	})

	t.Run("the same prefix repeated is not a conflict", func(t *testing.T) {
		table, diags := discoverSrc(t, map[string]string{
			"a.go": `
//ghtmx:routeprefix /admin
package app

//ghtmx:route GET /one localA

func localA() {}
`,
			"b.go": `
//ghtmx:routeprefix /admin
package app

//ghtmx:route GET /two localB

func localB() {}
`})
		requireNoDiagnostics(t, diags)
		requireRoute(t, table, GET, "/admin/one", "example.com/app.localA")
		requireRoute(t, table, GET, "/admin/two", "example.com/app.localB")
	})

	t.Run("the directive is not read as a malformed route annotation", func(t *testing.T) {
		_, diags := discoverSrc(t, map[string]string{"main.go": `
//ghtmx:routeprefix /admin
package app
`})
		requireNoDiagnostics(t, diags)
	})
}

func TestDuplicateRoutes(t *testing.T) {
	_, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/go-chi/chi/v5"

	"example.com/app/handlers"
)

//ghtmx:route GET /users/{id} handlers.Conflicting

func routes() {
	r := chi.NewRouter()
	r.Get("/users/{id}", handlers.GetUser)
	r.Get("/other", handlers.Other)
}
`})
	if len(diags) != 1 {
		t.Fatalf("expected 1 duplicate diagnostic, got %+v", diags)
	}
	d := diags[0]
	if d.ID != diag.DuplicateRoute {
		t.Errorf("expected %s, got %s", diag.DuplicateRoute, d.ID)
	}
	// The error names both registration sites (FR-050): its own position
	// plus the other site in the message.
	if !strings.Contains(d.Message, "main.go") {
		t.Errorf("message must name the other site, got %q", d.Message)
	}
}

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		path   string
		style  ParamStyle
		want   string
		params int
	}{
		{"/users/{id}", BraceStyle, "/users/{id}", 1},
		{"/files/{path...}", BraceStyle, "/files/{path...}", 1},
		{"/users/{id:[0-9]+}", BraceStyle, "/users/{id}", 1},
		{"/users/:id", ColonStyle, "/users/{id}", 1},
		{"/users/:id/posts/:post", ColonStyle, "/users/{id}/posts/{post}", 2},
		{"/files/*", ColonStyle, "/files/{rest...}", 1},
		{"/files/*filepath", ColonStyle, "/files/{filepath...}", 1},
		{"/opt/:id?", ColonStyle, "/opt/{id}", 1},
		{"/plain", ColonStyle, "/plain", 0},
		// net/http's end-of-path anchor is not a parameter: the home
		// route must emit a constant, never an Index("$") constructor.
		{"/{$}", BraceStyle, "/", 0},
		{"/admin/{$}", BraceStyle, "/admin/", 0},
		{"/users/{id}/{$}", BraceStyle, "/users/{id}/", 1},
	}
	for _, tt := range tests {
		got, params := NormalizePath(tt.path, tt.style)
		if got != tt.want || len(params) != tt.params {
			t.Errorf("NormalizePath(%q) = %q with %d params, want %q with %d", tt.path, got, len(params), tt.want, tt.params)
		}
	}
}

func TestJoinPaths(t *testing.T) {
	tests := []struct{ prefix, path, want string }{
		{"", "/users", "/users"},
		{"/api", "/users", "/api/users"},
		{"/api/", "/users", "/api/users"},
		{"/api", "", "/api"},
		{"/api", "/", "/api"},
		{"", "/", "/"},
	}
	for _, tt := range tests {
		if got := JoinPaths(tt.prefix, tt.path); got != tt.want {
			t.Errorf("JoinPaths(%q, %q) = %q, want %q", tt.prefix, tt.path, got, tt.want)
		}
	}
}

// Regression tests for adversarial cases found in review.

func TestZeroArgRegistrationCallsDoNotPanic(t *testing.T) {
	// Malformed user code (won't compile under go build) must never panic
	// the discoverer.
	_, _ = discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/labstack/echo/v4"
	"github.com/gin-gonic/gin"
	"github.com/gofiber/fiber/v2"
)

func routes() {
	e := echo.New()
	e.GET()
	e.Add()
	r := gin.Default()
	r.Handle()
	r.POST("/only-path")
	app := fiber.New()
	app.Get("/only-path")
}
`})
}

func TestNestedMounts(t *testing.T) {
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/go-chi/chi/v5"

	"example.com/app/handlers"
)

func routes() {
	inner := chi.NewRouter()
	inner.Get("/leaf", handlers.Leaf)
	mid := chi.NewRouter()
	mid.Mount("/b", inner)
	root := chi.NewRouter()
	root.Mount("/a", mid)
}
`})
	requireNoDiagnostics(t, diags)
	requireRoute(t, table, GET, "/a/b/leaf", "example.com/app/handlers.Leaf")
}

func TestMountWithMiddlewareChainedRegistration(t *testing.T) {
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/go-chi/chi/v5"

	"example.com/app/handlers"
	"example.com/app/mw"
)

func routes() {
	sub := chi.NewRouter()
	sub.With(mw.Auth).Get("/x", handlers.X)
	r := chi.NewRouter()
	r.Mount("/s", sub)
}
`})
	requireNoDiagnostics(t, diags)
	requireRoute(t, table, GET, "/s/x", "example.com/app/handlers.X")
}

func TestMountInsideClosure(t *testing.T) {
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/go-chi/chi/v5"

	"example.com/app/handlers"
)

func routes() {
	sub := chi.NewRouter()
	sub.Get("/x", handlers.X)
	r := chi.NewRouter()
	r.Route("/api", func(r chi.Router) {
		r.Mount("/inner", sub)
	})
}
`})
	requireNoDiagnostics(t, diags)
	requireRoute(t, table, GET, "/api/inner/x", "example.com/app/handlers.X")
}

func TestRouterAliasAssignment(t *testing.T) {
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/go-chi/chi/v5"

	"example.com/app/handlers"
)

func routes() {
	r := chi.NewRouter()
	alias := r
	alias.Get("/via-alias", handlers.X)
}
`})
	requireNoDiagnostics(t, diags)
	requireRoute(t, table, GET, "/via-alias", "example.com/app/handlers.X")
}

func TestLocalShadowingOfPackageConst(t *testing.T) {
	_, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/go-chi/chi/v5"

	"example.com/app/handlers"
)

const usersPath = "/users"

func dynamic() string { return "/other" }

func routes() {
	usersPath := dynamic()
	r := chi.NewRouter()
	r.Get(usersPath, handlers.X)
}
`})
	// The shadowed name must NOT resolve to the package const; it is
	// reported as unresolvable instead of silently registering /users.
	if len(diags) != 1 || diags[0].ID != diag.UnresolvableRoute {
		t.Fatalf("expected one unresolvable diagnostic, got %+v", diags)
	}
}

func TestChiMultiParamSegment(t *testing.T) {
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"github.com/go-chi/chi/v5"

	"example.com/app/handlers"
)

func routes() {
	r := chi.NewRouter()
	r.Get("/articles/{date}-{slug}", handlers.Article)
}
`})
	requireNoDiagnostics(t, diags)
	r := requireRoute(t, table, GET, "/articles/{date}-{slug}", "example.com/app/handlers.Article")
	if len(r.Params) != 2 || r.Params[0].Name != "date" || r.Params[1].Name != "slug" {
		t.Errorf("params = %+v", r.Params)
	}
}

func TestLookupSpecificVerbBeatsAnyVerb(t *testing.T) {
	table := NewTable()
	h := SymbolRef{"example.com/app/handlers", "X"}
	if _, ok := table.Add(Route{Verb: AnyVerb, Path: "/x", Handler: h}); !ok {
		t.Fatal("add AnyVerb failed")
	}
	if _, ok := table.Add(Route{Verb: GET, Path: "/x", Handler: h}); !ok {
		t.Fatal("add GET failed (AnyVerb and GET may coexist, ServeMux-style)")
	}
	r, ok := table.Lookup(GET, h)
	if !ok || r.Verb != GET {
		t.Errorf("Lookup must prefer the exact verb, got %+v ok=%v", r, ok)
	}
	r, ok = table.Lookup(POST, h)
	if !ok || r.Verb != AnyVerb {
		t.Errorf("Lookup must fall back to AnyVerb, got %+v ok=%v", r, ok)
	}
}

func TestTableAddDuplicateContract(t *testing.T) {
	table := NewTable()
	a := Route{Verb: GET, Path: "/x", Handler: SymbolRef{"p", "A"}, Pos: Position{File: "a.go", Line: 1, Col: 1}}
	b := Route{Verb: GET, Path: "/x", Handler: SymbolRef{"p", "B"}}
	if _, ok := table.Add(a); !ok {
		t.Fatal("first add must succeed")
	}
	existing, ok := table.Add(b)
	if ok || existing.Handler.Name != "A" {
		t.Fatalf("duplicate add must return the existing route, got %+v ok=%v", existing, ok)
	}
	// The dropped duplicate must not appear in handler lookups.
	if got := table.ByHandler(SymbolRef{"p", "B"}); len(got) != 0 {
		t.Errorf("dropped duplicate leaked into ByHandler: %+v", got)
	}
}

func TestJoinPathsAlwaysLeadingSlash(t *testing.T) {
	if got := JoinPaths("", "users"); got != "/users" {
		t.Errorf("JoinPaths(\"\", \"users\") = %q, want /users", got)
	}
}

func TestAnnotationEmptyParamRejected(t *testing.T) {
	_, diags := discoverSrc(t, map[string]string{"main.go": `
package app

//ghtmx:route GET /x/{} localHandler

func localHandler() {}
`})
	if len(diags) != 1 || diags[0].ID != diag.MalformedAnnotation {
		t.Fatalf("expected a malformed-annotation diagnostic for the empty param, got %+v", diags)
	}
}

func TestHostBearingServeMuxPattern(t *testing.T) {
	table, diags := discoverSrc(t, map[string]string{"main.go": `
package app

import (
	"net/http"

	"example.com/app/handlers"
)

func routes() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET example.com/x", handlers.X)
}
`})
	requireNoDiagnostics(t, diags)
	requireRoute(t, table, GET, "example.com/x", "example.com/app/handlers.X")
}
