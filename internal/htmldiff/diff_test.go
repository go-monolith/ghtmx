package htmldiff

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx"
)

// This package is the assertion tool for 43 test packages: when it says
// "no diff", 43 golden-corpus tests pass. A false negative here — a
// normalizer that collapses away a difference that matters — turns the
// whole corpus green regardless of what the compiler emits. So these
// tests come in two halves: differences that must be invisible
// (formatting), and differences that must not be (content).

// componentFunc adapts a render function to ghtmx.Component.
type componentFunc func(ctx context.Context, w io.Writer) error

func (f componentFunc) Render(ctx context.Context, w io.Writer) error { return f(ctx, w) }

func markup(s string) ghtmx.Component {
	return componentFunc(func(ctx context.Context, w io.Writer) error {
		_, err := io.WriteString(w, s)
		return err
	})
}

// TestFormattingDifferencesAreInvisible pins what the normalizer is for:
// the compiler is free to change its indentation and line breaks without
// breaking every golden file in the corpus.
func TestFormattingDifferencesAreInvisible(t *testing.T) {
	tests := []struct {
		name             string
		expected, actual string
	}{
		{
			name:     "indentation",
			expected: "<div><p>hi</p></div>",
			actual:   "<div>\n  <p>hi</p>\n</div>",
		},
		{
			name:     "collapsed whitespace runs in text",
			expected: "<p>hello world</p>",
			actual:   "<p>hello    \n\t world</p>",
		},
		{
			name:     "leading and trailing whitespace around text",
			expected: "<p>text</p>",
			actual:   "<p>\n   text\n</p>",
		},
		{
			name:     "whitespace inside a comment",
			expected: "<!-- note -->",
			actual:   "<!--    note    -->",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diff, err := DiffStrings(tt.expected, tt.actual)
			if err != nil {
				t.Fatal(err)
			}
			if diff != "" {
				t.Errorf("formatting difference registered as a diff:\n%s", diff)
			}
		})
	}
}

// TestContentDifferencesAreVisible is the half that actually protects
// the corpus. Each case is a change the normalizer must not swallow.
func TestContentDifferencesAreVisible(t *testing.T) {
	tests := []struct {
		name             string
		expected, actual string
	}{
		{"text", "<p>hello</p>", "<p>goodbye</p>"},
		{"tag name", "<p>x</p>", "<div>x</div>"},
		{"attribute value", `<a href="/a">x</a>`, `<a href="/b">x</a>`},
		{"attribute name", `<a href="/a">x</a>`, `<a hgef="/a">x</a>`},
		{"added attribute", "<p>x</p>", `<p class="c">x</p>`},
		{"nesting depth", "<div><p>x</p></div>", "<div><span><p>x</p></span></div>"},
		{"element order", "<div><a></a><b></b></div>", "<div><b></b><a></a></div>"},
		{"missing element", "<div><p>x</p><p>y</p></div>", "<div><p>x</p></div>"},
		{"comment text", "<!--a-->", "<!--b-->"},
		// Whitespace is significant inside pre, so this must be a diff
		// even though the same change elsewhere would not be.
		{"whitespace inside pre", "<pre>a  b</pre>", "<pre>a b</pre>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diff, err := DiffStrings(tt.expected, tt.actual)
			if err != nil {
				t.Fatal(err)
			}
			if diff == "" {
				t.Errorf("a real difference was normalized away:\nexpected %q\nactual   %q", tt.expected, tt.actual)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string // substrings the canonical form must contain
	}{
		{
			name: "doctype",
			src:  "<!DOCTYPE html><html><body></body></html>",
			want: []string{"<!doctype html>"},
		},
		{
			name: "void element has no closing tag",
			src:  "<div><br><img src=\"x.png\"></div>",
			want: []string{"<br>", `<img src="x.png">`, "</div>"},
		},
		{
			name: "attribute values are escaped",
			src:  `<a title="a&amp;b">x</a>`,
			want: []string{`title="a&amp;b"`},
		},
		{
			name: "textarea content is verbatim",
			src:  "<textarea>  keep  me  </textarea>",
			want: []string{"  keep  me  "},
		},
		{
			name: "comment",
			src:  "<div><!-- hi --></div>",
			want: []string{"<!--hi-->"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tt.want {
				if !strings.Contains(got, want) {
					t.Errorf("normalized form is missing %q:\n%s", want, got)
				}
			}
		})
	}
}

// TestNormalizeVoidElementsHaveNoClosingTag pins the void-element branch
// directly: emitting </br> would make every golden containing a line
// break differ from what a browser sees.
func TestNormalizeVoidElementsHaveNoClosingTag(t *testing.T) {
	got, err := Normalize("<div><br></div>")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, "</br>") {
		t.Errorf("normalized form closes a void element:\n%s", got)
	}
}

func TestDiffRendersTheComponent(t *testing.T) {
	actual, diff, err := Diff(markup("<p>hello</p>"), "<p>hello</p>")
	if err != nil {
		t.Fatal(err)
	}
	if diff != "" {
		t.Errorf("identical markup produced a diff:\n%s", diff)
	}
	if !strings.Contains(actual, "hello") {
		t.Errorf("returned actual %q does not contain the rendered content", actual)
	}
}

// TestDiffReportsRenderFailure pins that a component which fails to
// render is reported as an error rather than silently comparing as an
// empty document — which would look like a diff against expected, or
// worse, pass against an empty golden.
func TestDiffReportsRenderFailure(t *testing.T) {
	sentinel := errors.New("render exploded")
	broken := componentFunc(func(ctx context.Context, w io.Writer) error { return sentinel })

	_, _, err := Diff(broken, "<p>x</p>")
	if !errors.Is(err, sentinel) {
		t.Errorf("Diff returned %v, want it to wrap %v", err, sentinel)
	}
}

// TestDiffCtxPassesTheContextThrough pins that DiffCtx is not quietly
// substituting a background context — components that read request
// values or honour cancellation depend on the caller's one arriving.
func TestDiffCtxPassesTheContextThrough(t *testing.T) {
	type keyType string
	const key keyType = "marker"
	ctx := context.WithValue(context.Background(), key, "present")

	var seen any
	probe := componentFunc(func(ctx context.Context, w io.Writer) error {
		seen = ctx.Value(key)
		_, err := io.WriteString(w, "<p>x</p>")
		return err
	})

	if _, _, err := DiffCtx(ctx, probe, "<p>x</p>"); err != nil {
		t.Fatal(err)
	}
	if seen != "present" {
		t.Errorf("the component saw context value %v, want %q", seen, "present")
	}
}

// TestGoldenUpdateModeRewritesTheGolden covers the GHTMX_UPDATE_GOLDEN
// path, and its guard: the mode must refuse to act in a package with
// more than one golden, where it cannot know which one to overwrite.
func TestGoldenUpdateModeRewritesTheGolden(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	t.Setenv("GHTMX_UPDATE_GOLDEN", "1")

	if err := os.WriteFile(filepath.Join(dir, "expected.html"), []byte("<p>stale</p>"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The expected value deliberately disagrees: update mode rewrites
	// rather than compares, so this must still report no diff.
	actual, diff, err := Diff(markup("<p>fresh</p>"), "<p>stale</p>")
	if err != nil {
		t.Fatal(err)
	}
	if diff != "" {
		t.Errorf("update mode reported a diff instead of rewriting:\n%s", diff)
	}
	if actual != "<p>fresh</p>" {
		t.Errorf("returned actual = %q, want the freshly rendered markup", actual)
	}

	got, err := os.ReadFile(filepath.Join(dir, "expected.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "<p>fresh</p>" {
		t.Errorf("expected.html = %q, want the rendered output", got)
	}
}

func TestGoldenUpdateModeRefusesWithSeveralGoldens(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	t.Setenv("GHTMX_UPDATE_GOLDEN", "1")

	for _, name := range []string{"expected.html", "expected-alt.html"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("<p>stale</p>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// With several goldens the mode must fall through to a normal
	// comparison, leaving the files alone.
	_, diff, err := Diff(markup("<p>fresh</p>"), "<p>stale</p>")
	if err != nil {
		t.Fatal(err)
	}
	if diff == "" {
		t.Error("update mode rewrote a golden in a package with several; it must compare instead")
	}
	got, err := os.ReadFile(filepath.Join(dir, "expected.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "<p>stale</p>" {
		t.Errorf("expected.html was modified to %q; it must be left alone", got)
	}
}

// TestGoldenUpdateModeIsOffByDefault pins that a normal test run never
// rewrites a golden — the failure mode where a broken compiler silently
// updates the file that would have caught it.
func TestGoldenUpdateModeIsOffByDefault(t *testing.T) {
	dir := t.TempDir()
	chdir(t, dir)
	t.Setenv("GHTMX_UPDATE_GOLDEN", "")

	if err := os.WriteFile(filepath.Join(dir, "expected.html"), []byte("<p>stale</p>"), 0o644); err != nil {
		t.Fatal(err)
	}

	_, diff, err := Diff(markup("<p>fresh</p>"), "<p>stale</p>")
	if err != nil {
		t.Fatal(err)
	}
	if diff == "" {
		t.Error("a differing render reported no diff with update mode off")
	}
	got, err := os.ReadFile(filepath.Join(dir, "expected.html"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "<p>stale</p>" {
		t.Errorf("expected.html was rewritten with update mode off: %q", got)
	}
}

func TestCollapse(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"a b", "a b"},
		{"  a   b  ", "a b"},
		{"a\n\tb", "a b"},
		{"   ", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := collapse(tt.in); got != tt.want {
				t.Errorf("collapse(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// chdir switches the working directory for the duration of a test. The
// golden-update path is defined in terms of the test's working
// directory, so exercising it means actually being somewhere else.
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(prev); err != nil {
			t.Fatal(err)
		}
	})
}
