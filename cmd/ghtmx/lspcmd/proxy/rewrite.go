package proxy

import (
	"log/slog"
	"path"
	"strings"
	"sync"

	"github.com/go-monolith/ghtmx/internal/config"
	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
)

// The template extension is project configuration, so these two
// conversions are methods: they must use the same extension the generator
// wrote with, or the proxy maps a document onto a Go file that does not
// exist.
func (p *Server) convertTemplToGoURI(templURI lsp.DocumentURI) (isTemplFile bool, goURI lsp.DocumentURI) {
	ext := p.templateExt()
	base, fileName := path.Split(string(templURI))
	if !strings.HasSuffix(fileName, ext) {
		return
	}
	return true, lsp.DocumentURI(base + (strings.TrimSuffix(fileName, ext) + "_ghtmx.go"))
}

// TemplateExtension carries the project's configured template extension to
// every URI mapping. The Server writes it once at Initialize, after
// ghtmx.json has been read; the Client reads it when converting
// diagnostics back. It is shared because the two proxies map URIs in
// opposite directions and must agree, or a diagnostic lands on a file name
// that was never generated.
type TemplateExtension struct {
	mu  sync.RWMutex
	ext string
}

// NewTemplateExtension returns a holder reading as the default until the
// project configuration is known.
func NewTemplateExtension() *TemplateExtension { return &TemplateExtension{} }

// Set is nil-safe for symmetry with Get: a Server assembled without
// NewServer has no holder to share, and Initialize must not panic on it.
// Such a server reads the default, which is what Get already returns.
func (t *TemplateExtension) Set(ext string) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ext = ext
}

func (t *TemplateExtension) Get() string {
	if t == nil {
		return config.DefaultTemplateExtension
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.ext == "" {
		return config.DefaultTemplateExtension
	}
	return t.ext
}

// templateExt is the configured template extension for this server.
func (p *Server) templateExt() string { return p.templateExtension.Get() }

// isPlainGoFile returns true if the URI refers to a .go file that is not a _ghtmx.go file.
func isPlainGoFile(docURI lsp.DocumentURI) bool {
	s := string(docURI)
	return strings.HasSuffix(s, ".go") && !strings.HasSuffix(s, "_ghtmx.go")
}

func convertTemplGoToTemplURI(ext string, goURI lsp.DocumentURI) (isTemplGoFile bool, templURI lsp.DocumentURI) {
	base, fileName := path.Split(string(goURI))
	if !strings.HasSuffix(fileName, "_ghtmx.go") {
		return
	}
	return true, lsp.DocumentURI(base + (strings.TrimSuffix(fileName, "_ghtmx.go") + ext))
}

// convertGoRangeToTemplRange converts a Go range to a templ range using the source map cache.
func convertGoRangeToTemplRange(cache *SourceMapCache, log *slog.Logger, templURI lsp.DocumentURI, input lsp.Range) (output lsp.Range) {
	output = input
	sourceMap, ok := cache.Get(string(templURI))
	if !ok {
		log.Warn("go->templ: sourcemap not found in cache",
			slog.String("uri", string(templURI)),
			slog.Any("cachedURIs", cache.URIs()))
		return
	}
	start, startMapped := sourceMap.SourcePositionFromTarget(input.Start.Line, input.Start.Character)
	if startMapped {
		output.Start.Line = start.Line
		output.Start.Character = start.Col
	}
	end, endMapped := sourceMap.SourcePositionFromTarget(input.End.Line, input.End.Character)
	if endMapped {
		output.End.Line = end.Line
		output.End.Character = end.Col
	}
	if !startMapped || !endMapped {
		log.Warn("go->templ: range not found in sourcemap",
			slog.String("uri", string(templURI)),
			slog.Any("range", input),
			slog.Bool("startMapped", startMapped),
			slog.Bool("endMapped", endMapped))
	}
	return
}

// convertLocationResults converts _ghtmx.go URIs and ranges in location results back to .ghtmx URIs.
func convertLocationResults(ext string, cache *SourceMapCache, log *slog.Logger, result []lsp.Location) {
	for i, r := range result {
		isTemplGoFile, templURI := convertTemplGoToTemplURI(ext, r.URI)
		if !isTemplGoFile {
			continue
		}
		log.Info("convertLocationResults: converting",
			slog.String("from", string(r.URI)),
			slog.String("to", string(templURI)),
			slog.Any("goRange", r.Range))
		result[i].URI = templURI
		result[i].Range = convertGoRangeToTemplRange(cache, log, templURI, r.Range)
		log.Info("convertLocationResults: converted",
			slog.Any("templRange", result[i].Range))
	}
}

// convertCallHierarchyItem converts a _ghtmx.go call hierarchy item back to .ghtmx.
func convertCallHierarchyItem(ext string, cache *SourceMapCache, log *slog.Logger, item *lsp.CallHierarchyItem) {
	isTemplGoFile, templURI := convertTemplGoToTemplURI(ext, item.URI)
	if !isTemplGoFile {
		return
	}
	item.URI = templURI
	item.Range = convertGoRangeToTemplRange(cache, log, templURI, item.Range)
	item.SelectionRange = convertGoRangeToTemplRange(cache, log, templURI, item.SelectionRange)
}

// convertWorkspaceEdit converts _ghtmx.go URIs and ranges in a workspace edit back to .ghtmx.
func convertWorkspaceEdit(ext string, cache *SourceMapCache, log *slog.Logger, edit *lsp.WorkspaceEdit) {
	if edit == nil {
		return
	}
	for i, dc := range edit.DocumentChanges {
		isTemplGoFile, templURI := convertTemplGoToTemplURI(ext, dc.TextDocument.URI)
		if !isTemplGoFile {
			continue
		}
		for j, e := range dc.Edits {
			dc.Edits[j].Range = convertGoRangeToTemplRange(cache, log, templURI, e.Range)
		}
		dc.TextDocument.URI = templURI
		edit.DocumentChanges[i] = dc
	}
	if edit.Changes == nil {
		return
	}
	converted := make(map[lsp.DocumentURI][]lsp.TextEdit)
	for docURI, edits := range edit.Changes {
		isTemplGoFile, templURI := convertTemplGoToTemplURI(ext, docURI)
		if !isTemplGoFile {
			converted[docURI] = edits
			continue
		}
		for i, e := range edits {
			edits[i].Range = convertGoRangeToTemplRange(cache, log, templURI, e.Range)
		}
		converted[templURI] = edits
	}
	edit.Changes = converted
}
