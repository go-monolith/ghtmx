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
			name: "plain mention becomes a code-styled link",
			in:   "See `SYNTAX.md` for the grammar.",
			want: "See [`SYNTAX.md`](/docs/syntax) for the grammar.",
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

// TestDocGroupsPartitionDocs: every document in Docs must appear in
// exactly one sidebar group, or it silently vanishes from the sidebar
// and the pager while staying routable.
func TestDocGroupsPartitionDocs(t *testing.T) {
	grouped := map[string]int{}
	for _, d := range append(append([]Doc{}, LanguageDocs...), ProjectDocs...) {
		grouped[d.Slug]++
	}
	for _, d := range Docs {
		if grouped[d.Slug] != 1 {
			t.Errorf("doc %s appears %d times in the sidebar groups, want exactly once", d.Slug, grouped[d.Slug])
		}
		delete(grouped, d.Slug)
	}
	for slug := range grouped {
		t.Errorf("sidebar group lists unknown doc %s", slug)
	}
}

// TestPagerFor pins the reading-order neighbours, including the
// one-sided ends and unknown keys.
func TestPagerFor(t *testing.T) {
	prev, next := pagerFor("home")
	if prev.Href != "" || next.Href != "/getting-started" {
		t.Errorf("pagerFor(home) = (%q, %q), want no prev and /getting-started next", prev.Href, next.Href)
	}
	prev, next = pagerFor("examples")
	if prev.Href == "" || next.Href != "" {
		t.Errorf("pagerFor(examples) = (%q, %q), want a prev and no next", prev.Href, next.Href)
	}
	prev, next = pagerFor("syntax")
	if prev.Href == "" || next.Href == "" {
		t.Errorf("pagerFor(syntax) = (%q, %q), want both neighbours", prev.Href, next.Href)
	}
	prev, next = pagerFor("nope")
	if prev.Href != "" || next.Href != "" {
		t.Errorf("pagerFor(nope) = (%q, %q), want empty pager", prev.Href, next.Href)
	}
}

// TestExtractTOC pins the heading extraction against goldmark-shaped
// output.
func TestExtractTOC(t *testing.T) {
	body := `<h2 id="one">One</h2><p>x</p>` +
		`<h3 id="one-sub">With <code>code</code></h3>` +
		`<h2 id="two">Two &amp; more</h2>` +
		`<h2>no id, skipped</h2>` +
		`<h2 id="empty"><img src="x"/></h2>`
	got := extractTOC(body)
	want := []TOCEntry{
		{Level: 2, ID: "one", Text: "One"},
		{Level: 3, ID: "one-sub", Text: "With code"},
		{Level: 2, ID: "two", Text: "Two & more"},
	}
	if len(got) != len(want) {
		t.Fatalf("extractTOC returned %d entries, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("entry %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// TestHelperClasses pins the small pure helpers.
func TestHelperClasses(t *testing.T) {
	if got := sourceLanguage("crud.ghtmx"); got != "language-ghtmx" {
		t.Errorf("sourceLanguage(crud.ghtmx) = %q", got)
	}
	if got := sourceLanguage("main.go"); got != "language-go" {
		t.Errorf("sourceLanguage(main.go) = %q", got)
	}
	if got := sourceLanguage("README.md"); got != "language-plaintext" {
		t.Errorf("sourceLanguage(README.md) = %q", got)
	}
	if got := tocClass(3); got != "toc-h3" {
		t.Errorf("tocClass(3) = %q", got)
	}
	if got := tocClass(2); got != "toc-h2" {
		t.Errorf("tocClass(2) = %q", got)
	}
	if got := navClass("brand", true); got != "brand active" {
		t.Errorf(`navClass("brand", true) = %q`, got)
	}
	if got := navClass("", false); got != "" {
		t.Errorf(`navClass("", false) = %q`, got)
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
