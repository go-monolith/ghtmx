package crud

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd"
)

func reset() { todos = newStore() }

func serve(t *testing.T) *httptest.Server {
	t.Helper()
	reset()
	srv := httptest.NewServer(Routes())
	t.Cleanup(srv.Close)
	return srv
}

func do(t *testing.T, srv *httptest.Server, method, path string, form url.Values, htmx bool) (*http.Response, string) {
	t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequest(method, srv.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, string(data)
}

// TestFullPageServesEverything: the page carries the pinned htmx
// script, the stats fragment, the bound form, and the list.
func TestFullPageServesEverything(t *testing.T) {
	srv := serve(t)
	resp, body := do(t, srv, http.MethodGet, "/", nil, false)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for _, want := range []string{
		`<script src="https://cdn.jsdelivr.net/npm/htmx.org@`,
		`0 of 0 done`,
		`hx-post="/todos"`,
		`hx-trigger="todo-created from:body, todo-toggled from:body, todo-deleted from:body"`,
		`<table id="todo-list">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page must contain %q", want)
		}
	}
}

// TestCreateReturnsListAndEmitsEvent: FR-037 — the creation announces
// itself through the generated emitter only.
func TestCreateReturnsListAndEmitsEvent(t *testing.T) {
	srv := serve(t)
	resp, body := do(t, srv, http.MethodPost, "/todos", url.Values{"title": {"Ship it"}}, true)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got, want := resp.Header.Get("HX-Trigger"), `{"todo-created":{"id":"1"}}`; got != want {
		t.Errorf("HX-Trigger = %q, want %q", got, want)
	}
	if !strings.Contains(body, `<tr id="todo-1">`) || !strings.Contains(body, "Ship it") {
		t.Errorf("response must be the refreshed list, got %q", body)
	}
	if strings.Contains(body, "<html>") {
		t.Errorf("creation must return the bare fragment, got %q", body)
	}
}

// TestToggleUpdatesTheRow: the row fragment refreshes standalone with
// the toggled state and the typed event payload.
func TestToggleUpdatesTheRow(t *testing.T) {
	srv := serve(t)
	do(t, srv, http.MethodPost, "/todos", url.Values{"title": {"Flip me"}}, true)
	resp, body := do(t, srv, http.MethodPut, "/todos/1", nil, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got, want := resp.Header.Get("HX-Trigger"), `{"todo-toggled":{"id":"1","done":true}}`; got != want {
		t.Errorf("HX-Trigger = %q, want %q", got, want)
	}
	if !strings.Contains(body, `class="done"`) || !strings.Contains(body, `<tr id="todo-1">`) {
		t.Errorf("row must render toggled, got %q", body)
	}
}

// TestRenameFlow: the full inline-edit round trip — the edit fragment
// carries the current title in its form, and saving swaps back a row
// with the new one.
func TestRenameFlow(t *testing.T) {
	srv := serve(t)
	do(t, srv, http.MethodPost, "/todos", url.Values{"title": {"Old name"}}, true)

	_, editRow := do(t, srv, http.MethodGet, "/todos/1/edit", nil, true)
	if !strings.Contains(editRow, `value="Old name"`) || !strings.Contains(editRow, `hx-put="/todos/1/title"`) {
		t.Fatalf("edit fragment must carry the title and the bound save URL, got %q", editRow)
	}
	resp, row := do(t, srv, http.MethodPut, "/todos/1/title", url.Values{"title": {"New name"}}, true)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if !strings.Contains(row, "New name") || !strings.Contains(row, `<tr id="todo-1">`) {
		t.Errorf("saving must swap back the renamed row, got %q", row)
	}
}

// TestDeleteRemovesTheRow: an empty 200 lets the outerHTML swap remove
// the row; the event still fires.
func TestDeleteRemovesTheRow(t *testing.T) {
	srv := serve(t)
	do(t, srv, http.MethodPost, "/todos", url.Values{"title": {"Doomed"}}, true)
	resp, body := do(t, srv, http.MethodDelete, "/todos/1", nil, true)
	if resp.StatusCode != http.StatusOK || body != "" {
		t.Fatalf("delete must return an empty 200, got %d %q", resp.StatusCode, body)
	}
	if got, want := resp.Header.Get("HX-Trigger"), `{"todo-deleted":{"id":"1"}}`; got != want {
		t.Errorf("HX-Trigger = %q, want %q", got, want)
	}
	_, page := do(t, srv, http.MethodGet, "/", nil, false)
	if strings.Contains(page, "Doomed") {
		t.Error("the deleted todo must be gone from the page")
	}
}

// TestListModeSelection: FR-035 — the same route serves the full page
// to a browser and the bare fragment to htmx, byte-identically to the
// page's embedded list.
func TestListModeSelection(t *testing.T) {
	srv := serve(t)
	do(t, srv, http.MethodPost, "/todos", url.Values{"title": {"Alpha"}}, true)

	_, full := do(t, srv, http.MethodGet, "/todos", nil, false)
	if !strings.Contains(full, "<html>") || !strings.Contains(full, "Alpha") {
		t.Errorf("a plain request must get the full page, got %q", full)
	}
	_, fragment := do(t, srv, http.MethodGet, "/todos", nil, true)
	if strings.Contains(fragment, "<html>") || !strings.HasPrefix(fragment, `<table id="todo-list">`) {
		t.Errorf("an htmx request must get the bare list, got %q", fragment)
	}
	if !strings.Contains(full, fragment) {
		t.Error("the standalone list must be byte-identical to the page's embedded list")
	}
}

// TestBoundURLsComeFromBindings: the rendered attributes carry the
// discovered routes — /todos folded statically for the symbol binding,
// the row URLs built by the escaping constructors.
func TestBoundURLsComeFromBindings(t *testing.T) {
	srv := serve(t)
	do(t, srv, http.MethodPost, "/todos", url.Values{"title": {"Bound"}}, true)
	_, page := do(t, srv, http.MethodGet, "/", nil, false)
	for _, want := range []string{
		`hx-post="/todos"`,
		`hx-get="/todos"`,
		`hx-get="/todos/1/edit"`,
		`hx-put="/todos/1"`,
		`hx-delete="/todos/1"`,
		`hx-get="/todos/stats"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("page must contain the bound URL %q", want)
		}
	}
}

// TestZeroHandWrittenGlue: the acceptance guarantee, self-enforced —
// no constant hx-verb URLs in the template, no hand-written HX-Trigger
// or htmx script tag in the Go source.
func TestZeroHandWrittenGlue(t *testing.T) {
	template, err := os.ReadFile("crud.ghtmx")
	if err != nil {
		t.Fatal(err)
	}
	// data-hx-* is honored by htmx too: a hand-written URL must not
	// sneak through the prefixed spelling either.
	if constantURL := regexp.MustCompile(`(data-)?hx-(get|post|put|patch|delete)="`); constantURL.Match(template) {
		t.Error("the template hand-writes an hx-verb URL; every URL must come from a binding")
	}
	source, err := os.ReadFile("crud.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{string(source), string(template)} {
		for _, forbidden := range []string{`"HX-Trigger`, `htmx.org`, `<script`} {
			if strings.Contains(file, forbidden) {
				t.Errorf("the app hand-writes %q; that glue must come from the generated helpers", forbidden)
			}
		}
	}
}

// TestRouteChangesBreakTheBuild: the acceptance criterion — deleting or
// renaming a route breaks the build at every binding site. The app is
// copied into a scratch module, verified to generate and build, then
// mutated.
func TestRouteChangesBreakTheBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a scratch module")
	}
	moduleRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	setup := func(t *testing.T, mutate func(main string) string) string {
		t.Helper()
		dir := t.TempDir()
		for _, name := range []string{"crud.go", "crud.ghtmx"} {
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			content := string(data)
			content = strings.ReplaceAll(content, "github.com/go-monolith/ghtmx/ghtmxgen", "example.com/crud/ghtmxgen")
			if name == "crud.go" && mutate != nil {
				content = mutate(content)
			}
			if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		gomod := "module example.com/crud\n\ngo 1.25.0\n\nrequire github.com/go-monolith/ghtmx v0.0.0\n\nreplace github.com/go-monolith/ghtmx => " + moduleRoot + "\n"
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(gomod), 0o644); err != nil {
			t.Fatal(err)
		}
		// The central package must exist as an in-module directory
		// before the first tidy, or tidy tries to resolve the import
		// remotely; generation then fills it with the real files.
		if err := os.MkdirAll(filepath.Join(dir, "ghtmxgen"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "ghtmxgen", "stub.go"), []byte("package ghtmxgen\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	tidy := func(t *testing.T, dir string) {
		t.Helper()
		cmd := exec.Command("go", "mod", "tidy")
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go mod tidy: %v\n%s", err, out)
		}
	}
	generate := func(dir string) (string, error) {
		var diagnostics strings.Builder
		err := generatecmd.Run(context.Background(), io.Discard, &diagnostics,
			[]string{"-path", dir, "-include-version=false"})
		return diagnostics.String(), err
	}
	build := func(t *testing.T, dir string) (string, error) {
		cmd := exec.Command("go", "build", "-o", filepath.Join(t.TempDir(), "app"), ".")
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	t.Run("baseline generates and builds", func(t *testing.T) {
		dir := setup(t, nil)
		tidy(t, dir)
		if diagnostics, err := generate(dir); err != nil {
			t.Fatalf("baseline generation failed: %v\n%s", err, diagnostics)
		}
		tidy(t, dir)
		if out, err := build(t, dir); err != nil {
			t.Fatalf("baseline build failed: %v\n%s", err, out)
		}
	})

	t.Run("deleting a route fails generation at the binding site", func(t *testing.T) {
		dir := setup(t, func(main string) string {
			return strings.ReplaceAll(main, `mux.HandleFunc("PUT /todos/{id}", ToggleTodo)`, "")
		})
		tidy(t, dir)
		diagnostics, err := generate(dir)
		if err == nil {
			t.Fatal("generation must fail when a bound route disappears")
		}
		if !strings.Contains(diagnostics, "GHTMX-E0101") || !strings.Contains(diagnostics, "ToggleTodo") {
			t.Fatalf("the failure must name the binding's diagnostic and symbol:\n%s", diagnostics)
		}
	})

	t.Run("renaming a handler fails generation at the binding site", func(t *testing.T) {
		dir := setup(t, func(main string) string {
			// The handler and its registration rename together — only
			// the template still says CreateTodo.
			main = strings.ReplaceAll(main, "func CreateTodo(", "func MakeTodo(")
			return strings.ReplaceAll(main, `mux.HandleFunc("POST /todos", CreateTodo)`, `mux.HandleFunc("POST /todos", MakeTodo)`)
		})
		tidy(t, dir)
		diagnostics, err := generate(dir)
		if err == nil {
			t.Fatal("generation must fail when a bound handler is renamed")
		}
		if !strings.Contains(diagnostics, "GHTMX-E0101") || !strings.Contains(diagnostics, "CreateTodo") {
			t.Fatalf("the failure must name the binding's diagnostic and symbol:\n%s", diagnostics)
		}
	})

	t.Run("renaming a handler alone breaks the committed build", func(t *testing.T) {
		// Even without regenerating, the generated blank-identifier
		// references pin the symbol: the build breaks immediately.
		dir := setup(t, nil)
		tidy(t, dir)
		if diagnostics, err := generate(dir); err != nil {
			t.Fatalf("%v\n%s", err, diagnostics)
		}
		tidy(t, dir)
		data, err := os.ReadFile(filepath.Join(dir, "crud.go"))
		if err != nil {
			t.Fatal(err)
		}
		mutated := strings.ReplaceAll(string(data), "func CreateTodo(", "func MakeTodo(")
		mutated = strings.ReplaceAll(mutated, `mux.HandleFunc("POST /todos", CreateTodo)`, `mux.HandleFunc("POST /todos", MakeTodo)`)
		if err := os.WriteFile(filepath.Join(dir, "crud.go"), []byte(mutated), 0o644); err != nil {
			t.Fatal(err)
		}
		out, err := build(t, dir)
		if err == nil {
			t.Fatal("the stale generated binding reference must break the build")
		}
		if !strings.Contains(out, "CreateTodo") {
			t.Errorf("the build error must name the binding site's symbol:\n%s", out)
		}
	})
}
