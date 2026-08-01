// Package site is the official ghtmx documentation site: chi routes,
// ghtmx templates, and the embedded single-source documents. The site
// dogfoods the engine — route-aware bindings, compile-time fragments,
// and the event contract are all in use in its own templates.
package site

import (
	"bytes"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"

	"github.com/go-monolith/ghtmx/docs/official/content"
)

// Doc is one reference document, served under /docs/{slug}.
type Doc struct {
	Slug  string
	Title string
	File  string // file name under content/docs
}

// Docs is the reference navigation, in display order. index.md and
// getting-started.md are served by their own routes, not listed here.
var Docs = []Doc{
	{Slug: "overview", Title: "Overview", File: "README.md"},
	{Slug: "syntax", Title: "Syntax", File: "SYNTAX.md"},
	{Slug: "diagnostics", Title: "Diagnostics", File: "DIAGNOSTICS.md"},
	{Slug: "config", Title: "Configuration", File: "CONFIG.md"},
	{Slug: "build-targets", Title: "Build targets", File: "build-targets.md"},
	{Slug: "conformance", Title: "templ conformance", File: "CONFORMANCE.md"},
	{Slug: "baseline", Title: "Fork baseline", File: "TEMPL_SYNTAX_BASELINE.md"},
	{Slug: "changelog", Title: "Changelog", File: "CHANGELOG.md"},
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
// their site routes so cross-references keep working. Fenced code
// blocks are left untouched: an example quoting a document name must
// not gain a link.
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
	}
	return []byte(strings.Join(segments, "```"))
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
			b.WriteString("[")
			b.WriteString(name)
			b.WriteString("](")
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
