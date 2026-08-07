package routetable_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/routetable"
)

// declared is a stand-in for what Load returns: the table ghtmx derived.
func declared() []routetable.Route {
	return []routetable.Route{
		{
			Verb: "GET", Path: "/admin/users", OriginalPath: "/admin/users",
			HandlerPkg: "example.com/app/adminpages", HandlerName: "ListUsers",
			Origin: "declared", Recognizer: "annotation", Source: "adminsubapp.go:12:1",
		},
		{
			Verb: "POST", Path: "/admin/users", OriginalPath: "/admin/users",
			HandlerPkg: "example.com/app/adminpages", HandlerName: "CreateUser",
			Origin: "declared", Recognizer: "annotation", Source: "adminsubapp.go:13:1",
		},
	}
}

func TestDiffAgreementIsEmpty(t *testing.T) {
	if ms := routetable.Diff(declared(), declared()); len(ms) != 0 {
		t.Fatalf("identical tables must not differ, got %v", ms)
	}
	if got := routetable.Report(nil); got != "" {
		t.Errorf("Report of nothing = %q, want empty", got)
	}
}

func TestDiffMissingRoute(t *testing.T) {
	// The router serves only one of the two annotated routes: the other
	// annotation is a lie, and its template bindings would 404.
	actual := declared()[:1]
	ms := routetable.Diff(declared(), actual)
	if len(ms) != 1 {
		t.Fatalf("expected one mismatch, got %v", ms)
	}
	if ms[0].Kind != routetable.KindMissing || ms[0].Verb != "POST" {
		t.Errorf("kind/verb = %s/%s, want missing/POST", ms[0].Kind, ms[0].Verb)
	}
	if ms[0].Declared == nil || ms[0].Actual != nil {
		t.Errorf("a missing route carries only the declared side, got %+v", ms[0])
	}
	// The message names the annotation site, which is what a failing CI
	// job needs in order to fix it.
	if !strings.Contains(ms[0].String(), "adminsubapp.go:13:1") {
		t.Errorf("message must name the declaration site, got %q", ms[0].String())
	}
}

func TestDiffUnexpectedRoute(t *testing.T) {
	actual := append(declared(), routetable.Route{Verb: "GET", Path: "/admin/secret"})
	ms := routetable.Diff(declared(), actual)
	if len(ms) != 1 || ms[0].Kind != routetable.KindUnexpected {
		t.Fatalf("expected one unexpected mismatch, got %v", ms)
	}
	if ms[0].Path != "/admin/secret" || ms[0].Declared != nil {
		t.Errorf("an unexpected route carries only the actual side, got %+v", ms[0])
	}
}

func TestDiffHandlerMismatch(t *testing.T) {
	actual := declared()
	actual[0].HandlerName = "ListAdmins"
	ms := routetable.Diff(declared(), actual)
	if len(ms) != 1 || ms[0].Kind != routetable.KindHandler {
		t.Fatalf("expected one handler mismatch, got %v", ms)
	}
	if !strings.Contains(ms[0].String(), "ListUsers") || !strings.Contains(ms[0].String(), "ListAdmins") {
		t.Errorf("message must name both handlers, got %q", ms[0].String())
	}
}

func TestDiffIgnoresUnreportedHandlers(t *testing.T) {
	// Most routers report paths, not Go symbols. An empty handler on the
	// actual side means "not reported", never "different" — otherwise
	// every route in a path-only dump would be a false mismatch.
	actual := declared()
	for i := range actual {
		actual[i].HandlerPkg, actual[i].HandlerName = "", ""
	}
	if ms := routetable.Diff(declared(), actual); len(ms) != 0 {
		t.Fatalf("a path-only dump must agree, got %v", ms)
	}
}

func TestDiffMatchesBareHandlerNames(t *testing.T) {
	// A router that knows the function but not its package still gets a
	// useful comparison.
	actual := declared()
	for i := range actual {
		actual[i].HandlerPkg = ""
	}
	if ms := routetable.Diff(declared(), actual); len(ms) != 0 {
		t.Fatalf("a bare-name dump must match on name alone, got %v", ms)
	}
	actual[0].HandlerName = "Other"
	if ms := routetable.Diff(declared(), actual); len(ms) != 1 || ms[0].Kind != routetable.KindHandler {
		t.Fatalf("a differing bare name must still mismatch, got %v", ms)
	}
}

func TestDiffIsSortedAndStable(t *testing.T) {
	d := []routetable.Route{
		{Verb: "GET", Path: "/z"},
		{Verb: "POST", Path: "/a"},
		{Verb: "GET", Path: "/a"},
	}
	ms := routetable.Diff(d, nil)
	if len(ms) != 3 {
		t.Fatalf("expected three mismatches, got %v", ms)
	}
	// Sorted by path then verb, so a failing test prints the same report
	// every run despite the map-based index.
	want := []string{"GET /a", "POST /a", "GET /z"}
	for i, w := range want {
		if got := ms[i].Verb + " " + ms[i].Path; got != w {
			t.Errorf("mismatch %d = %q, want %q", i, got, w)
		}
	}
}

func TestNormalizePathAcrossFlavours(t *testing.T) {
	tests := []struct {
		in     string
		style  routetable.ParamStyle
		want   string
		params []string
	}{
		{"/users/:id", routetable.ColonStyle, "/users/{id}", []string{"id"}},
		{"/files/*", routetable.ColonStyle, "/files/{rest...}", []string{"rest..."}},
		{"/users/{id}", routetable.BraceStyle, "/users/{id}", []string{"id"}},
		{"/files/{path...}", routetable.BraceStyle, "/files/{path...}", []string{"path..."}},
		{"/static", routetable.BraceStyle, "/static", nil},
	}
	for _, tt := range tests {
		got, params := routetable.NormalizePath(tt.in, tt.style)
		if got != tt.want {
			t.Errorf("NormalizePath(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if len(params) != len(tt.params) {
			t.Errorf("NormalizePath(%q) params = %v, want %v", tt.in, params, tt.params)
			continue
		}
		for i := range params {
			if params[i] != tt.params[i] {
				t.Errorf("NormalizePath(%q) params = %v, want %v", tt.in, params, tt.params)
			}
		}
	}
}

func TestNormalizeBridgesRouterSyntax(t *testing.T) {
	// The consumer's own router reports colon-style paths; normalizing
	// them is what makes the comparison meaningful, and it uses the
	// toolchain's normalizer rather than a copy that could drift.
	d := []routetable.Route{{Verb: "GET", Path: "/users/{id}"}}
	fromRouter := []routetable.Route{{Verb: "GET", Path: "/users/:id"}}

	if ms := routetable.Diff(d, fromRouter); len(ms) == 0 {
		t.Fatal("un-normalized colon paths must not silently match")
	}
	normalized := routetable.Normalize(fromRouter, routetable.ColonStyle)
	if ms := routetable.Diff(d, normalized); len(ms) != 0 {
		t.Fatalf("normalized paths must match, got %v", ms)
	}
	if normalized[0].OriginalPath != "/users/:id" {
		t.Errorf("Normalize must keep the input pattern, got %q", normalized[0].OriginalPath)
	}
	if fromRouter[0].Path != "/users/:id" {
		t.Error("Normalize must not mutate its input")
	}
}

func TestReportRendersEveryMismatch(t *testing.T) {
	actual := append(declared()[:1], routetable.Route{Verb: "GET", Path: "/legacy"})
	report := routetable.Report(routetable.Diff(declared(), actual))
	for _, want := range []string{"/legacy", "/admin/users", "does not serve", "no ghtmx route declares"} {
		if !strings.Contains(report, want) {
			t.Errorf("report is missing %q:\n%s", want, report)
		}
	}
	if lines := strings.Count(report, "\n"); lines != 2 {
		t.Errorf("report must be one newline-terminated line per mismatch, got %d:\n%s", lines, report)
	}
	// An unrecognised kind still renders rather than vanishing.
	unknown := routetable.Mismatch{Kind: "wat", Verb: "GET", Path: "/x"}
	if got := unknown.String(); !strings.Contains(got, "wat") || !strings.Contains(got, "/x") {
		t.Errorf("an unknown kind must still render, got %q", got)
	}
}

func TestFromTableOfNothing(t *testing.T) {
	// A nil table is what a caller gets when discovery could not run;
	// returning an empty slice keeps `for range` callers and JSON
	// encoders from special-casing it.
	got := routetable.FromTable(nil)
	if got == nil || len(got) != 0 {
		t.Fatalf("FromTable(nil) = %#v, want an empty non-nil slice", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != "[]" {
		t.Errorf("an empty table must encode as [], got %s", encoded)
	}
}

func TestDiffKeepsTheFirstOfDuplicates(t *testing.T) {
	// A table cannot hold verb+path duplicates, but a hand-written dump
	// can; the report must still name one stable side rather than
	// varying with map order.
	dup := []routetable.Route{
		{Verb: "GET", Path: "/x", HandlerName: "First"},
		{Verb: "GET", Path: "/x", HandlerName: "Second"},
	}
	ms := routetable.Diff(dup, nil)
	if len(ms) != 1 {
		t.Fatalf("duplicates must collapse to one mismatch, got %v", ms)
	}
	if ms[0].Declared.HandlerName != "First" {
		t.Errorf("the first duplicate must win, got %q", ms[0].Declared.HandlerName)
	}
}

func TestLoadRejectsAnUnreadableProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ghtmx.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := routetable.Load(dir); err == nil {
		t.Error("an unparseable ghtmx.json must fail the load")
	}
}

func TestRouteHandlerAndKey(t *testing.T) {
	r := routetable.Route{Verb: "GET", Path: "/x", HandlerPkg: "example.com/app", HandlerName: "H"}
	if got := r.Handler(); got != "example.com/app.H" {
		t.Errorf("Handler() = %q", got)
	}
	if got := (routetable.Route{HandlerName: "H"}).Handler(); got != "H" {
		t.Errorf("a package-less handler renders bare, got %q", got)
	}
	if got := r.Key(); got != "GET /x" {
		t.Errorf("Key() = %q", got)
	}
}

// TestLoadReadsAModule exercises the whole path a consumer's test takes:
// a real module on disk, its ghtmx.json honoured, out to public Routes.
func TestLoadReadsAModule(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/app\n\ngo 1.25\n",
		"ghtmx.json": `{"routeScope": ["./..."]}
`,
		"routes.go": `package app

import "net/http"

//ghtmx:route GET /admin/audit AuditLog nav

func AuditLog(w http.ResponseWriter, r *http.Request) {}

func ListUsers(w http.ResponseWriter, r *http.Request) {}

func routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /users/{id}", ListUsers)
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := routetable.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected two routes, got %+v", got)
	}
	// Sorted by path: the annotation first, then the discovered route.
	if got[0].Path != "/admin/audit" || got[0].Origin != "declared" || !got[0].NavOnly {
		t.Errorf("annotated route = %+v", got[0])
	}
	if got[1].Path != "/users/{id}" || got[1].Recognizer != "nethttp" {
		t.Errorf("discovered route = %+v", got[1])
	}
	if len(got[1].Params) != 1 || got[1].Params[0] != "id" {
		t.Errorf("params = %v", got[1].Params)
	}
	if got[1].Handler() != "example.com/app.ListUsers" {
		t.Errorf("handler = %q", got[1].Handler())
	}
	if !strings.Contains(got[1].Source, "routes.go") {
		t.Errorf("source = %q, want the registration site", got[1].Source)
	}

	// The JSON shape is the CLI's: `ghtmx routes -json` output round-trips
	// into the same values, which is what lets a consumer pipe one into
	// the other.
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var back []routetable.Route
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	if ms := routetable.Diff(got, back); len(ms) != 0 {
		t.Errorf("a round trip through JSON must be lossless, got %v", ms)
	}
}
