package routes

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/go-monolith/ghtmx/internal/diag"
)

// TestLoadIsSyntaxOnly proves the A3.1 bootstrap rule: a module with a
// deliberate type error (simulating missing generated files) still loads
// and yields a complete route table (FR-010), and a file that fails to
// parse degrades gracefully rather than aborting the run (FR-055).
func TestLoadIsSyntaxOnly(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/fx\n\ngo 1.25\n")
	write("main.go", `package main

import (
	"net/http"

	"example.com/fx/handlers"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /users/{id}", handlers.GetUser)
	mux.HandleFunc("POST /users", handlers.CreateUser)
}
`)
	// The handlers package contains a deliberate type error AND references
	// a generated symbol that does not exist — exactly the clean-checkout
	// state route discovery must survive.
	write("handlers/handlers.go", `package handlers

import "net/http"

var missingGenerated = UserPage // undefined: generated code not written yet

func GetUser(w http.ResponseWriter, r *http.Request) {
	var s string = 42 // deliberate type error
	_ = s
}

func CreateUser(w http.ResponseWriter, r *http.Request) {}
`)
	// A package that does not even parse: reported, not fatal.
	write("broken/broken.go", "package broken\n\nfunc {{{\n")

	sink := diag.NewSink(nil)
	pkgs, err := Load(dir, []string{"./..."}, sink)
	if err != nil {
		t.Fatalf("Load must not fail on type errors: %v", err)
	}
	table := Discover(pkgs, sink)

	if _, ok := table.Lookup(GET, SymbolRef{"example.com/fx/handlers", "GetUser"}); !ok {
		t.Errorf("route table incomplete despite type errors: %+v", table.All())
	}
	if _, ok := table.Lookup(POST, SymbolRef{"example.com/fx/handlers", "CreateUser"}); !ok {
		t.Errorf("route table missing POST /users")
	}
	// The unparseable package produced a diagnostic but did not abort.
	var sawParseDiag bool
	for _, d := range sink.Diagnostics() {
		if d.ID == diag.UnresolvableRoute {
			sawParseDiag = true
		}
	}
	if !sawParseDiag {
		t.Error("expected a diagnostic for the unparseable package")
	}
}
