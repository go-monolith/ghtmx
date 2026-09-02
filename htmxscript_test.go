package ghtmx

import (
	"context"
	"slices"
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
	if versions[len(versions)-1] != "4.0.0" {
		t.Errorf("4.0.0 must sort last numerically, got %v", versions)
	}
	// Numeric, not lexical: 2.0.10 follows 2.0.9 and precedes 4.0.0.
	i9, i10 := slices.Index(versions, "2.0.9"), slices.Index(versions, "2.0.10")
	if i9 < 0 || i10 < 0 || i9 > i10 {
		t.Errorf("2.0.9 must precede 2.0.10, got %v", versions)
	}
}

func TestHTMXScriptTagHtmx4(t *testing.T) {
	var sb strings.Builder
	if err := HTMXScriptTag("4.0.0").Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{
		`src="https://cdn.jsdelivr.net/npm/htmx.org@4.0.0/dist/htmx.min.js"`,
		`integrity="sha384-BvJpBiO8Kh31EqtJe5DRIeWrHWnCGkwytKs9NKFi86Hhw96dEqdEMzZDeK9iEGTc"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in tag:\n%s", want, out)
		}
	}
}

func TestHTMXScriptIntegrity(t *testing.T) {
	integrity, ok := HTMXScriptIntegrity("2.0.10")
	if !ok {
		t.Fatal("2.0.10 must have a pinned integrity hash")
	}
	if !strings.HasPrefix(integrity, "sha384-") {
		t.Errorf("integrity = %q, want a sha384 pin", integrity)
	}
	// The accessor exists so tests can assert a vendored file against the
	// pin without scraping HTML; the rendered tag must agree with it.
	var sb strings.Builder
	if err := HTMXScriptTag("2.0.10").Render(context.Background(), &sb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), `integrity="`+integrity+`"`) {
		t.Errorf("rendered tag does not carry the pinned hash %q:\n%s", integrity, sb.String())
	}
	if _, ok := HTMXScriptIntegrity("1.9.12"); ok {
		t.Error("an unpinned version must report ok=false")
	}
}
