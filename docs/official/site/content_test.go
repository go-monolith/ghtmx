package site

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/docs/official/content"
)

// TestRewriteLinks locks the intended behavior: mentions become
// links, fenced code blocks stay untouched, and mentions that are
// already link text are not double-linked.
func TestRewriteLinks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plain mention becomes a link",
			in:   "See `SYNTAX.md` for the grammar.",
			want: "See [SYNTAX.md](/docs/syntax) for the grammar.",
		},
		{
			name: "fenced code block untouched",
			in:   "```\ncat `SYNTAX.md`\n```",
			want: "```\ncat `SYNTAX.md`\n```",
		},
		{
			name: "already-linked mention untouched",
			in:   "See [`SYNTAX.md`](https://example.com/syntax).",
			want: "See [`SYNTAX.md`](https://example.com/syntax).",
		},
		{
			name: "site-only base name untouched",
			in:   "See `build-targets.md` too.",
			want: "See `build-targets.md` too.",
		},
	}
	for _, tc := range cases {
		if got := string(rewriteLinks([]byte(tc.in))); got != tc.want {
			t.Errorf("%s: rewriteLinks(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestExamplesManifestMatchesEmbeddedContent: the Examples manifest
// and the embedded content/examples tree must name the same set —
// an example copied in but not listed (or listed but not copied)
// is a wiring bug.
func TestExamplesManifestMatchesEmbeddedContent(t *testing.T) {
	entries, err := content.FS.ReadDir("examples")
	if err != nil {
		t.Fatal(err)
	}
	embedded := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			embedded[e.Name()] = true
		}
	}
	for _, e := range Examples {
		if !embedded[e.Name] {
			t.Errorf("manifest example %s has no embedded content", e.Name)
		}
		delete(embedded, e.Name)
	}
	for name := range embedded {
		t.Errorf("embedded example %s is not in the Examples manifest", name)
	}
	if len(Examples) == 0 {
		t.Fatal("Examples manifest is empty")
	}
	var missing []string
	for _, e := range Examples {
		if _, ok := ExampleByName(e.Name); !ok {
			missing = append(missing, e.Name)
		}
	}
	if len(missing) > 0 {
		t.Errorf("ExampleByName cannot resolve %s", strings.Join(missing, ", "))
	}
}
