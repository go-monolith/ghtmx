package ghtmx

import (
	"context"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/htmxsurface"
)

func TestHTMXScriptTag(t *testing.T) {
	var sb strings.Builder
	if err := HTMXScriptTag("2.0.10").Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{
		`src="https://cdn.jsdelivr.net/npm/htmx.org@2.0.10/dist/htmx.min.js"`,
		`integrity="sha384-H5SrcfygHmAuTDZphMHqBJLc3FhssKjG7w/CeCpFReSfwBWDTKpkzPP8c+cLsK+V"`,
		`crossorigin="anonymous"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in tag:\n%s", want, out)
		}
	}
}

func TestHTMXScriptTagUnknownVersionFails(t *testing.T) {
	var sb strings.Builder
	err := HTMXScriptTag("1.9.12").Render(context.Background(), &sb)
	if err == nil || !strings.Contains(err.Error(), "GHTMX-E0502") || !strings.Contains(err.Error(), "1.9.12") {
		t.Fatalf("expected an E0502 error naming the version, got %v", err)
	}
	if sb.String() != "" {
		t.Errorf("no partial tag may be written, got %q", sb.String())
	}
}

func TestHTMXScriptTagCustomSrcEscaped(t *testing.T) {
	var sb strings.Builder
	if err := HTMXScriptTag("2.0.10", WithScriptSrc(`/assets/htmx.min.js?a=1&b="2"`)).Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	if !strings.Contains(out, `src="/assets/htmx.min.js?a=1&amp;b=&#34;2&#34;"`) {
		t.Errorf("custom src must be attribute-escaped, got %s", out)
	}
	// The integrity pin stays: a self-hosted asset must be the exact
	// published build of the configured version.
	if !strings.Contains(out, `integrity="sha384-`) {
		t.Errorf("integrity must be pinned for custom sources too, got %s", out)
	}
}

// TestScriptVersionsMatchSurface: every version with a pinned script
// asset is a version the surface set accepts, and the surface's known
// versions all have pinned assets — the two tables cannot drift.
func TestScriptVersionsMatchSurface(t *testing.T) {
	for _, v := range SupportedHtmxVersions() {
		if _, err := htmxsurface.ForVersion(v); err != nil {
			t.Errorf("script asset pinned for %s, but the surface rejects it: %v", v, err)
		}
	}
	// Reverse direction: a surface version without a pinned asset would
	// bake a version into HTMXScript() that fails at render time.
	for _, v := range htmxsurface.SupportedVersions() {
		if _, ok := htmxIntegrity[v]; !ok {
			t.Errorf("surface version %s has no pinned script asset", v)
		}
	}
}

func TestSupportedHtmxVersionsNumericOrder(t *testing.T) {
	versions := SupportedHtmxVersions()
	if versions[len(versions)-1] != "2.0.10" {
		t.Errorf("2.0.10 must sort last numerically, got %v", versions)
	}
}
