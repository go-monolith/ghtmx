// Package docsite builds the static documentation site from the
// repository's single-source documents (NFR-013): the syntax
// specification, diagnostic catalogue, configuration reference, and
// conformance record are rendered as-is, so the site cannot drift from
// the gates that keep those documents honest. Site-only pages (the
// landing page, getting started, build targets) live in docs/site.
//
// The builder is pure Go — no Node, per the constitution — and runs in
// tests on every build. It is a verification harness: its tests prove
// the site builds and that the getting-started guide compiles and
// renders. The published documentation site is docs/official, deployed
// to https://ghtmx.dev by deploy-docs.yml.
package docsite

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

// Page is one document of the site, in navigation order.
type Page struct {
	Slug   string // output name without extension
	Title  string
	Source string // repo-relative markdown source
}

// Pages is the site manifest. Sources under docs/site are site-only;
// everything else is a repository single-source document.
var Pages = []Page{
	{Slug: "index", Title: "ghtmx", Source: "docs/site/index.md"},
	{Slug: "getting-started", Title: "Getting started", Source: "docs/site/getting-started.md"},
	{Slug: "syntax", Title: "Syntax", Source: "SYNTAX.md"},
	{Slug: "diagnostics", Title: "Diagnostics", Source: "DIAGNOSTICS.md"},
	{Slug: "config", Title: "Configuration", Source: "CONFIG.md"},
	{Slug: "build-targets", Title: "Build targets", Source: "docs/site/build-targets.md"},
	{Slug: "conformance", Title: "templ conformance", Source: "CONFORMANCE.md"},
	{Slug: "baseline", Title: "Fork baseline", Source: "TEMPL_SYNTAX_BASELINE.md"},
}

var layout = template.Must(template.New("layout").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8"/>
<meta name="viewport" content="width=device-width, initial-scale=1"/>
<title>{{.Title}} — ghtmx</title>
<style>
:root { color-scheme: light dark; }
body { font-family: system-ui, sans-serif; margin: 0; display: flex; min-height: 100vh; }
nav { min-width: 14rem; padding: 1.5rem 1rem; border-right: 1px solid #8884; }
nav a { display: block; padding: .3rem .5rem; text-decoration: none; color: inherit; border-radius: .3rem; }
nav a.active, nav a:hover { background: #8882; }
main { padding: 2rem 3rem; max-width: 52rem; overflow-x: auto; }
pre { padding: 1rem; background: #8881; overflow-x: auto; border-radius: .4rem; }
code { font-family: ui-monospace, monospace; }
table { border-collapse: collapse; }
td, th { border: 1px solid #8884; padding: .4rem .6rem; text-align: left; vertical-align: top; }
h1, h2 { border-bottom: 1px solid #8884; padding-bottom: .3rem; }
</style>
</head>
<body>
<nav>
{{range .Nav}}<a href="{{.Slug}}.html"{{if eq .Slug $.Slug}} class="active"{{end}}>{{.Title}}</a>
{{end}}</nav>
<main>
{{.Body}}
</main>
</body>
</html>
`))

// Build renders the site into dstDir. root is the repository root
// holding the markdown sources.
func Build(root, dstDir string) error {
	if err := os.MkdirAll(dstDir, 0o755); err != nil {
		return err
	}
	markdown := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRendererOptions(html.WithUnsafe()),
	)
	for _, page := range Pages {
		source, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(page.Source)))
		if err != nil {
			return fmt.Errorf("page %s: %w", page.Slug, err)
		}
		source = rewriteLinks(source)
		var body bytes.Buffer
		if err := markdown.Convert(source, &body); err != nil {
			return fmt.Errorf("page %s: %w", page.Slug, err)
		}
		var out bytes.Buffer
		err = layout.Execute(&out, map[string]any{
			"Title": page.Title,
			"Slug":  page.Slug,
			"Nav":   Pages,
			"Body":  template.HTML(body.String()),
		})
		if err != nil {
			return fmt.Errorf("page %s: %w", page.Slug, err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, page.Slug+".html"), out.Bytes(), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// rewriteLinks maps repository-document references to their site pages
// so cross-links keep working when the markdown is rendered. Fenced
// code blocks are left untouched: a code example quoting a document
// name must not gain a link.
func rewriteLinks(source []byte) []byte {
	segments := strings.Split(string(source), "```")
	for i := range segments {
		if i%2 == 1 {
			continue // inside a fence
		}
		for _, page := range Pages {
			if strings.HasPrefix(page.Source, "docs/") {
				continue
			}
			name := filepath.Base(page.Source)
			segments[i] = strings.ReplaceAll(segments[i], "`"+name+"`", "["+name+"]("+page.Slug+".html)")
		}
	}
	return []byte(strings.Join(segments, "```"))
}
