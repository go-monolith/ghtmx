package generatecmd

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/go-monolith/ghtmx/internal/config"
)

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// FatalError is what stops a watch-mode session instead of logging and
// carrying on, so its errors.Is/As behaviour decides whether a build
// failure ends the run or is quietly swallowed and retried forever.
func TestFatalError(t *testing.T) {
	inner := errors.New("boom")
	fatal := FatalError{Err: inner}

	if got := fatal.Error(); got != "boom" {
		t.Errorf("Error() = %q, want %q", got, "boom")
	}
	if got := fatal.Unwrap(); !errors.Is(got, inner) {
		t.Errorf("Unwrap() = %v, want %v", got, inner)
	}

	// errors.Is must match any FatalError, so a caller can ask "is this
	// fatal" without knowing which error is inside.
	if !errors.Is(fatal, FatalError{}) {
		t.Error("errors.Is(fatal, FatalError{}) = false, want true")
	}
	// And it must still find the wrapped cause.
	if !errors.Is(fatal, inner) {
		t.Error("errors.Is(fatal, inner) = false; the wrapped cause is unreachable")
	}
	if errors.Is(fatal, errors.New("unrelated")) {
		t.Error("errors.Is matched an unrelated error")
	}

	var target FatalError
	if !errors.As(fatal, &target) {
		t.Error("errors.As(fatal, &FatalError{}) = false, want true")
	}
	if !errors.Is(target.Err, inner) {
		t.Errorf("errors.As left the target empty: %+v", target)
	}

	// Wrapped one level deeper, the classification must survive: this is
	// how it actually arrives from the generate pipeline.
	wrapped := errors.Join(errors.New("context"), fatal)
	if !errors.Is(wrapped, FatalError{}) {
		t.Error("a FatalError joined with another error is no longer recognised as fatal")
	}
}

func TestCentralFilePath(t *testing.T) {
	cmd := &Generate{}
	cmd.Args.Config = config.Config{}
	cmd.Args.Config.GeneratedPackage.Dir = "ghtmxgen"
	cmd.Args.Config.GeneratedSuffix = "_ghtmx.go"

	// No module root means there is nowhere to write the central
	// package, and the caller relies on "" to skip it rather than
	// writing to a path relative to the process working directory.
	if got := cmd.centralFilePath(""); got != "" {
		t.Errorf("centralFilePath(\"\") = %q, want empty", got)
	}

	want := filepath.Join("/project", "ghtmxgen", "routes_ghtmx.go")
	if got := cmd.centralFilePath("/project"); got != want {
		t.Errorf("centralFilePath(\"/project\") = %q, want %q", got, want)
	}
}

// TestAttributeValidationOptionDegradesOnAnUnknownVersion pins that an
// htmx version the surface data does not know disables validation rather
// than failing the build. Generation must still work against a version
// this compiler has never heard of.
func TestAttributeValidationOptionDegradesOnAnUnknownVersion(t *testing.T) {
	tests := []struct {
		name    string
		version string
	}{
		{"unknown version", "0.0.0-not-a-real-htmx"},
		{"empty version", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := attributeValidationOption(quietLog(), config.Config{HtmxVersion: tt.version})
			if opt == nil {
				t.Fatal("attributeValidationOption returned nil; it must always return a usable option")
			}
			// Applying it must be safe whether or not validation was
			// configured.
			h := &FSEventHandler{}
			opt(h)
		})
	}
}

func TestToolchainIdentity(t *testing.T) {
	// The salt is the documented override, and it exists so tests get a
	// stable identity: without it a devel build has none and the cache
	// is disabled.
	t.Setenv("GHTMX_BUILD_CACHE_SALT", "test-salt")
	id, ok := toolchainIdentity()
	if !ok {
		t.Fatal("toolchainIdentity reported not-ok with the salt set")
	}
	if id == "" {
		t.Error("toolchainIdentity returned an empty id")
	}
	// The Go version is part of the identity: the same source compiled
	// by a different toolchain can emit different output.
	if len(id) <= len("test-salt|") {
		t.Errorf("id = %q, want the salt joined with the Go version", id)
	}

	// A different salt must produce a different identity, or cached
	// output would be reused across generators that disagree.
	t.Setenv("GHTMX_BUILD_CACHE_SALT", "other-salt")
	other, ok := toolchainIdentity()
	if !ok {
		t.Fatal("toolchainIdentity reported not-ok for the second salt")
	}
	if other == id {
		t.Errorf("two different salts produced the same identity %q", id)
	}
}
