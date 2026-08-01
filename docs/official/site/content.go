// Package site is the official ghtmx documentation site: chi routes,
// ghtmx templates, and the embedded single-source documents. The site
// dogfoods the engine — route-aware bindings, compile-time fragments,
// and the event contract are all in use in its own templates.
package site

import (
	"bytes"
	"fmt"
	stdhtml "html"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer/html"

	"github.com/go-monolith/ghtmx/docs/official/content"
)

// Doc is one reference document, served under /docs/{slug}.
type Doc struct {
	Slug  string
	Title string
	File  string // file name under content/docs
}

// Docs is the reference routing table. index.md and
// getting-started.md are served by their own routes, not listed here.
var Docs = []Doc{
	{Slug: "overview", Title: "Overview", File: "README.md"},
	{Slug: "syntax", Title: "Syntax and usage", File: "SYNTAX.md"},
	{Slug: "diagnostics", Title: "Diagnostics", File: "DIAGNOSTICS.md"},
	{Slug: "config", Title: "Configuration", File: "CONFIG.md"},
	{Slug: "build-targets", Title: "Build targets", File: "build-targets.md"},
	{Slug: "editors", Title: "Editor support", File: "editors.md"},
	{Slug: "conformance", Title: "templ conformance", File: "CONFORMANCE.md"},
	{Slug: "baseline", Title: "Fork baseline", File: "TEMPL_SYNTAX_BASELINE.md"},
	{Slug: "contributing", Title: "Contributing", File: "CONTRIBUTING.md"},
	{Slug: "releasing", Title: "Releasing", File: "RELEASING.md"},
	{Slug: "changelog", Title: "Changelog", File: "CHANGELOG.md"},
}

// TOCEntry is one "On this page" link, extracted from a rendered
// document's h2/h3 headings.
type TOCEntry struct {
	Level int // 2 or 3
	ID    string
	Text  string
}

// NavLink is a pager (previous/next) target.
type NavLink struct {
	Title string
	Href  string
}

// PageView is everything a reference-document page needs: the
// rendered body, breadcrumbs, table of contents, and pager
// neighbours.
type PageView struct {
	Active   string
	Title    string
	Category string // breadcrumb parent for sectioned sub-pages
	Body     string
	TOC      []TOCEntry
	Prev     NavLink
	Next     NavLink
}

// NewPageView assembles the view for one document slug.
func NewPageView(active, title, body string) PageView {
	prev, next := pagerFor(active)
	return PageView{
		Active: active,
		Title:  title,
		Body:   body,
		TOC:    extractTOC(body),
		Prev:   prev,
		Next:   next,
	}
}

// headingPattern matches goldmark's heading output, where the
// auto-generated id is always the first attribute. Accepted limits
// for repo-owned content: a raw-HTML heading with another leading
// attribute is skipped, and the close tag is not level-matched (RE2
// has no backreferences) — well-formed output never mixes levels.
var headingPattern = regexp.MustCompile(`(?s)<h([23]) id="([^"]+)"[^>]*>(.*?)</h[23]>`)
var tagPattern = regexp.MustCompile(`<[^>]*>`)

// extractTOC pulls the h2/h3 headings out of rendered HTML. Heading
// IDs come from goldmark's auto-heading-ID parser option.
func extractTOC(body string) []TOCEntry {
	var toc []TOCEntry
	for _, m := range headingPattern.FindAllStringSubmatch(body, -1) {
		level := 2
		if m[1] == "3" {
			level = 3
		}
		text := strings.TrimSpace(stdhtml.UnescapeString(tagPattern.ReplaceAllString(m[3], "")))
		if text == "" {
			continue
		}
		toc = append(toc, TOCEntry{Level: level, ID: m[2], Text: text})
	}
	return toc
}

// tocClass styles a TOC entry by heading depth.
func tocClass(level int) string {
	if level == 3 {
		return "toc-h3"
	}
	return "toc-h2"
}

// pagerOrder is the reading order the previous/next pager follows —
// the same top-to-bottom order as the sidebar, with the syntax
// sub-pages inlined after their category overview.
func pagerOrder() []NavLink {
	order := []NavLink{
		{Title: "Introduction", Href: "/"},
		{Title: "Getting started", Href: "/getting-started"},
		{Title: "Syntax and usage", Href: "/docs/syntax"},
	}
	for _, s := range syntaxNav() {
		order = append(order, NavLink{Title: s.Title, Href: "/docs/syntax/" + s.ID})
	}
	for _, d := range append(append([]Doc{}, ReferenceDocs...), ProjectDocs...) {
		order = append(order, NavLink{Title: d.Title, Href: "/docs/" + d.Slug})
	}
	return append(order, NavLink{Title: "Examples", Href: "/examples"})
}

// pagerFor returns the neighbours of a page in reading order. The
// active key is "home", "examples", a document slug, or
// "syntax/<section>".
func pagerFor(active string) (prev, next NavLink) {
	href := "/docs/" + active
	switch active {
	case "home":
		href = "/"
	case "getting-started", "examples":
		href = "/" + active
	}
	order := pagerOrder()
	for i, link := range order {
		if link.Href != href {
			continue
		}
		if i > 0 {
			prev = order[i-1]
		}
		if i < len(order)-1 {
			next = order[i+1]
		}
		return prev, next
	}
	return NavLink{}, NavLink{}
}

// Sidebar groups: Reference and Project partition Docs (minus the
// syntax category, which renders as a collapsible sub-menu), and the
// pager follows the grouped order. Docs remains the routing table;
// TestDocGroupsPartitionDocs keeps the partition complete.
func docsBySlug(slugs ...string) []Doc {
	var out []Doc
	for _, slug := range slugs {
		if d, ok := DocBySlug(slug); ok {
			out = append(out, d)
		}
	}
	return out
}

var (
	ReferenceDocs = docsBySlug("diagnostics", "config", "build-targets", "editors")
	ProjectDocs   = docsBySlug("overview", "conformance", "baseline", "contributing", "releasing", "changelog")
)

// DocSection is one H2 slice of a sectioned document, served as its
// own sub-page (the templ.guide "Syntax and usage" category shape).
type DocSection struct {
	ID    string
	Title string
	Body  string // section HTML with the heading promoted to h1
}

var h2Pattern = regexp.MustCompile(`<h2 id="([^"]+)"[^>]*>(?s:(.*?))</h2>`)

var syntaxOnce sync.Once
var syntaxSections []DocSection
var syntaxIntro string
var syntaxErr error

// SyntaxSections splits the rendered SYNTAX.md into its H2 sections.
// The split happens over the single-source render, so sub-pages can
// never drift from the specification.
func SyntaxSections() ([]DocSection, string, error) {
	syntaxOnce.Do(func() {
		html, err := renderMarkdown("docs/SYNTAX.md")
		if err != nil {
			syntaxErr = err
			return
		}
		locs := h2Pattern.FindAllStringSubmatchIndex(html, -1)
		if len(locs) == 0 {
			syntaxIntro = html
			return
		}
		syntaxIntro = html[:locs[0][0]]
		for i, loc := range locs {
			end := len(html)
			if i+1 < len(locs) {
				end = locs[i+1][0]
			}
			id := html[loc[2]:loc[3]]
			rawTitle := html[loc[4]:loc[5]]
			title := strings.TrimSpace(stdhtml.UnescapeString(tagPattern.ReplaceAllString(rawTitle, "")))
			body := "<h1>" + rawTitle + "</h1>" + html[loc[1]:end]
			syntaxSections = append(syntaxSections, DocSection{ID: id, Title: title, Body: body})
		}
	})
	return syntaxSections, syntaxIntro, syntaxErr
}

// SyntaxSectionByID returns one syntax sub-page.
func SyntaxSectionByID(id string) (DocSection, bool) {
	sections, _, err := SyntaxSections()
	if err != nil {
		return DocSection{}, false
	}
	for _, s := range sections {
		if s.ID == id {
			return s, true
		}
	}
	return DocSection{}, false
}

// syntaxNav is the sidebar's section list; a render failure degrades
// to an empty sub-menu rather than a broken shell.
func syntaxNav() []DocSection {
	sections, _, err := SyntaxSections()
	if err != nil {
		return nil
	}
	return sections
}

// inSyntax reports whether the active key belongs to the syntax
// category, which keeps its sidebar sub-menu expanded.
func inSyntax(active string) bool {
	return active == "syntax" || strings.HasPrefix(active, "syntax/")
}

// heroCode is the landing-page sample: the hello-world example's
// template, close to templ syntax but with a bound route.
const heroCode = `package main

templ page(name string) {
	<!DOCTYPE html>
	<html>
		<head>@ghtmxgen.HTMXScript()</head>
		<body>
			<h1>Hello, { name }</h1>
			<button hx-get={ home } hx-target="body">reload</button>
		</body>
	</html>
}`

// sourceLanguage picks the client-side highlighter grammar class for
// a displayed source file (.ghtmx maps to Go in the layout script).
func sourceLanguage(name string) string {
	switch {
	case strings.HasSuffix(name, ".ghtmx"):
		return "language-ghtmx"
	case strings.HasSuffix(name, ".go"):
		return "language-go"
	default:
		return "language-plaintext"
	}
}

// navClass builds a sidebar link's class list.
func navClass(base string, active bool) string {
	switch {
	case active && base != "":
		return base + " active"
	case active:
		return "active"
	default:
		return base
	}
}

// DocBySlug returns the reference document for a /docs/{slug} URL.
func DocBySlug(slug string) (Doc, bool) {
	for _, d := range Docs {
		if d.Slug == slug {
			return d, true
		}
	}
	return Doc{}, false
}

// Example is one example application shown under /examples.
type Example struct {
	Name        string // directory name and URL segment
	Title       string
	Description string
}

// Examples lists every example shipped in the repository.
var Examples = []Example{
	{Name: "hello-world", Title: "Hello world", Description: "The walking skeleton: one template, one route, one rendered page."},
	{Name: "hx-bindings", Title: "Route bindings", Description: "Symbol and constructor bindings: every htmx URL is resolved against a real Go route at build time."},
	{Name: "fragments", Title: "Fragments", Description: "Compile-time fragments rendered inline in a page and standalone for htmx swaps, byte-identically."},
	{Name: "events", Title: "Events", Description: "The server-driven event contract: declared events, generated HX-Trigger emitters, and CSRF headers."},
	{Name: "crud", Title: "CRUD todos", Description: "The reference application: full CRUD with partial updates and zero hand-written htmx glue."},
}

// ExampleByName returns the example for an /examples/{name} URL.
func ExampleByName(name string) (Example, bool) {
	for _, e := range Examples {
		if e.Name == name {
			return e, true
		}
	}
	return Example{}, false
}

// SourceFile is one displayed source file of an example.
type SourceFile struct {
	Name   string // display name relative to the example directory
	Source string
}

var markdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

var (
	renderMu    sync.Mutex
	renderCache = map[string]string{}
)

// renderMarkdown converts one embedded markdown file to HTML, caching
// the result: content is embedded and immutable, so every render after
// the first is a map lookup (this keeps wasm cold starts cheap).
func renderMarkdown(name string) (string, error) {
	renderMu.Lock()
	defer renderMu.Unlock()
	if html, ok := renderCache[name]; ok {
		return html, nil
	}
	source, err := content.FS.ReadFile(name)
	if err != nil {
		return "", err
	}
	var body bytes.Buffer
	if err := markdown.Convert(rewriteLinks(source), &body); err != nil {
		return "", fmt.Errorf("render %s: %w", name, err)
	}
	renderCache[name] = body.String()
	return body.String(), nil
}

// DocHTML renders a reference document to HTML.
func DocHTML(d Doc) (string, error) {
	return renderMarkdown(path.Join("docs", d.File))
}

// PageHTML renders a site-only page (index.md, getting-started.md).
func PageHTML(file string) (string, error) {
	return renderMarkdown(path.Join("docs", file))
}

// rewriteLinks maps repository-document mentions like `SYNTAX.md` to
// their site routes and backticked repository paths (the spec's
// "Verified by" anchors and friends) to the sources on GitHub, so a
// reader can click through to the verifying tests. Fenced code blocks
// are left untouched: an example quoting a document name must not
// gain a link.
func rewriteLinks(source []byte) []byte {
	segments := strings.Split(string(source), "```")
	for i := range segments {
		if i%2 == 1 {
			continue // inside a fence
		}
		for _, d := range Docs {
			if d.Slug == "build-targets" {
				continue // site-only source; its base name is not a repo document
			}
			segments[i] = linkifyMentions(segments[i], d.File, "/docs/"+d.Slug)
		}
		segments[i] = linkifyRepoPaths(segments[i])
	}
	return []byte(strings.Join(segments, "```"))
}

// repoTreeURL is where backticked repository paths link to; GitHub
// redirects tree URLs to blob view for files.
const repoTreeURL = "https://github.com/go-monolith/ghtmx/tree/main/"

var repoPathPattern = regexp.MustCompile("`((?:internal|examples|conformance|benchmarks|adapters|editors|cmd|runtime|docs)/[A-Za-z0-9_./\\-]*)`")

// linkifyRepoPaths turns backticked repository paths into GitHub
// source links, leaving mentions that are already link text alone.
func linkifyRepoPaths(segment string) string {
	matches := repoPathPattern.FindAllStringSubmatchIndex(segment, -1)
	if len(matches) == 0 {
		return segment
	}
	var b strings.Builder
	last := 0
	for _, m := range matches {
		start, end := m[0], m[1]
		path := strings.TrimSuffix(segment[m[2]:m[3]], "/")
		b.WriteString(segment[last:start])
		if start > 0 && segment[start-1] == '[' {
			b.WriteString(segment[start:end])
		} else {
			b.WriteString("[`")
			b.WriteString(path)
			b.WriteString("`](")
			b.WriteString(repoTreeURL)
			b.WriteString(path)
			b.WriteString(")")
		}
		last = end
	}
	b.WriteString(segment[last:])
	return b.String()
}

// linkifyMentions turns `name` mentions into links to target, leaving
// mentions that are already link text (preceded by "[") untouched.
func linkifyMentions(segment, name, target string) string {
	needle := "`" + name + "`"
	var b strings.Builder
	for {
		i := strings.Index(segment, needle)
		if i < 0 {
			b.WriteString(segment)
			return b.String()
		}
		if i > 0 && segment[i-1] == '[' {
			b.WriteString(segment[:i+len(needle)])
		} else {
			b.WriteString(segment[:i])
			b.WriteString("[`")
			b.WriteString(name)
			b.WriteString("`](")
			b.WriteString(target)
			b.WriteString(")")
		}
		segment = segment[i+len(needle):]
	}
}

// ExampleFiles returns an example's displayed sources (templates
// first, then Go files) and its README rendered to HTML ("" if the
// example has none).
func ExampleFiles(name string) ([]SourceFile, string, error) {
	root := path.Join("examples", name)
	var files []SourceFile
	readme := ""
	err := fs.WalkDir(content.FS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(p, root+"/")
		if rel == "README.md" {
			html, err := renderMarkdown(p)
			if err != nil {
				return err
			}
			readme = html
			return nil
		}
		source, err := content.FS.ReadFile(p)
		if err != nil {
			return err
		}
		files = append(files, SourceFile{
			Name:   strings.TrimSuffix(rel, ".txt"),
			Source: string(source),
		})
		return nil
	})
	if err != nil {
		return nil, "", err
	}
	sort.Slice(files, func(i, j int) bool {
		ti, tj := strings.HasSuffix(files[i].Name, ".ghtmx"), strings.HasSuffix(files[j].Name, ".ghtmx")
		if ti != tj {
			return ti
		}
		return files[i].Name < files[j].Name
	})
	return files, readme, nil
}
