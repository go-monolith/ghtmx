package routescmd

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/routes"
	"github.com/google/go-cmp/cmp"
)

// `ghtmx routes` exists to answer "why did this binding not resolve"
// (FR-064), so what matters is that the two renderings tell the truth
// about the table: the wildcard verb reads as *, escape-hatch
// declarations are visibly distinct from discovered routes, and the
// JSON form carries the fields a tool would key on.

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// sampleTable builds a table covering the cases the renderers branch on:
// an ordinary verb, the any-verb wildcard, a declared route, and both
// parameter kinds.
func sampleTable(t *testing.T) *routes.Table {
	t.Helper()
	table := routes.NewTable()
	for _, r := range []routes.Route{
		{
			Verb:         "GET",
			Path:         "/users/{id}",
			OriginalPath: "/users/:id",
			Params:       []routes.RouteParam{{Name: "id"}},
			Handler:      routes.SymbolRef{PkgPath: "example.com/app", Name: "ShowUser"},
			Pos:          routes.Position{File: "main.go", Line: 12, Col: 3},
			Origin:       routes.Discovered,
			Recognizer:   "chi",
		},
		{
			Verb:         routes.AnyVerb,
			Path:         "/files/{rest...}",
			OriginalPath: "/files/*",
			Params:       []routes.RouteParam{{Name: "rest", Wildcard: true}},
			Handler:      routes.SymbolRef{PkgPath: "example.com/app", Name: "ServeFiles"},
			Pos:          routes.Position{File: "main.go", Line: 20, Col: 3},
			Origin:       routes.Discovered,
			Recognizer:   "nethttp",
		},
		{
			Verb:         "POST",
			Path:         "/webhook",
			OriginalPath: "/webhook",
			Handler:      routes.SymbolRef{PkgPath: "example.com/app", Name: "Webhook"},
			Pos:          routes.Position{File: "hooks.go", Line: 5, Col: 1},
			Origin:       routes.Declared,
			Recognizer:   "annotation",
		},
	} {
		if _, ok := table.Add(r); !ok {
			t.Fatalf("duplicate route in the fixture: %s %s", r.Verb, r.Path)
		}
	}
	return table
}

func TestWriteText(t *testing.T) {
	var buf bytes.Buffer
	if err := writeText(&buf, sampleTable(t)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	for _, want := range []string{
		"VERB", "PATH", "HANDLER", "ORIGIN", "RECOGNIZER", "SOURCE",
		"GET", "/users/{id}", "ShowUser", "chi", "main.go:12:3",
		"POST", "/webhook", "Webhook",
		"/files/{rest...}", "ServeFiles",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output is missing %q:\n%s", want, got)
		}
	}
}

// TestWriteTextRendersTheAnyVerbAsAStar pins the substitution: an empty
// verb column would read as "this row is broken" rather than "this route
// matches every method".
func TestWriteTextRendersTheAnyVerbAsAStar(t *testing.T) {
	table := routes.NewTable()
	table.Add(routes.Route{
		Verb:       routes.AnyVerb,
		Path:       "/any",
		Handler:    routes.SymbolRef{Name: "H"},
		Origin:     routes.Discovered,
		Recognizer: "nethttp",
	})

	var buf bytes.Buffer
	if err := writeText(&buf, table); err != nil {
		t.Fatal(err)
	}

	line := rowFor(t, buf.String(), "/any")
	if !strings.HasPrefix(strings.TrimSpace(line), "*") {
		t.Errorf("the any-verb row does not start with *: %q", line)
	}
}

// TestWriteTextMarksDeclaredRoutes pins FR-064's distinct marking: a
// declared route behaves differently from a discovered one, so the
// output has to say which it is.
func TestWriteTextMarksDeclaredRoutes(t *testing.T) {
	var buf bytes.Buffer
	if err := writeText(&buf, sampleTable(t)); err != nil {
		t.Fatal(err)
	}
	got := buf.String()

	if !strings.Contains(got, "declared (//ghtmx:route)") {
		t.Errorf("declared routes are not distinctly marked:\n%s", got)
	}
	discovered := rowFor(t, got, "/users/{id}")
	if strings.Contains(discovered, "declared") {
		t.Errorf("a discovered route is marked as declared: %q", discovered)
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := writeJSON(&buf, sampleTable(t)); err != nil {
		t.Fatal(err)
	}

	var got []jsonRoute
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
	}

	// All() sorts by path, then verb.
	want := []jsonRoute{
		{
			Verb: "*", Path: "/files/{rest...}", OriginalPath: "/files/*",
			Params:     []string{"rest..."},
			HandlerPkg: "example.com/app", HandlerName: "ServeFiles",
			Origin: "discovered", Recognizer: "nethttp", Source: "main.go:20:3",
		},
		{
			Verb: "GET", Path: "/users/{id}", OriginalPath: "/users/:id",
			Params:     []string{"id"},
			HandlerPkg: "example.com/app", HandlerName: "ShowUser",
			Origin: "discovered", Recognizer: "chi", Source: "main.go:12:3",
		},
		{
			Verb: "POST", Path: "/webhook", OriginalPath: "/webhook",
			HandlerPkg: "example.com/app", HandlerName: "Webhook",
			Origin: "declared", Recognizer: "annotation", Source: "hooks.go:5:1",
		},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("JSON output mismatch (-want +got):\n%s", diff)
	}
}

// TestWriteJSONEmitsAnArrayWhenEmpty pins that a project with no routes
// produces `[]`, not `null` — a consumer that ranges over the result
// should not have to special-case the empty case.
func TestWriteJSONEmitsAnArrayWhenEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := writeJSON(&buf, routes.NewTable()); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("empty table rendered as %q, want []", got)
	}
}

// failAfter fails every write once the budget is spent, which is how the
// renderers' error returns are reached: neither can fail against a
// bytes.Buffer.
type failAfter struct {
	remaining int
	err       error
}

func (w *failAfter) Write(p []byte) (int, error) {
	if w.remaining <= 0 {
		return 0, w.err
	}
	w.remaining--
	return len(p), nil
}

// countingWriter records how many Write calls a renderer makes, which is
// what the failure sweep needs to know to be exhaustive rather than
// arbitrary.
type countingWriter struct{ n int }

func (w *countingWriter) Write(p []byte) (int, error) { w.n++; return len(p), nil }

func TestRenderersReportWriteFailures(t *testing.T) {
	sentinel := io.ErrShortWrite
	tests := []struct {
		name  string
		write func(io.Writer, *routes.Table) error
	}{
		{"text", writeText},
		{"json", writeJSON},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Discover how many writes this renderer makes, then fail
			// each one in turn. Every write site gets a turn, and none
			// is missed — a swallowed error at any of them is a
			// silently truncated route listing that reads as "no such
			// route".
			var counter countingWriter
			if err := tt.write(&counter, sampleTable(t)); err != nil {
				t.Fatalf("baseline render failed: %v", err)
			}
			if counter.n == 0 {
				t.Fatal("the renderer made no writes; the sweep would assert nothing")
			}
			for budget := range counter.n {
				w := &failAfter{remaining: budget, err: sentinel}
				if err := tt.write(w, sampleTable(t)); err == nil {
					t.Errorf("write %d of %d failed but the error was swallowed", budget+1, counter.n)
				}
			}
		})
	}
}

// TestRunReportsAConfigFailure pins that Run surfaces a broken project
// rather than printing an empty table, which would read as "no routes
// found" and send someone hunting for a registration bug that is not
// there.
func TestRunReportsAConfigFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ghtmx.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout bytes.Buffer
	err := Run(discardLog(), &stdout, Arguments{Dir: dir})
	if err == nil {
		t.Fatal("Run succeeded against an unparseable ghtmx.json, want an error")
	}
	if stdout.Len() != 0 {
		t.Errorf("Run wrote a table despite failing: %q", stdout.String())
	}
}

// TestRunOnAModuleWithNoRoutes pins the ordinary empty case end to end,
// including the default-to-working-directory branch.
func TestRunOnAModuleWithNoRoutes(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args Arguments
		want string
	}{
		{"text", Arguments{Dir: dir}, "VERB"},
		{"json", Arguments{Dir: dir, JSON: true}, "[]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout bytes.Buffer
			if err := Run(discardLog(), &stdout, tt.args); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !strings.Contains(stdout.String(), tt.want) {
				t.Errorf("output %q does not contain %q", stdout.String(), tt.want)
			}
		})
	}
}

// TestRunDefaultsToTheWorkingDirectory covers the empty-Dir branch,
// which is what the CLI actually uses.
func TestRunDefaultsToTheWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/empty\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	var stdout bytes.Buffer
	if err := Run(discardLog(), &stdout, Arguments{}); err != nil {
		t.Fatalf("Run with no Dir: %v", err)
	}
	if !strings.Contains(stdout.String(), "VERB") {
		t.Errorf("output %q does not look like a route table", stdout.String())
	}
}

// writeModule scaffolds a minimal module, optionally with a ghtmx.json.
func writeModule(t *testing.T, source, ghtmxJSON string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":  "module example.com/app\n\ngo 1.25\n",
		"main.go": source,
	}
	if ghtmxJSON != "" {
		files["ghtmx.json"] = ghtmxJSON
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// malformedAnnotation is a //ghtmx:route escape hatch the parser cannot
// read, which is the cheapest way to make route discovery emit a
// diagnostic carrying both a message and a suggestion.
const malformedAnnotation = `package main

//ghtmx:route NOT-A-VALID-ANNOTATION
func Handler() {}

func main() {}
`

// TestRunReportsDiagnostics covers the reporting loop. `ghtmx routes` is
// a debugging command, so the diagnostics explaining why a route is
// missing are the output that matters most — dropping them would leave a
// user staring at an empty table with no reason given.
//
// Only the error branch is exercised here because every diagnostic route
// discovery can emit (GHTMX-E0401/0402/0403) is error-level and refuses
// demotion to a warning, so the warn branch is unreachable from this
// command's inputs. It will start being covered the day a warning-level
// route check exists.
func TestRunReportsDiagnostics(t *testing.T) {
	dir := writeModule(t, malformedAnnotation, "")

	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, nil))
	var stdout bytes.Buffer

	// An error diagnostic must also fail the command: a route table
	// built from unreadable input is not one anyone should act on.
	if err := Run(log, &stdout, Arguments{Dir: dir}); err == nil {
		t.Error("Run succeeded despite an error diagnostic, want a failure")
	}

	out := logged.String()
	if !strings.Contains(out, "ERROR") {
		t.Errorf("no error diagnostic was logged:\n%s", out)
	}
	// The id and position are what make a diagnostic actionable, and
	// the suggestion is what makes it fixable.
	for _, want := range []string{"GHTMX-E0403", "main.go", "suggest"} {
		if !strings.Contains(out, want) {
			t.Errorf("the logged diagnostic is missing %q:\n%s", want, out)
		}
	}
}

// rowFor returns the output line containing the given path.
func rowFor(t *testing.T, out, path string) string {
	t.Helper()
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Contains(line, path) {
			return line
		}
	}
	t.Fatalf("no row for %q in:\n%s", path, out)
	return ""
}
