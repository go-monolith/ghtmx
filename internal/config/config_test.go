package config

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/diag"
	"github.com/go-monolith/ghtmx/internal/htmxsurface"
)

func TestMissingFileYieldsDefaults(t *testing.T) {
	dir := t.TempDir()
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("a missing config file must not be an error, got %v", err)
	}
	want := Default()
	if cfg.HtmxVersion != want.HtmxVersion {
		t.Errorf("HtmxVersion = %q, want %q", cfg.HtmxVersion, want.HtmxVersion)
	}
	if cfg.GeneratedPackage != want.GeneratedPackage {
		t.Errorf("GeneratedPackage = %+v, want %+v", cfg.GeneratedPackage, want.GeneratedPackage)
	}
	if len(cfg.SourceDirs) != 1 || cfg.SourceDirs[0] != "." {
		t.Errorf("SourceDirs = %v, want [.]", cfg.SourceDirs)
	}
}

func TestValidFileOverridesDefaults(t *testing.T) {
	dir := t.TempDir()
	content := `{
	"htmxVersion": "2.0.0",
	"sourceDirs": ["ui", "pages"],
	"generatedPackage": {"dir": "gen", "name": "gen"},
	"checks": {"GHTMX-W0101": "off"},
	"strictTargets": true
}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HtmxVersion != "2.0.0" {
		t.Errorf("HtmxVersion = %q", cfg.HtmxVersion)
	}
	if len(cfg.SourceDirs) != 2 || cfg.SourceDirs[0] != "ui" {
		t.Errorf("SourceDirs = %v", cfg.SourceDirs)
	}
	overrides := cfg.SeverityOverrides()
	if overrides[diag.UnusedFragment] != diag.Off {
		t.Errorf("expected GHTMX-W0101 off, got %q", overrides[diag.UnusedFragment])
	}
	if overrides[diag.DanglingTarget] != diag.Error {
		t.Errorf("strictTargets must promote %s to error", diag.DanglingTarget)
	}
}

func TestUnknownKeyIsPositionedError(t *testing.T) {
	content := "{\n\t\"htmxVersion\": \"2.0.10\",\n\t\"htmxVerison\": \"oops\"\n}"
	_, err := Parse("ghtmx.json", []byte(content))
	if err == nil {
		t.Fatal("expected an error for the unknown key")
	}
	msg := err.Error()
	if !strings.Contains(msg, `"htmxVerison"`) {
		t.Errorf("error must name the offending key, got %q", msg)
	}
	if !strings.Contains(msg, "ghtmx.json:3:") {
		t.Errorf("error must carry a position on line 3, got %q", msg)
	}
}

func TestInvalidJSONIsPositionedError(t *testing.T) {
	content := "{\n\t\"htmxVersion\": 2.0.10\n}"
	_, err := Parse("ghtmx.json", []byte(content))
	if err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
	if !strings.Contains(err.Error(), "ghtmx.json:2:") {
		t.Errorf("error must carry a line-2 position, got %q", err.Error())
	}
}

func TestWrongValueTypeIsPositionedError(t *testing.T) {
	content := "{\n\t\"sourceDirs\": \"not-a-list\"\n}"
	_, err := Parse("ghtmx.json", []byte(content))
	if err == nil {
		t.Fatal("expected an error for the wrong value type")
	}
	if !strings.Contains(err.Error(), "sourceDirs") {
		t.Errorf("error must name the key, got %q", err.Error())
	}
}

func TestUnknownDiagnosticIDRejected(t *testing.T) {
	content := "{\n\t\"checks\": {\"GHTMX-X9999\": \"off\"}\n}"
	_, err := Parse("ghtmx.json", []byte(content))
	if err == nil || !strings.Contains(err.Error(), "GHTMX-X9999") {
		t.Fatalf("expected an error naming the unknown ID, got %v", err)
	}
}

func TestErrorChecksCannotBeDemoted(t *testing.T) {
	content := "{\n\t\"checks\": {\"GHTMX-E0101\": \"off\"}\n}"
	_, err := Parse("ghtmx.json", []byte(content))
	if err == nil || !strings.Contains(err.Error(), "cannot be demoted") {
		t.Fatalf("expected a demotion error, got %v", err)
	}
}

func TestFlagPrecedenceOverFile(t *testing.T) {
	fileCfg := Default()
	fileCfg.HtmxVersion = "2.0.0"
	fileCfg.StrictTargets = false

	v := "2.0.10"
	strict := true
	cfg := Resolve(fileCfg, Flags{
		HtmxVersion:   &v,
		SourceDirs:    []string{"web"},
		StrictTargets: &strict,
		CheckSeverities: map[string]diag.Severity{
			diag.UnboundRoute: diag.Off,
		},
	})
	if cfg.HtmxVersion != "2.0.10" {
		t.Errorf("flag must win over file: %q", cfg.HtmxVersion)
	}
	if len(cfg.SourceDirs) != 1 || cfg.SourceDirs[0] != "web" {
		t.Errorf("SourceDirs = %v", cfg.SourceDirs)
	}
	if !cfg.StrictTargets {
		t.Error("StrictTargets flag must win")
	}
	if cfg.Checks[diag.UnboundRoute] != diag.Off {
		t.Errorf("check severity flag must merge, got %v", cfg.Checks)
	}
}

func TestHashChangesWithContent(t *testing.T) {
	a := Default()
	b := Default()
	if a.Hash() != b.Hash() {
		t.Error("identical configs must hash identically")
	}
	b.HtmxVersion = "2.0.0"
	if a.Hash() == b.Hash() {
		t.Error("different configs must hash differently")
	}
}

// Regression tests for review findings.

func TestBOMIsStripped(t *testing.T) {
	content := append([]byte{0xEF, 0xBB, 0xBF}, []byte(`{"htmxVersion": "2.0.0"}`)...)
	cfg, err := Parse("ghtmx.json", content)
	if err != nil {
		t.Fatalf("a UTF-8 BOM must not break parsing: %v", err)
	}
	if cfg.HtmxVersion != "2.0.0" {
		t.Errorf("HtmxVersion = %q", cfg.HtmxVersion)
	}
}

func TestEmptyFileYieldsDefaults(t *testing.T) {
	for _, content := range []string{"", "  \n\t\n"} {
		cfg, err := Parse("ghtmx.json", []byte(content))
		if err != nil {
			t.Fatalf("empty content %q must behave like a missing file: %v", content, err)
		}
		if cfg.HtmxVersion != DefaultHtmxVersion {
			t.Errorf("expected defaults, got %+v", cfg)
		}
	}
}

func TestDuplicateKeysRejected(t *testing.T) {
	content := "{\n\t\"htmxVersion\": \"2.0.0\",\n\t\"htmxVersion\": \"2.0.10\"\n}"
	_, err := Parse("ghtmx.json", []byte(content))
	if err == nil || !strings.Contains(err.Error(), "duplicate configuration key") {
		t.Fatalf("expected a duplicate-key error, got %v", err)
	}
	if !strings.Contains(err.Error(), "ghtmx.json:3:") {
		t.Errorf("duplicate-key error must be positioned on line 3, got %q", err)
	}
}

func TestNestedUnknownKeyNamedWithPosition(t *testing.T) {
	content := "{\n\t\"generatedPackage\": {\"dir\": \"g\", \"name\": \"g\", \"bogus\": 1}\n}"
	_, err := Parse("ghtmx.json", []byte(content))
	if err == nil || !strings.Contains(err.Error(), `"bogus"`) {
		t.Fatalf("expected the nested unknown key to be named, got %v", err)
	}
	if !strings.Contains(err.Error(), "ghtmx.json:2:") {
		t.Errorf("nested unknown-key error must carry a position, got %q", err)
	}
}

func TestTrailingGarbageRejected(t *testing.T) {
	content := `{"htmxVersion": "2.0.10"} {"sourceDirs": 5}`
	_, err := Parse("ghtmx.json", []byte(content))
	if err == nil || !strings.Contains(err.Error(), "unexpected content") {
		t.Fatalf("expected a trailing-content error, got %v", err)
	}
}

func TestEmptyArraysRejected(t *testing.T) {
	for _, content := range []string{`{"sourceDirs": []}`, `{"routeScope": []}`} {
		if _, err := Parse("ghtmx.json", []byte(content)); err == nil {
			t.Errorf("expected %s to be rejected", content)
		}
	}
}

func TestValidateAfterResolve(t *testing.T) {
	empty := ""
	cfg := Resolve(Default(), Flags{HtmxVersion: &empty})
	if err := cfg.Validate(); err == nil {
		t.Error("an empty --htmx-version flag must fail validation")
	}
	bogus := Resolve(Default(), Flags{CheckSeverities: map[string]diag.Severity{"GHTMX-X9999": diag.Off}})
	if err := bogus.Validate(); err == nil {
		t.Error("an unknown check ID from flags must fail validation")
	}
}

func TestHashCanonicalizesNilChecks(t *testing.T) {
	a := Default()
	a.Checks = nil
	b := Default()
	b.Checks = map[string]diag.Severity{}
	if a.Hash() != b.Hash() {
		t.Error("nil and empty Checks must hash identically")
	}
}

func TestDefaultHtmxVersionIsEmbedded(t *testing.T) {
	// A version bump in either package must not silently break the other.
	if !slices.Contains(htmxsurface.SupportedVersions(), DefaultHtmxVersion) {
		t.Errorf("DefaultHtmxVersion %s is not in the embedded surface set %v", DefaultHtmxVersion, htmxsurface.SupportedVersions())
	}
}

func TestHtmxScriptKey(t *testing.T) {
	if !Default().EmitHtmxScript() {
		t.Error("EmitHtmxScript() must default to true")
	}
	cfg, err := Parse("ghtmx.json", []byte(`{"htmxScript": false}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.EmitHtmxScript() {
		t.Error("htmxScript: false must disable the helper")
	}
	cfg, err = Parse("ghtmx.json", []byte(`{"htmxScript": true}`))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.EmitHtmxScript() {
		t.Error("htmxScript: true must keep the helper")
	}
}

func TestHtmxScriptWrongTypeIsFriendlyError(t *testing.T) {
	_, err := Parse("ghtmx.json", []byte("{\n\t\"htmxScript\": \"nope\"\n}"))
	if err == nil || !strings.Contains(err.Error(), "true or false") {
		t.Fatalf("expected a friendly type error, got %v", err)
	}
}

func TestHtmxScriptFlagPrecedence(t *testing.T) {
	file, err := Parse("ghtmx.json", []byte(`{"htmxScript": true}`))
	if err != nil {
		t.Fatal(err)
	}
	off := false
	cfg := Resolve(file, Flags{HtmxScript: &off})
	if cfg.EmitHtmxScript() {
		t.Error("flag must beat file")
	}
}

func TestHashCanonicalizesNilHtmxScript(t *testing.T) {
	on := true
	explicit := Default()
	explicit.HtmxScript = &on
	if Default().Hash() != explicit.Hash() {
		t.Error("an absent htmxScript must hash like an explicit true")
	}
	off := false
	disabled := Default()
	disabled.HtmxScript = &off
	if Default().Hash() == disabled.Hash() {
		t.Error("htmxScript: false must change the hash")
	}
}
