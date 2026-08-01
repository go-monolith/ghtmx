// Package catalogcheck keeps DIAGNOSTICS.md and CONFIG.md derived from
// the in-repo sources so they cannot drift (FR-045, FR-071): every
// diagnostic ID in internal/diag/ids.go must be catalogued (and vice
// versa), every config setting's JSON tag documented, and every
// `ghtmx generate` flag registration documented.
package catalogcheck

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the catalogcheck source file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func read(t *testing.T, parts ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{moduleRoot(t)}, parts...)...))
	if err != nil {
		t.Fatalf("required file missing: %v", err)
	}
	return string(raw)
}

// TestEveryDiagnosticIsCatalogued: the ID registry and the catalogue
// must agree in both directions.
func TestEveryDiagnosticIsCatalogued(t *testing.T) {
	idPattern := regexp.MustCompile(`GHTMX-[EW]\d{4}`)

	registry := map[string]bool{}
	for _, id := range idPattern.FindAllString(read(t, "internal", "diag", "ids.go"), -1) {
		registry[id] = true
	}
	if len(registry) < 20 {
		t.Fatalf("only %d diagnostic IDs found in ids.go — the registry moved?", len(registry))
	}

	catalogue := map[string]bool{}
	for _, id := range idPattern.FindAllString(read(t, "DIAGNOSTICS.md"), -1) {
		catalogue[id] = true
	}

	// A registry ID needs a cause/remedy TABLE ROW, not just a prose
	// mention elsewhere in the document.
	rows := map[string]bool{}
	rowPattern := regexp.MustCompile("(?m)^\\| `(GHTMX-[EW]\\d{4})`")
	for _, match := range rowPattern.FindAllStringSubmatch(read(t, "DIAGNOSTICS.md"), -1) {
		rows[match[1]] = true
	}
	for id := range registry {
		if !rows[id] {
			t.Errorf("diagnostic %s has no catalogue row in DIAGNOSTICS.md — every emitted ID needs cause and remedy", id)
		}
	}
	for id := range catalogue {
		if !registry[id] {
			t.Errorf("DIAGNOSTICS.md documents %s, which internal/diag/ids.go does not define", id)
		}
	}
}

// TestEveryConfigSettingIsDocumented: each JSON tag in the config
// schema must appear in CONFIG.md.
func TestEveryConfigSettingIsDocumented(t *testing.T) {
	doc := read(t, "CONFIG.md")
	// [,"] keeps a future `json:"x,omitempty"` tag from silently
	// dropping out of the check.
	tagPattern := regexp.MustCompile(`json:"([a-zA-Z]+)[,"]`)
	tags := tagPattern.FindAllStringSubmatch(read(t, "internal", "config", "config.go"), -1)
	if len(tags) < 7 {
		t.Fatalf("only %d config settings found — the schema moved?", len(tags))
	}
	for _, match := range tags {
		setting := match[1]
		needle := "`" + setting
		if setting == "dir" || setting == "name" {
			// Nested under generatedPackage: require the qualified form
			// so a stray substring cannot satisfy the check.
			needle = "generatedPackage." + setting
		}
		if !strings.Contains(doc, needle) {
			t.Errorf("config setting %q is not documented in CONFIG.md", setting)
		}
	}
	for _, needle := range []string{"CLI flag >", "default", "ghtmx.json"} {
		if !strings.Contains(doc, needle) {
			t.Errorf("CONFIG.md must document precedence: missing %q", needle)
		}
	}
}

// TestEveryGenerateFlagIsDocumented: each flag the generate command
// registers must appear in CONFIG.md.
func TestEveryGenerateFlagIsDocumented(t *testing.T) {
	doc := read(t, "CONFIG.md")
	source := read(t, "cmd", "ghtmx", "generatecmd", "main.go")
	// The flag NAME is the first argument for the value-returning forms
	// and the second for the *Var forms; one loose pattern would capture
	// default values instead.
	patterns := []*regexp.Regexp{
		// Any value-returning registration (String, Bool, Float64, ...):
		// the name is the first argument.
		regexp.MustCompile(`cmd\.[A-Z][a-zA-Z0-9]*\("([a-zA-Z-]+)"`),
		// Any *Var registration: the name is the second argument.
		regexp.MustCompile(`cmd\.[A-Z][a-zA-Z0-9]*Var\(&[^,]+, "([a-zA-Z-]+)"`),
		regexp.MustCompile(`cmd\.Var\([^,]+, "([a-zA-Z-]+)"`),
	}
	var flags [][]string
	for _, pattern := range patterns {
		flags = append(flags, pattern.FindAllStringSubmatch(source, -1)...)
	}
	if len(flags) < 25 {
		t.Fatalf("only %d generate flags found — the registration moved?", len(flags))
	}
	for _, match := range flags {
		name := match[1]
		if !strings.Contains(doc, "`-"+name+"`") {
			t.Errorf("generate flag -%s is not documented in CONFIG.md", name)
		}
	}
}

// TestD9MappingPresent: the compile-error mapping (D9 enforcement) must
// keep its load-bearing rows.
func TestD9MappingPresent(t *testing.T) {
	doc := read(t, "DIAGNOSTICS.md")
	for _, needle := range []string{
		"ghtmxgen.EmitFoo",
		"blank-identifier",
		"ghtmxFragmentBody_",
		"redeclared",
	} {
		if !strings.Contains(doc, needle) {
			t.Errorf("the D9 compile-error mapping lost its %q row", needle)
		}
	}
}
