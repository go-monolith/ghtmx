package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/a-h/parse"
	"github.com/go-monolith/ghtmx/internal/format"
	"github.com/go-monolith/ghtmx/internal/imports"
	"github.com/go-monolith/ghtmx/internal/lazyloader"
	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
	"github.com/go-monolith/ghtmx/internal/lsp/uri"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/internal/analyzer"
	"github.com/go-monolith/ghtmx/internal/config"
	"github.com/go-monolith/ghtmx/internal/diag"
	"github.com/go-monolith/ghtmx/internal/generator"
	"github.com/go-monolith/ghtmx/internal/generator/central"
	"github.com/go-monolith/ghtmx/internal/htmxsurface"
	"github.com/go-monolith/ghtmx/internal/parser"
	"github.com/go-monolith/ghtmx/internal/routes"
)

// Server is responsible for rewriting messages that are
// originated from the text editor, and need to be sent to gopls.
//
// Since the editor is working on `templ` files, and `gopls` works
// on Go files, the job of this code is to rewrite incoming requests
// to adjust the file names from `*.ghtmx` to `*_ghtmx.go` and to
// remap the line/character positions in the `templ` files to their
// corresponding locations in the Go file.
//
// This allows gopls to operate as usual.
//
// This code also rewrites the responses back from gopls to do the
// inverse operation - to put the file names back, and readjust any
// character positions.
type Server struct {
	Log                *slog.Logger
	Target             lsp.Server
	SourceMapCache     *SourceMapCache
	DiagnosticCache    *DiagnosticCache
	TemplSource        *DocumentContents
	GoSource           map[string]string
	GoplsPath          string
	GoplsVersion       string
	NoPreload          bool
	preLoadURIs        []*lsp.DidOpenTextDocumentParams
	templDocLazyLoader lazyloader.TemplDocLazyLoader
	formatConf         format.Config
	// surface and severityOverrides drive the ghtmx analyzer passes so
	// live diagnostics match the CLI for the same source (FR-080). They
	// are written once in Initialize and read by later requests;
	// jsonrpc2.AsyncHandler chains each request on the previous one, so
	// no mutex is needed.
	surface           *htmxsurface.Surface
	severityOverrides map[string]diag.Severity
	// astCache is the URI-keyed store of each open document's parsed AST
	// (FR-080): every consumer shares this one parse — never a second
	// parser — and the whole-set analysis tasks build on it.
	astCache struct {
		mu    sync.Mutex
		byURI map[string]*parser.TemplateFile
	}
	// goSourceMu guards GoSource: the gopls restart replay reads it from
	// the supervisor goroutine while request handlers write it.
	goSourceMu sync.RWMutex
	// lastInitializeParams reproduces the session's Initialize on a gopls
	// restart (FR-085).
	initMu               sync.Mutex
	lastInitializeParams *lsp.InitializeParams
	// Route table, constructor naming, and the whole-set registry drive
	// the ghtmx completion providers (FR-081, FR-082). Discovered at
	// Initialize; setAnalysis refreshes as documents change.
	routeMu          sync.RWMutex
	routeTable       *routes.Table
	constructors     map[string]central.Constructor
	generatedPkgName string
	// templateExtension is the project's configured template extension,
	// written once in Initialize alongside the other config-derived
	// fields, and shared with the Client proxy.
	templateExtension *TemplateExtension
	setAnalysis       *analyzer.SetAnalysis
}

// LastInitializeParams returns the params the session initialized with,
// for gopls restart replay.
func (p *Server) LastInitializeParams() *lsp.InitializeParams {
	p.initMu.Lock()
	defer p.initMu.Unlock()
	return p.lastInitializeParams
}

// OpenGoDocuments snapshots the generated Go documents of every open
// template, as didOpen items for gopls restart replay (FR-085).
func (p *Server) OpenGoDocuments() []lsp.TextDocumentItem {
	p.goSourceMu.RLock()
	defer p.goSourceMu.RUnlock()
	items := make([]lsp.TextDocumentItem, 0, len(p.GoSource))
	for templURI, text := range p.GoSource {
		isTempl, goURI := p.convertTemplToGoURI(lsp.DocumentURI(templURI))
		if !isTempl {
			continue
		}
		items = append(items, lsp.TextDocumentItem{
			URI:        goURI,
			LanguageID: "go",
			Version:    1,
			Text:       text,
		})
	}
	return items
}

// NewServer requires the template-extension holder the Client proxy also
// holds. Passing nil is a wiring mistake, and both ways of tolerating it
// are worse than failing: nil makes Set a no-op, so the server ignores the
// project's configured extension, and substituting a fresh holder is
// quieter still — Set would appear to work while the Client kept reading
// the default, leaving the two proxies mapping URIs to different file
// names. Neither shows up until a .htmx project gets no diagnostics.
func NewServer(log *slog.Logger, target lsp.Server, cache *SourceMapCache, diagnosticCache *DiagnosticCache, noPreload bool, formatConf format.Config, templateExtension *TemplateExtension) (s *Server) {
	if templateExtension == nil {
		panic("proxy.NewServer: templateExtension must not be nil; share the holder given to NewClient")
	}
	return &Server{
		Log:               log,
		Target:            target,
		SourceMapCache:    cache,
		DiagnosticCache:   diagnosticCache,
		TemplSource:       newDocumentContents(log),
		GoSource:          make(map[string]string),
		NoPreload:         noPreload,
		formatConf:        formatConf,
		templateExtension: templateExtension,
	}
}

// updatePosition maps positions and filenames from source templ files into the target *.go files.
func (p *Server) updatePosition(templURI lsp.DocumentURI, current lsp.Position) (ok bool, goURI lsp.DocumentURI, updated lsp.Position) {
	log := p.Log.With(slog.String("uri", string(templURI)))
	var isTemplFile bool
	if isTemplFile, goURI = p.convertTemplToGoURI(templURI); !isTemplFile {
		return false, templURI, current
	}
	sourceMap, ok := p.SourceMapCache.Get(string(templURI))
	if !ok {
		log.Warn("completion: sourcemap not found in cache, it could be that didOpen was not called")
		return
	}
	// Map from the source position to target Go position.
	to, ok := sourceMap.TargetPositionFromSource(current.Line, current.Character)
	if !ok {
		log.Info("updatePosition: not found", slog.String("from", fmt.Sprintf("%d:%d", current.Line, current.Character)))
		return false, templURI, current
	}
	log.Info("updatePosition: found", slog.String("fromTempl", fmt.Sprintf("%d:%d", current.Line, current.Character)),
		slog.String("toGo", fmt.Sprintf("%d:%d", to.Line, to.Col)))
	updated.Line = to.Line
	updated.Character = to.Col

	return true, goURI, updated
}

func (p *Server) convertTemplRangeToGoRange(templURI lsp.DocumentURI, input lsp.Range) (output lsp.Range, ok bool) {
	output = input
	var sourceMap *parser.SourceMap
	sourceMap, ok = p.SourceMapCache.Get(string(templURI))
	if !ok {
		p.Log.Warn("templ->go: sourcemap not found in cache")
		return
	}
	// Map from the source position to target Go position.
	start, ok := sourceMap.TargetPositionFromSource(input.Start.Line, input.Start.Character)
	if ok {
		output.Start.Line = start.Line
		output.Start.Character = start.Col
	}
	end, ok := sourceMap.TargetPositionFromSource(input.End.Line, input.End.Character)
	if ok {
		output.End.Line = end.Line
		output.End.Character = end.Col
	}
	return
}

// proxyPositionRequest determines how to forward a position-based request.
// For .ghtmx files, it converts the position to the corresponding Go position.
// For non-templ files (e.g. .go), it returns the position unchanged so the
// request can be forwarded to gopls as-is.
// Returns ok=false only when position mapping fails for a .ghtmx file.
func (p *Server) proxyPositionRequest(templURI lsp.DocumentURI, pos lsp.Position) (isTempl bool, goURI lsp.DocumentURI, goPos lsp.Position, ok bool) {
	isTemplFile, goFileURI := p.convertTemplToGoURI(templURI)
	if !isTemplFile {
		return false, templURI, pos, true
	}
	sourceMap, smOk := p.SourceMapCache.Get(string(templURI))
	if !smOk {
		p.Log.Warn("proxyPositionRequest: sourcemap not found in cache", slog.String("uri", string(templURI)))
		return true, templURI, pos, false
	}
	to, toOk := sourceMap.TargetPositionFromSource(pos.Line, pos.Character)
	if !toOk {
		p.Log.Info("proxyPositionRequest: position not found in sourcemap",
			slog.String("from", fmt.Sprintf("%d:%d", pos.Line, pos.Character)))
		return true, templURI, pos, false
	}
	return true, goFileURI, lsp.Position{Line: to.Line, Character: to.Col}, true
}

// parseTemplate parses the templ file content, and notifies the end user via the LSP about how it went.
func (p *Server) parseTemplate(ctx context.Context, uri uri.URI, templateText string) (template *parser.TemplateFile, ok bool, err error) {
	template, err = parser.ParseString(templateText)
	if err != nil {
		msg := &lsp.PublishDiagnosticsParams{
			URI: uri,
			Diagnostics: []lsp.Diagnostic{
				{
					Severity: lsp.DiagnosticSeverityError,
					Code:     "",
					Source:   "templ",
					Message:  err.Error(),
				},
			},
		}
		if pe, isParserError := err.(parse.ParseError); isParserError {
			msg.Diagnostics[0].Range = lsp.Range{
				Start: lsp.Position{
					Line:      uint32(pe.Pos.Line),
					Character: uint32(pe.Pos.Col),
				},
				End: lsp.Position{
					Line:      uint32(pe.Pos.Line),
					Character: uint32(pe.Pos.Col),
				},
			}
		}
		msg.Diagnostics = p.DiagnosticCache.AddGoDiagnostics(string(uri), msg.Diagnostics)
		err = lsp.ClientFromContext(ctx).PublishDiagnostics(ctx, msg)
		if err != nil {
			p.Log.Error("failed to publish error diagnostics", slog.Any("error", err))
		}
		// If the template was even partially parsed, it's still potentially useful.
		if template != nil {
			template.Filepath = string(uri)
		}
		return
	}
	template.Filepath = string(uri)
	if p.setAnalysis != nil {
		p.setAnalysis.CollectFile(template)
	}
	p.astCache.mu.Lock()
	if p.astCache.byURI == nil {
		p.astCache.byURI = map[string]*parser.TemplateFile{}
	}
	p.astCache.byURI[string(uri)] = template
	p.astCache.mu.Unlock()
	parsedDiagnostics, err := parser.Diagnose(template)
	if err != nil {
		return
	}
	ok = true
	diagnostics := make([]lsp.Diagnostic, 0, len(parsedDiagnostics))
	for _, d := range parsedDiagnostics {
		diagnostics = append(diagnostics, lsp.Diagnostic{
			Severity: lsp.DiagnosticSeverityWarning,
			Code:     "",
			Source:   "templ",
			Message:  d.Message,
			Range: lsp.Range{
				Start: lsp.Position{
					Line:      uint32(d.Range.From.Line),
					Character: uint32(d.Range.From.Col),
				},
				End: lsp.Position{
					Line:      uint32(d.Range.To.Line),
					Character: uint32(d.Range.To.Col),
				},
			},
		})
	}
	// The ghtmx analyzer runs on the same shared AST — never a second
	// parse — so live diagnostics match the CLI's set and severities for
	// the same source (FR-080).
	diagnostics = append(diagnostics, p.ghtmxDiagnostics(template)...)
	if len(diagnostics) > 0 {
		msg := &lsp.PublishDiagnosticsParams{
			URI:         uri,
			Diagnostics: p.DiagnosticCache.AddGoDiagnostics(string(uri), diagnostics),
		}
		err = lsp.ClientFromContext(ctx).PublishDiagnostics(ctx, msg)
		if err != nil {
			p.Log.Error("failed to publish error diagnostics", slog.Any("error", err))
		}
		return
	}
	// A resolved issue must drop its diagnostic: publishing through the
	// cache with an empty templ set clears our side while preserving any
	// cached gopls diagnostics.
	err = lsp.ClientFromContext(ctx).PublishDiagnostics(ctx, &lsp.PublishDiagnosticsParams{
		URI: uri,
		// Cannot be nil as per https://microsoft.github.io/language-server-protocol/specifications/lsp/3.17/specification/#publishDiagnosticsParams
		Diagnostics: p.DiagnosticCache.AddGoDiagnostics(string(uri), nil),
	})
	if err != nil {
		p.Log.Error("failed to publish diagnostics", slog.Any("error", err))
		return
	}
	return
}

// loadAnalyzerConfig resolves the workspace's ghtmx.json so live
// diagnostics use the same htmx surface and severity overrides as the
// CLI. Failures fall back to defaults and are logged, never fatal.
// TODO: reload on DidChangeWatchedFiles so mid-session ghtmx.json edits,
// Go route-registration changes, and template deletions reach diagnostics
// and completion without an editor restart.
func (p *Server) loadAnalyzerConfig(params *lsp.InitializeParams) {
	root := ""
	if params != nil && params.RootURI != "" {
		root = params.RootURI.Filename()
	}
	if root == "" && params != nil && len(params.WorkspaceFolders) > 0 {
		root = uri.New(params.WorkspaceFolders[0].URI).Filename()
	}
	cfg := config.Default()
	if root != "" {
		loaded, err := config.Load(root)
		if err != nil {
			p.Log.Warn("ghtmx.json not usable; using defaults", slog.Any("error", err))
		} else {
			cfg = config.Resolve(loaded, config.Flags{})
		}
	}
	p.severityOverrides = cfg.SeverityOverrides()
	p.generatedPkgName = cfg.GeneratedPackage.Name
	p.templateExtension.Set(cfg.TemplateExtension)
	p.setAnalysis = analyzer.NewSetAnalysis()
	if root != "" {
		// Route discovery feeds route-aware completion (FR-081); a
		// failure degrades completion only.
		sink := diag.NewSink(nil)
		if pkgs, err := routes.Load(root, cfg.RouteScope, sink); err == nil {
			table := routes.Discover(pkgs, sink)
			byName, _ := central.Naming(table)
			p.routeMu.Lock()
			p.routeTable = table
			p.constructors = byName
			p.routeMu.Unlock()
		} else {
			p.Log.Warn("route discovery failed; route completion disabled", slog.Any("error", err))
		}
		// Seed the event registry from the workspace so event-name
		// completion works before files are opened (FR-082).
		p.seedEventRegistry(root)
	}
	surface, err := htmxsurface.ForVersion(cfg.HtmxVersion)
	if err != nil {
		p.Log.Warn("hx-* attribute diagnostics disabled", slog.Any("error", err))
		return
	}
	p.surface = surface
}

// seedEventRegistry parses the workspace's templates once so the event
// registry is populated at startup; open-document edits keep it fresh.
func (p *Server) seedEventRegistry(root string) {
	count := 0
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if count >= 2000 {
			return filepath.SkipAll
		}
		if !strings.HasSuffix(path, p.templateExt()) {
			return nil
		}
		count++
		t, err := parser.Parse(path)
		if err != nil || t == nil {
			return nil
		}
		// Key by the URI form didOpen will use, so live edits replace the
		// seeded entry instead of shadowing it.
		t.Filepath = string(uri.File(path))
		p.setAnalysis.CollectFile(t)
		return nil
	})
}

// ghtmxDiagnostics runs the per-file analyzer passes the CLI runs and
// converts their diagnostics to LSP form (1-based to 0-based positions).
func (p *Server) ghtmxDiagnostics(template *parser.TemplateFile) []lsp.Diagnostic {
	sink := diag.NewSink(p.severityOverrides)
	if p.surface != nil {
		analyzer.ValidateAttributes(template, p.surface, sink)
	}
	analyzer.ValidateFragments(template, sink)
	ds := sink.Diagnostics()
	out := make([]lsp.Diagnostic, 0, len(ds))
	for _, d := range ds {
		severity := lsp.DiagnosticSeverityWarning
		if d.Severity == diag.Error {
			severity = lsp.DiagnosticSeverityError
		}
		line, col := uint32(0), uint32(0)
		if d.Pos.Line > 0 {
			line = uint32(d.Pos.Line - 1)
		}
		if d.Pos.Col > 0 {
			col = uint32(d.Pos.Col - 1)
		}
		out = append(out, lsp.Diagnostic{
			Severity: severity,
			Code:     d.ID,
			Source:   "ghtmx",
			Message:  d.Message,
			Range: lsp.Range{
				Start: lsp.Position{Line: line, Character: col},
				End:   lsp.Position{Line: line, Character: col},
			},
		})
	}
	return out
}

func (p *Server) Initialize(ctx context.Context, params *lsp.InitializeParams) (result *lsp.InitializeResult, err error) {
	p.Log.Info("client -> server: Initialize")
	defer p.Log.Info("client -> server: Initialize end")
	p.initMu.Lock()
	p.lastInitializeParams = params
	p.initMu.Unlock()
	p.loadAnalyzerConfig(params)
	result, err = p.Target.Initialize(ctx, params)
	if err != nil || result == nil {
		p.Log.Error("Initialize failed", slog.Any("error", err))
		if result == nil {
			result = &lsp.InitializeResult{}
		}
	}
	// Add the '<' and '{' trigger so that we can do snippets for tags.
	if result.Capabilities.CompletionProvider == nil {
		result.Capabilities.CompletionProvider = &lsp.CompletionOptions{}
	}
	result.Capabilities.CompletionProvider.TriggerCharacters = append(result.Capabilities.CompletionProvider.TriggerCharacters, "{", "<")
	// Remove all the gopls commands.
	if result.Capabilities.ExecuteCommandProvider == nil {
		result.Capabilities.ExecuteCommandProvider = &lsp.ExecuteCommandOptions{}
	}
	result.Capabilities.ExecuteCommandProvider.Commands = []string{}
	result.Capabilities.DocumentFormattingProvider = true
	result.Capabilities.SemanticTokensProvider = nil
	result.Capabilities.DocumentRangeFormattingProvider = false
	result.Capabilities.TextDocumentSync = lsp.TextDocumentSyncOptions{
		OpenClose:         true,
		Change:            lsp.TextDocumentSyncKindFull,
		WillSave:          false,
		WillSaveWaitUntil: false,
		Save:              &lsp.SaveOptions{IncludeText: true},
	}

	if p.NoPreload {
		// NOTE: the lazy loader holds the raw map; its accesses run on the
		// serialized request path, but a gopls restart replay snapshots
		// through OpenGoDocuments (goSourceMu) rather than this reference.
		p.templDocLazyLoader = lazyloader.New(lazyloader.NewParams{
			TemplDocHandler: p,
			OpenDocSources:  p.GoSource,
		})
	} else {
		p.preload(ctx, params.WorkspaceFolders)
	}

	result.ServerInfo.Name = "ghtmx-lsp"
	result.ServerInfo.Version = ghtmx.Version()
	// Advertise Go file support so editors can register for .go files
	// and route them through the proxy for cross-file navigation.
	result.Capabilities.Experimental = map[string]any{
		"templ": map[string]any{
			"goFileSupport": true,
		},
	}

	return result, err
}

func (p *Server) preload(ctx context.Context, workspaceFolders []lsp.WorkspaceFolder) {
	for _, c := range workspaceFolders {
		path, err := uri.ParseDocumentURI(c.URI)
		if err != nil {
			p.Log.Error("invalid uri", slog.String("uri", c.URI))
			continue
		}

		werr := filepath.Walk(path.Filename(), func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			p.Log.Info("found file", slog.String("path", path))
			uri := uri.URIFromPath(path)
			isTemplFile, goURI := p.convertTemplToGoURI(uri)

			if !isTemplFile {
				return nil
			}

			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			p.TemplSource.Set(string(uri), NewDocument(p.Log, string(b)))
			// Parse the template.
			template, _, err := p.parseTemplate(ctx, uri, string(b))
			if err != nil {
				// It's expected to have some failures while parsing the template, since
				// you are likely to have invalid docs while you're typing.
				p.Log.Info("parseTemplate failure", slog.Any("error", err))
			}
			w := new(strings.Builder)
			generatorOutput, err := generator.Generate(template, w)
			if err != nil {
				// It's expected to have some failures while generating code from the template, since
				// you are likely to have invalid docs while you're typing.
				p.Log.Info("generator failure", slog.Any("error", err))
			}
			p.Log.Info("setting source map cache contents", slog.String("uri", string(uri)))
			p.SourceMapCache.Set(string(uri), generatorOutput.SourceMap)
			// Set the Go contents.
			p.goSourceMu.Lock()
			p.GoSource[string(uri)] = w.String()
			p.goSourceMu.Unlock()

			didOpenParams := &lsp.DidOpenTextDocumentParams{
				TextDocument: lsp.TextDocumentItem{
					URI:        goURI,
					Text:       w.String(),
					Version:    1,
					LanguageID: "go",
				},
			}

			p.preLoadURIs = append(p.preLoadURIs, didOpenParams)
			return nil
		})
		if werr != nil {
			p.Log.Error("walk error", slog.Any("error", werr))
		}
	}
}

func (p *Server) Initialized(ctx context.Context, params *lsp.InitializedParams) (err error) {
	p.Log.Info("client -> server: Initialized")
	defer p.Log.Info("client -> server: Initialized end")
	goInitErr := p.Target.Initialized(ctx, params)

	p.notifyGoplsVersion(ctx)

	for i, doParams := range p.preLoadURIs {
		doErr := p.Target.DidOpen(ctx, doParams)
		if doErr != nil {
			return doErr
		}
		p.preLoadURIs[i] = nil
	}

	return goInitErr
}

func (p *Server) notifyGoplsVersion(ctx context.Context) {
	client := lsp.ClientFromContext(ctx)
	if client == nil {
		return
	}

	if p.GoplsVersion == "" {
		_ = client.LogMessage(ctx, &lsp.LogMessageParams{
			Type:    lsp.MessageTypeWarning,
			Message: fmt.Sprintf("ghtmx: could not determine gopls version at %s. If you experience errors, upgrade with: go install golang.org/x/tools/gopls@latest", p.GoplsPath),
		})
		return
	}

	_ = client.LogMessage(ctx, &lsp.LogMessageParams{
		Type:    lsp.MessageTypeInfo,
		Message: fmt.Sprintf("ghtmx: using gopls %s (%s)", p.GoplsVersion, p.GoplsPath),
	})
}

func (p *Server) Shutdown(ctx context.Context) (err error) {
	p.Log.Info("client -> server: Shutdown")
	defer p.Log.Info("client -> server: Shutdown end")
	return p.Target.Shutdown(ctx)
}

func (p *Server) Exit(ctx context.Context) (err error) {
	p.Log.Info("client -> server: Exit")
	defer p.Log.Info("client -> server: Exit end")
	return p.Target.Exit(ctx)
}

func (p *Server) WorkDoneProgressCancel(ctx context.Context, params *lsp.WorkDoneProgressCancelParams) (err error) {
	p.Log.Info("client -> server: WorkDoneProgressCancel")
	defer p.Log.Info("client -> server: WorkDoneProgressCancel end")
	return p.Target.WorkDoneProgressCancel(ctx, params)
}

func (p *Server) LogTrace(ctx context.Context, params *lsp.LogTraceParams) (err error) {
	p.Log.Info("client -> server: LogTrace", slog.String("message", params.Message))
	defer p.Log.Info("client -> server: LogTrace end")
	return p.Target.LogTrace(ctx, params)
}

func (p *Server) SetTrace(ctx context.Context, params *lsp.SetTraceParams) (err error) {
	p.Log.Info("client -> server: SetTrace")
	defer p.Log.Info("client -> server: SetTrace end")
	return p.Target.SetTrace(ctx, params)
}

var supportedCodeActions = map[string]bool{
	"Organize Imports": true,
}

func (p *Server) CodeAction(ctx context.Context, params *lsp.CodeActionParams) (result []lsp.CodeAction, err error) {
	p.Log.Info("client -> server: CodeAction", slog.Any("params", params))
	defer p.Log.Info("client -> server: CodeAction end")

	if p.NoPreload && !p.templDocLazyLoader.HasLoaded(params.TextDocument) {
		p.Log.Error("lazy loader has not loaded document", slog.Any("params", params))
		return nil, nil
	}

	templURI, err := uri.ParseDocumentURI(string(params.TextDocument.URI))
	if err != nil {
		p.Log.Error("invalid uri", slog.String("uri", string(params.TextDocument.URI)))
		return
	}
	isTemplFile, goURI := p.convertTemplToGoURI(templURI)
	if !isTemplFile {
		return nil, nil
	}
	var ok bool
	if params.Range, ok = p.convertTemplRangeToGoRange(templURI, params.Range); !ok {
		// Don't pass the request to gopls if the range is not within a Go code block.
		return
	}
	params.TextDocument.URI = goURI
	result, err = p.Target.CodeAction(ctx, params)
	if err != nil {
		return
	}
	var updatedResults []lsp.CodeAction
	// Filter out commands that are not yet supported.
	// For example, "Fill Struct" runs the `gopls.apply_fix` command.
	// This command has a set of arguments, including Fix, Range and URI.
	// However, these are just a map[string]any so for each command that we want to support,
	// we need to know what the arguments are so that we can rewrite them.
	for _, r := range result {
		if !supportedCodeActions[r.Title] {
			continue
		}
		for di, diag := range r.Diagnostics {
			r.Diagnostics[di].Range = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, diag.Range)
		}
		convertWorkspaceEdit(p.templateExt(), p.SourceMapCache, p.Log, r.Edit)
		updatedResults = append(updatedResults, r)
	}
	return updatedResults, nil
}

func (p *Server) CodeLens(ctx context.Context, params *lsp.CodeLensParams) (result []lsp.CodeLens, err error) {
	p.Log.Info("client -> server: CodeLens")
	defer p.Log.Info("client -> server: CodeLens end")
	templURI, err := uri.ParseDocumentURI(string(params.TextDocument.URI))
	if err != nil {
		p.Log.Error("invalid uri", slog.String("uri", string(params.TextDocument.URI)))
		return
	}
	isTemplFile, goURI := p.convertTemplToGoURI(templURI)
	if !isTemplFile {
		return p.Target.CodeLens(ctx, params)
	}
	params.TextDocument.URI = goURI
	result, err = p.Target.CodeLens(ctx, params)
	if err != nil {
		return
	}
	if result == nil {
		return
	}
	for i, cl := range result {
		cl.Range = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, cl.Range)
		result[i] = cl
	}
	return
}

func (p *Server) CodeLensResolve(ctx context.Context, params *lsp.CodeLens) (result *lsp.CodeLens, err error) {
	p.Log.Info("client -> server: CodeLensResolve")
	defer p.Log.Info("client -> server: CodeLensResolve end")
	return p.Target.CodeLensResolve(ctx, params)
}

func (p *Server) ColorPresentation(ctx context.Context, params *lsp.ColorPresentationParams) (result []lsp.ColorPresentation, err error) {
	p.Log.Info("client -> server: ColorPresentation ColorPresentation")
	defer p.Log.Info("client -> server: ColorPresentation end")
	templURI, err := uri.ParseDocumentURI(string(params.TextDocument.URI))
	if err != nil {
		p.Log.Error("invalid uri", slog.String("uri", string(params.TextDocument.URI)))
		return
	}
	isTemplFile, goURI := p.convertTemplToGoURI(templURI)
	if !isTemplFile {
		return p.Target.ColorPresentation(ctx, params)
	}
	params.TextDocument.URI = goURI
	result, err = p.Target.ColorPresentation(ctx, params)
	if err != nil {
		return
	}
	if result == nil {
		return
	}
	for i, r := range result {
		if r.TextEdit != nil {
			r.TextEdit.Range = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, r.TextEdit.Range)
		}
		result[i] = r
	}
	return
}

func (p *Server) Completion(ctx context.Context, params *lsp.CompletionParams) (result *lsp.CompletionList, err error) {
	p.Log.Info("client -> server: Completion")
	defer p.Log.Info("client -> server: Completion end")
	isTemplFile, _ := p.convertTemplToGoURI(params.TextDocument.URI)
	if !isTemplFile {
		return nil, nil
	}
	if params.Context != nil && params.Context.TriggerCharacter == "<" {
		result = &lsp.CompletionList{
			Items: htmlSnippets,
		}
		return
	}
	// Get the sourcemap from the cache.
	templURI, err := uri.ParseDocumentURI(string(params.TextDocument.URI))
	if err != nil {
		p.Log.Error("invalid uri", slog.String("uri", string(params.TextDocument.URI)))
		return
	}
	// ghtmx contexts (attribute names and values, event listeners, verb
	// bindings) complete from the engine's own registries (FR-081,
	// FR-082); everything else proxies to gopls.
	ghtmxItems, exclusive := p.ghtmxCompletions(string(templURI), params.Position)
	if exclusive && len(ghtmxItems) > 0 {
		return &lsp.CompletionList{Items: ghtmxItems}, nil
	}
	defer func() {
		// Verb expressions are Go code too: engine items merge with the
		// gopls result rather than replacing it.
		if len(ghtmxItems) == 0 {
			return
		}
		if result == nil {
			result = &lsp.CompletionList{}
		}
		result.Items = append(result.Items, ghtmxItems...)
	}()
	var ok bool
	ok, params.TextDocument.URI, params.Position = p.updatePosition(templURI, params.Position)
	if !ok {
		return nil, nil
	}

	// Ensure that Go source is available.
	p.goSourceMu.RLock()
	goSourceText := p.GoSource[string(templURI)]
	p.goSourceMu.RUnlock()
	gosrc := strings.Split(goSourceText, "\n")
	if len(gosrc) < int(params.Position.Line) {
		p.Log.Info("completion: line position out of range")
		return nil, nil
	}
	if len(gosrc[params.Position.Line]) < int(params.Position.Character) {
		p.Log.Info("completion: col position out of range")
		return nil, nil
	}

	// Call the target.
	result, err = p.Target.Completion(ctx, params)
	if err != nil {
		p.Log.Warn("completion: got gopls error", slog.Any("error", err))
		return
	}
	if result == nil {
		return
	}
	// Rewrite the result positions.
	p.Log.Info("completion: received items", slog.Int("count", len(result.Items)))

	for i, item := range result.Items {
		item.FilterText = stripTemplStringable(item.FilterText)
		if item.TextEdit != nil {
			if item.TextEdit.TextEdit != nil {
				item.TextEdit.TextEdit.Range = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, item.TextEdit.TextEdit.Range)
				item.TextEdit.TextEdit.NewText = stripTemplStringable(item.TextEdit.TextEdit.NewText)
			}
			if item.TextEdit.InsertReplaceEdit != nil {
				item.TextEdit.InsertReplaceEdit.Insert = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, item.TextEdit.InsertReplaceEdit.Insert)
				item.TextEdit.InsertReplaceEdit.Replace = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, item.TextEdit.InsertReplaceEdit.Replace)
				item.TextEdit.InsertReplaceEdit.NewText = stripTemplStringable(item.TextEdit.InsertReplaceEdit.NewText)
			}
		}
		if len(item.AdditionalTextEdits) > 0 {
			doc, ok := p.TemplSource.Get(string(templURI))
			if !ok {
				continue
			}
			pkg := getPackageFromItemDetail(item.Detail)
			imp := addImport(doc.Lines, pkg)
			item.AdditionalTextEdits = []lsp.TextEdit{
				{
					Range: lsp.Range{
						Start: lsp.Position{Line: uint32(imp.LineIndex), Character: 0},
						End:   lsp.Position{Line: uint32(imp.LineIndex), Character: 0},
					},
					NewText: imp.Text,
				},
			}
		}
		result.Items[i] = item
	}

	// Add templ snippet.
	result.Items = append(result.Items, snippet...)

	return
}

// The LSP attempts to insert `ghtmx.stringable(variable)` as a completion, but this isn't required.
func stripTemplStringable(s string) string {
	if !strings.HasPrefix(s, "ghtmx.stringable(") {
		return s
	}
	s = strings.TrimPrefix(s, "ghtmx.stringable(")
	s = strings.TrimSuffix(s, ")")
	return s
}

var completionWithImport = regexp.MustCompile(`^.*\(from\s(".+")\)$`)

func getPackageFromItemDetail(pkg string) string {
	if m := completionWithImport.FindStringSubmatch(pkg); len(m) == 2 {
		return m[1]
	}
	return pkg
}

type importInsert struct {
	Text      string
	LineIndex int
}

var nonImportKeywordRegexp = regexp.MustCompile(`^(?:templ|func|css|script|var|const|type)\s`)

func addImport(lines []string, pkg string) (result importInsert) {
	var isInMultiLineImport bool
	lastSingleLineImportIndex := -1
	for lineIndex, line := range lines {
		if strings.HasPrefix(line, "import (") {
			isInMultiLineImport = true
			continue
		}
		if strings.HasPrefix(line, "import \"") {
			lastSingleLineImportIndex = lineIndex
			continue
		}
		if isInMultiLineImport && strings.HasPrefix(line, ")") {
			return importInsert{
				LineIndex: lineIndex,
				Text:      fmt.Sprintf("\t%s\n", pkg),
			}
		}
		// Only add import statements before templates, functions, css, and script templates.
		if nonImportKeywordRegexp.MatchString(line) {
			break
		}
	}
	var suffix string
	if lastSingleLineImportIndex == -1 {
		lastSingleLineImportIndex = 1
		suffix = "\n"
	}
	return importInsert{
		LineIndex: lastSingleLineImportIndex + 1,
		Text:      fmt.Sprintf("import %s\n%s", pkg, suffix),
	}
}

func (p *Server) CompletionResolve(ctx context.Context, params *lsp.CompletionItem) (result *lsp.CompletionItem, err error) {
	p.Log.Info("client -> server: CompletionResolve")
	defer p.Log.Info("client -> server: CompletionResolve end")
	return p.Target.CompletionResolve(ctx, params)
}

func (p *Server) Declaration(ctx context.Context, params *lsp.DeclarationParams) (result []lsp.Location /* Declaration | DeclarationLink[] | null */, err error) {
	p.Log.Info("client -> server: Declaration")
	defer p.Log.Info("client -> server: Declaration end")
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	result, err = p.Target.Declaration(ctx, params)
	if err != nil || result == nil {
		return
	}
	convertLocationResults(p.templateExt(), p.SourceMapCache, p.Log, result)
	return
}

func (p *Server) Definition(ctx context.Context, params *lsp.DefinitionParams) (result []lsp.Location /* Definition | DefinitionLink[] | null */, err error) {
	p.Log.Info("client -> server: Definition")
	defer p.Log.Info("client -> server: Definition end")
	// ghtmx constructs resolve against the engine's registries: event
	// references to .ghtmx declarations, bound symbols to their Go
	// registration sites (FR-084).
	if templURI, parseErr := uri.ParseDocumentURI(string(params.TextDocument.URI)); parseErr == nil {
		if locations, handled := p.ghtmxDefinition(string(templURI), params.Position); handled {
			return locations, nil
		}
	}
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	result, err = p.Target.Definition(ctx, params)
	if err != nil || result == nil {
		return
	}
	convertLocationResults(p.templateExt(), p.SourceMapCache, p.Log, result)
	return
}

func (p *Server) DidChange(ctx context.Context, params *lsp.DidChangeTextDocumentParams) (err error) {
	p.Log.Info("client -> server: DidChange", slog.Any("params", params))
	defer p.Log.Info("client -> server: DidChange end")
	templURI, err := uri.ParseDocumentURI(string(params.TextDocument.URI))
	if err != nil {
		p.Log.Error("invalid uri", slog.String("uri", string(params.TextDocument.URI)))
		return
	}
	isTemplFile, goURI := p.convertTemplToGoURI(templURI)
	if !isTemplFile {
		return p.Target.DidChange(ctx, params)
	}
	// Apply content changes to the cached template.
	d, err := p.TemplSource.Apply(string(templURI), params.ContentChanges)
	if err != nil {
		p.Log.Error("error applying changes", slog.Any("error", err))
		return
	}
	// Update the Go code.
	p.Log.Info("parsing template")
	template, ok, err := p.parseTemplate(ctx, templURI, d.String())
	if err != nil {
		p.Log.Error("parseTemplate failure", slog.Any("error", err))
	}
	if !ok {
		p.Log.Info("parseTemplate not OK, but attempting to generate anyway")
	}
	// Even if the template isn't parsed successfully, attempt to generate, because we
	// need the LSP to have an up-to-date view of completions.
	w := new(strings.Builder)
	// In future updates, we may pass `WithSkipCodeGeneratedComment` to the generator.
	// This will enable a number of actions within gopls that it doesn't currently apply because
	// it recognises templ code as being auto-generated.
	//
	// This change would increase the surface area of gopls that we use, so may surface a number of issues
	// if enabled.
	generatorOutput, err := generator.Generate(template, w)
	if err != nil {
		p.Log.Error("generate failure", slog.Any("error", err))
		return
	}
	// Cache the sourcemap.
	p.Log.Info("setting cache", slog.String("uri", string(templURI)))
	p.SourceMapCache.Set(string(templURI), generatorOutput.SourceMap)
	p.goSourceMu.Lock()
	p.GoSource[string(templURI)] = w.String()
	p.goSourceMu.Unlock()

	if p.NoPreload {
		params.TextDocument.URI = templURI
		if err := p.templDocLazyLoader.Sync(ctx, params); err != nil {
			p.Log.Error("lazy loader sync", slog.Any("error", err))
		}
	}

	// Change the path.
	params.TextDocument.URI = goURI
	// Overwrite all the Go contents.
	params.ContentChanges = []lsp.TextDocumentContentChangeEvent{{
		Text: w.String(),
	}}
	return p.Target.DidChange(ctx, params)
}

func (p *Server) DidChangeConfiguration(ctx context.Context, params *lsp.DidChangeConfigurationParams) (err error) {
	p.Log.Info("client -> server: DidChangeConfiguration")
	defer p.Log.Info("client -> server: DidChangeConfiguration end")
	return p.Target.DidChangeConfiguration(ctx, params)
}

func (p *Server) DidChangeWatchedFiles(ctx context.Context, params *lsp.DidChangeWatchedFilesParams) (err error) {
	p.Log.Info("client -> server: DidChangeWatchedFiles")
	defer p.Log.Info("client -> server: DidChangeWatchedFiles end")
	return p.Target.DidChangeWatchedFiles(ctx, params)
}

func (p *Server) DidChangeWorkspaceFolders(ctx context.Context, params *lsp.DidChangeWorkspaceFoldersParams) (err error) {
	p.Log.Info("client -> server: DidChangeWorkspaceFolders")
	defer p.Log.Info("client -> server: DidChangeWorkspaceFolders end")
	return p.Target.DidChangeWorkspaceFolders(ctx, params)
}

func (p *Server) DidClose(ctx context.Context, params *lsp.DidCloseTextDocumentParams) (err error) {
	p.Log.Info("client -> server: DidClose")
	defer p.Log.Info("client -> server: DidClose end")

	if p.NoPreload {
		templURI, err := uri.ParseDocumentURI(string(params.TextDocument.URI))
		if err != nil {
			p.Log.Error("invalid uri", slog.String("uri", string(params.TextDocument.URI)))
			return err
		}
		if isTemplFile, _ := p.convertTemplToGoURI(templURI); !isTemplFile {
			return p.Target.DidClose(ctx, params)
		}
		params.TextDocument.URI = templURI
		return p.templDocLazyLoader.Unload(ctx, params)
	}

	return p.HandleDidClose(ctx, params)
}

func (p *Server) HandleDidClose(ctx context.Context, params *lsp.DidCloseTextDocumentParams) (err error) {
	templURI, err := uri.ParseDocumentURI(string(params.TextDocument.URI))
	if err != nil {
		p.Log.Error("invalid uri", slog.String("uri", string(params.TextDocument.URI)))
		return
	}
	isTemplFile, goURI := p.convertTemplToGoURI(templURI)
	if !isTemplFile {
		return p.Target.DidClose(ctx, params)
	}
	// Delete the template and sourcemaps from caches; GoSource too, or a
	// gopls restart would replay documents the editor closed.
	p.TemplSource.Delete(string(templURI))
	p.SourceMapCache.Delete(string(templURI))
	p.goSourceMu.Lock()
	delete(p.GoSource, string(templURI))
	p.goSourceMu.Unlock()
	// Get gopls to delete the Go file from its cache.
	params.TextDocument.URI = goURI
	return p.Target.DidClose(ctx, params)
}

func (p *Server) DidOpen(ctx context.Context, params *lsp.DidOpenTextDocumentParams) (err error) {
	templURI, err := uri.ParseDocumentURI(string(params.TextDocument.URI))
	if err != nil {
		p.Log.Error("invalid uri", slog.String("uri", string(params.TextDocument.URI)))
		return
	}
	p.Log.Info("client -> server: DidOpen", slog.String("uri", string(templURI)))
	defer p.Log.Info("client -> server: DidOpen end")

	if p.NoPreload {
		if isTemplFile, _ := p.convertTemplToGoURI(templURI); !isTemplFile {
			return p.Target.DidOpen(ctx, params)
		}
		params.TextDocument.URI = templURI
		return p.templDocLazyLoader.Load(ctx, params)
	}

	return p.HandleDidOpen(ctx, params)
}

func (p *Server) HandleDidOpen(ctx context.Context, params *lsp.DidOpenTextDocumentParams) (err error) {
	templURI, err := uri.ParseDocumentURI(string(params.TextDocument.URI))
	if err != nil {
		p.Log.Error("invalid uri", slog.String("uri", string(params.TextDocument.URI)))
		return
	}
	isTemplFile, goURI := p.convertTemplToGoURI(templURI)
	if !isTemplFile {
		return p.Target.DidOpen(ctx, params)
	}
	// Cache the template doc.
	p.TemplSource.Set(string(templURI), NewDocument(p.Log, params.TextDocument.Text))
	// Parse the template.
	template, ok, err := p.parseTemplate(ctx, templURI, params.TextDocument.Text)
	if err != nil {
		p.Log.Error("parseTemplate failure", slog.Any("error", err))
	}
	if !ok {
		p.Log.Info("parsing template did not succeed", slog.String("uri", string(templURI)))
		return nil
	}
	// Generate the output code and cache the source map and Go contents to use during completion
	// requests.
	w := new(strings.Builder)
	generatorOutput, err := generator.Generate(template, w)
	if err != nil {
		return
	}
	p.Log.Info("setting source map cache contents", slog.String("uri", string(templURI)))
	p.SourceMapCache.Set(string(templURI), generatorOutput.SourceMap)
	// Set the Go contents.
	params.TextDocument.Text = w.String()
	p.goSourceMu.Lock()
	p.GoSource[string(templURI)] = params.TextDocument.Text
	p.goSourceMu.Unlock()
	// Change the path.
	params.TextDocument.URI = goURI
	return p.Target.DidOpen(ctx, params)
}

func (p *Server) DidSave(ctx context.Context, params *lsp.DidSaveTextDocumentParams) (err error) {
	p.Log.Info("client -> server: DidSave")
	defer p.Log.Info("client -> server: DidSave end")
	if isTemplFile, goURI := p.convertTemplToGoURI(params.TextDocument.URI); isTemplFile {
		// Re-analyze from the saved text so diagnostics refresh on save
		// too (FR-080); Save.IncludeText is advertised, so Text carries
		// the content.
		if params.Text != "" {
			templURI, parseErr := uri.ParseDocumentURI(string(params.TextDocument.URI))
			if parseErr == nil {
				if _, _, err := p.parseTemplate(ctx, templURI, params.Text); err != nil {
					p.Log.Error("didSave: failed to re-analyze", slog.Any("error", err))
				}
			}
		}
		params.TextDocument.URI = goURI
	}
	return p.Target.DidSave(ctx, params)
}

func (p *Server) DocumentColor(ctx context.Context, params *lsp.DocumentColorParams) (result []lsp.ColorInformation, err error) {
	p.Log.Info("client -> server: DocumentColor")
	defer p.Log.Info("client -> server: DocumentColor end")
	templURI, err := uri.ParseDocumentURI(string(params.TextDocument.URI))
	if err != nil {
		p.Log.Error("invalid uri", slog.String("uri", string(params.TextDocument.URI)))
		return
	}
	isTemplFile, goURI := p.convertTemplToGoURI(templURI)
	if !isTemplFile {
		return p.Target.DocumentColor(ctx, params)
	}
	params.TextDocument.URI = goURI
	result, err = p.Target.DocumentColor(ctx, params)
	if err != nil {
		return
	}
	if result == nil {
		return
	}
	for i, r := range result {
		result[i].Range = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, r.Range)
	}
	return
}

func (p *Server) DocumentHighlight(ctx context.Context, params *lsp.DocumentHighlightParams) (result []lsp.DocumentHighlight, err error) {
	p.Log.Info("client -> server: DocumentHighlight")
	defer p.Log.Info("client -> server: DocumentHighlight end")
	if isPlainGoFile(params.TextDocument.URI) {
		return nil, nil
	}
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	templURI := params.TextDocument.URI
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	result, err = p.Target.DocumentHighlight(ctx, params)
	if err != nil || result == nil {
		return
	}
	if isTempl {
		for i, r := range result {
			result[i].Range = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, r.Range)
		}
	}
	return
}

func (p *Server) DocumentLink(ctx context.Context, params *lsp.DocumentLinkParams) (result []lsp.DocumentLink, err error) {
	p.Log.Info("client -> server: DocumentLink", slog.String("uri", string(params.TextDocument.URI)))
	defer p.Log.Info("client -> server: DocumentLink end")
	if isTemplFile, _ := p.convertTemplToGoURI(params.TextDocument.URI); !isTemplFile {
		return nil, nil
	}
	// No document links for templ files.
	return
}

func (p *Server) DocumentLinkResolve(ctx context.Context, params *lsp.DocumentLink) (result *lsp.DocumentLink, err error) {
	p.Log.Info("client -> server: DocumentLinkResolve")
	defer p.Log.Info("client -> server: DocumentLinkResolve end")
	templURI, err := uri.ParseDocumentURI(string(params.Target))
	if err != nil {
		p.Log.Error("invalid uri", slog.String("uri", string(params.Target)))
		return
	}
	isTemplFile, goURI := p.convertTemplToGoURI(templURI)
	if !isTemplFile {
		return p.Target.DocumentLinkResolve(ctx, params)
	}
	params.Target = goURI
	var ok bool
	if params.Range, ok = p.convertTemplRangeToGoRange(templURI, params.Range); !ok {
		return
	}
	// Rewrite the result.
	result, err = p.Target.DocumentLinkResolve(ctx, params)
	if err != nil {
		return
	}
	if result == nil {
		return
	}
	result.Target = templURI
	result.Range = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, result.Range)
	return
}

func (p *Server) DocumentSymbol(ctx context.Context, params *lsp.DocumentSymbolParams) (result []lsp.SymbolInformationOrDocumentSymbol, err error) {
	p.Log.Info("client -> server: DocumentSymbol")
	defer p.Log.Info("client -> server: DocumentSymbol end")
	templURI, err := uri.ParseDocumentURI(string(params.TextDocument.URI))
	if err != nil {
		p.Log.Error("invalid uri", slog.String("uri", string(params.TextDocument.URI)))
		return
	}
	isTemplFile, goURI := p.convertTemplToGoURI(templURI)
	if !isTemplFile {
		return p.Target.DocumentSymbol(ctx, params)
	}
	params.TextDocument.URI = goURI
	symbols, err := p.Target.DocumentSymbol(ctx, params)
	if err != nil {
		return nil, err
	}

	for _, s := range symbols {
		if s.DocumentSymbol != nil {
			p.convertSymbolRange(templURI, s.DocumentSymbol)
			result = append(result, s)
		}
		if s.SymbolInformation != nil {
			s.SymbolInformation.Location.URI = templURI
			s.SymbolInformation.Location.Range = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, s.SymbolInformation.Location.Range)
			result = append(result, s)
		}
	}

	return result, err
}

func (p *Server) convertSymbolRange(templURI lsp.DocumentURI, s *lsp.DocumentSymbol) {
	sourceMap, ok := p.SourceMapCache.Get(string(templURI))
	if !ok {
		p.Log.Warn("go->templ: sourcemap not found in cache")
		return
	}
	src, ok := sourceMap.SymbolSourceRangeFromTarget(s.Range.Start.Line, s.Range.Start.Character)
	if !ok {
		p.Log.Warn("go->templ: symbol range not found", slog.Any("symbol", s), slog.Any("choices", sourceMap.TargetSymbolRangeToSource))
		return
	}
	s.Range = lsp.Range{
		Start: lsp.Position{
			Line:      uint32(src.From.Line),
			Character: uint32(src.From.Col),
		},
		End: lsp.Position{
			Line:      uint32(src.To.Line),
			Character: uint32(src.To.Col),
		},
	}
	// Within the symbol, we can select sub-sections.
	// These are Go expressions, in the standard source map.
	s.SelectionRange = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, s.SelectionRange)
	for i := range s.Children {
		p.convertSymbolRange(templURI, &s.Children[i])
		if !isRangeWithin(s.Range, s.Children[i].Range) {
			p.Log.Error("child symbol range not within parent range", slog.Any("symbol", s.Children[i]), slog.Int("index", i))
		}
	}
	if !isRangeWithin(s.Range, s.SelectionRange) {
		p.Log.Error("selection range not within range", slog.Any("symbol", s))
	}
}

func isRangeWithin(parent, child lsp.Range) bool {
	if child.Start.Line < parent.Start.Line || child.End.Line > parent.End.Line {
		return false
	}
	if child.Start.Line == parent.Start.Line && child.Start.Character < parent.Start.Character {
		return false
	}
	if child.End.Line == parent.End.Line && child.End.Character > parent.End.Character {
		return false
	}
	return true
}

func (p *Server) ExecuteCommand(ctx context.Context, params *lsp.ExecuteCommandParams) (result any, err error) {
	p.Log.Info("client -> server: ExecuteCommand")
	defer p.Log.Info("client -> server: ExecuteCommand end")
	return p.Target.ExecuteCommand(ctx, params)
}

func (p *Server) FoldingRanges(ctx context.Context, params *lsp.FoldingRangeParams) (result []lsp.FoldingRange, err error) {
	p.Log.Info("client -> server: FoldingRanges")
	defer p.Log.Info("client -> server: FoldingRanges end")
	// There are no folding ranges in templ files.
	return []lsp.FoldingRange{}, nil
}

func (p *Server) Formatting(ctx context.Context, params *lsp.DocumentFormattingParams) (result []lsp.TextEdit, err error) {
	p.Log.Info("client -> server: Formatting")
	defer p.Log.Info("client -> server: Formatting end")
	if isTemplFile, _ := p.convertTemplToGoURI(params.TextDocument.URI); !isTemplFile {
		return nil, nil
	}
	// Format the current document.
	templURI, err := uri.ParseDocumentURI(string(params.TextDocument.URI))
	if err != nil {
		p.Log.Error("invalid uri", slog.String("uri", string(params.TextDocument.URI)))
		return
	}
	// A format request can arrive for a document the server is not
	// holding — an editor formatting on save while didOpen is still in
	// flight, or after a gopls restart. Dereferencing the nil Document
	// here panicked, which takes the whole language server down rather
	// than losing one format.
	d, ok := p.TemplSource.Get(string(templURI))
	if !ok {
		p.Log.Info("formatting requested for a document that is not open", slog.String("uri", string(templURI)))
		return nil, nil
	}
	template, ok, err := p.parseTemplate(ctx, templURI, d.String())
	if err != nil {
		p.Log.Error("parseTemplate failure", slog.Any("error", err))
		return
	}
	if !ok {
		return
	}
	p.Log.Info("attempting to organise imports", slog.String("uri", template.Filepath))
	template, err = imports.Process(template, p.templateExt())
	if err != nil {
		p.Log.Error("organise imports failure", slog.Any("error", err))
		return
	}
	w := new(strings.Builder)
	err = template.Write(w)
	if err != nil {
		p.Log.Error("handleFormatting: faled to write template", slog.Any("error", err))
		return
	}
	// Replace everything.
	result = append(result, lsp.TextEdit{
		Range: lsp.Range{
			Start: lsp.Position{},
			End:   lsp.Position{Line: uint32(len(d.Lines)), Character: 0},
		},
		NewText: w.String(),
	})
	d.Replace(w.String())
	return
}

func (p *Server) Hover(ctx context.Context, params *lsp.HoverParams) (result *lsp.Hover, err error) {
	p.Log.Info("client -> server: Hover")
	defer p.Log.Info("client -> server: Hover end")
	if isPlainGoFile(params.TextDocument.URI) {
		return nil, nil
	}
	// ghtmx constructs hover from the engine's registries: event payload
	// types and route verb/path for bound symbols (FR-083).
	if templURI, parseErr := uri.ParseDocumentURI(string(params.TextDocument.URI)); parseErr == nil {
		if hover, handled := p.ghtmxHover(string(templURI), params.Position); handled {
			return hover, nil
		}
	}
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	templURI := params.TextDocument.URI
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	result, err = p.Target.Hover(ctx, params)
	if err != nil {
		return
	}
	if result != nil && result.Range != nil && isTempl {
		r := convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, *result.Range)
		result.Range = &r
	}
	return
}

func (p *Server) Implementation(ctx context.Context, params *lsp.ImplementationParams) (result []lsp.Location, err error) {
	p.Log.Info("client -> server: Implementation")
	defer p.Log.Info("client -> server: Implementation end")
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	result, err = p.Target.Implementation(ctx, params)
	if err != nil || result == nil {
		return
	}
	convertLocationResults(p.templateExt(), p.SourceMapCache, p.Log, result)
	return
}

func (p *Server) OnTypeFormatting(ctx context.Context, params *lsp.DocumentOnTypeFormattingParams) (result []lsp.TextEdit, err error) {
	p.Log.Info("client -> server: OnTypeFormatting")
	defer p.Log.Info("client -> server: OnTypeFormatting end")
	if isPlainGoFile(params.TextDocument.URI) {
		return nil, nil
	}
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	templURI := params.TextDocument.URI
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	result, err = p.Target.OnTypeFormatting(ctx, params)
	if err != nil || result == nil {
		return
	}
	if isTempl {
		for i, r := range result {
			result[i].Range = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, r.Range)
		}
	}
	return
}

func (p *Server) PrepareRename(ctx context.Context, params *lsp.PrepareRenameParams) (result *lsp.Range, err error) {
	p.Log.Info("client -> server: PrepareRename")
	defer p.Log.Info("client -> server: PrepareRename end")
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	templURI := params.TextDocument.URI
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	result, err = p.Target.PrepareRename(ctx, params)
	if err != nil || result == nil {
		return
	}
	if isTempl {
		output := convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, *result)
		return &output, nil
	}
	return
}

func (p *Server) RangeFormatting(ctx context.Context, params *lsp.DocumentRangeFormattingParams) (result []lsp.TextEdit, err error) {
	p.Log.Info("client -> server: RangeFormatting")
	defer p.Log.Info("client -> server: RangeFormatting end")
	templURI := params.TextDocument.URI
	isTemplFile, goURI := p.convertTemplToGoURI(templURI)
	if !isTemplFile {
		return nil, nil
	}
	var ok bool
	if params.Range, ok = p.convertTemplRangeToGoRange(templURI, params.Range); !ok {
		return
	}
	params.TextDocument.URI = goURI
	result, err = p.Target.RangeFormatting(ctx, params)
	if err != nil {
		return
	}
	for i, r := range result {
		result[i].Range = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, r.Range)
	}
	return
}

func (p *Server) References(ctx context.Context, params *lsp.ReferenceParams) (result []lsp.Location, err error) {
	p.Log.Info("client -> server: References")
	defer p.Log.Info("client -> server: References end")
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	result, err = p.Target.References(ctx, params)
	if err != nil || result == nil {
		return
	}
	convertLocationResults(p.templateExt(), p.SourceMapCache, p.Log, result)
	return
}

func (p *Server) Rename(ctx context.Context, params *lsp.RenameParams) (result *lsp.WorkspaceEdit, err error) {
	p.Log.Info("client -> server: Rename")
	defer p.Log.Info("client -> server: Rename end")
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	result, err = p.Target.Rename(ctx, params)
	if err != nil {
		return
	}
	convertWorkspaceEdit(p.templateExt(), p.SourceMapCache, p.Log, result)
	return
}

func (p *Server) SignatureHelp(ctx context.Context, params *lsp.SignatureHelpParams) (result *lsp.SignatureHelp, err error) {
	p.Log.Info("client -> server: SignatureHelp")
	defer p.Log.Info("client -> server: SignatureHelp end")
	if isPlainGoFile(params.TextDocument.URI) {
		return nil, nil
	}
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	return p.Target.SignatureHelp(ctx, params)
}

func (p *Server) Symbols(ctx context.Context, params *lsp.WorkspaceSymbolParams) (result []lsp.SymbolInformation, err error) {
	p.Log.Info("client -> server: Symbols")
	defer p.Log.Info("client -> server: Symbols end")
	result, err = p.Target.Symbols(ctx, params)
	if err != nil || result == nil {
		return
	}
	for i, s := range result {
		if isTemplGoFile, templURI := convertTemplGoToTemplURI(p.templateExt(), s.Location.URI); isTemplGoFile {
			result[i].Location.URI = templURI
			result[i].Location.Range = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, s.Location.Range)
		}
	}
	return
}

func (p *Server) TypeDefinition(ctx context.Context, params *lsp.TypeDefinitionParams) (result []lsp.Location, err error) {
	p.Log.Info("client -> server: TypeDefinition")
	defer p.Log.Info("client -> server: TypeDefinition end")
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	result, err = p.Target.TypeDefinition(ctx, params)
	if err != nil || result == nil {
		return
	}
	convertLocationResults(p.templateExt(), p.SourceMapCache, p.Log, result)
	return
}

func (p *Server) WillSave(ctx context.Context, params *lsp.WillSaveTextDocumentParams) (err error) {
	p.Log.Info("client -> server: WillSave")
	defer p.Log.Info("client -> server: WillSave end")
	if isTemplFile, goURI := p.convertTemplToGoURI(params.TextDocument.URI); isTemplFile {
		params.TextDocument.URI = goURI
	}
	return p.Target.WillSave(ctx, params)
}

func (p *Server) WillSaveWaitUntil(ctx context.Context, params *lsp.WillSaveTextDocumentParams) (result []lsp.TextEdit, err error) {
	p.Log.Info("client -> server: WillSaveWaitUntil")
	defer p.Log.Info("client -> server: WillSaveWaitUntil end")
	return p.Target.WillSaveWaitUntil(ctx, params)
}

func (p *Server) ShowDocument(ctx context.Context, params *lsp.ShowDocumentParams) (result *lsp.ShowDocumentResult, err error) {
	p.Log.Info("client -> server: ShowDocument")
	defer p.Log.Info("client -> server: ShowDocument end")
	return p.Target.ShowDocument(ctx, params)
}

func (p *Server) WillCreateFiles(ctx context.Context, params *lsp.CreateFilesParams) (result *lsp.WorkspaceEdit, err error) {
	p.Log.Info("client -> server: WillCreateFiles")
	defer p.Log.Info("client -> server: WillCreateFiles end")
	return p.Target.WillCreateFiles(ctx, params)
}

func (p *Server) DidCreateFiles(ctx context.Context, params *lsp.CreateFilesParams) (err error) {
	p.Log.Info("client -> server: DidCreateFiles")
	defer p.Log.Info("client -> server: DidCreateFiles end")
	return p.Target.DidCreateFiles(ctx, params)
}

func (p *Server) WillRenameFiles(ctx context.Context, params *lsp.RenameFilesParams) (result *lsp.WorkspaceEdit, err error) {
	p.Log.Info("client -> server: WillRenameFiles")
	defer p.Log.Info("client -> server: WillRenameFiles end")
	return p.Target.WillRenameFiles(ctx, params)
}

func (p *Server) DidRenameFiles(ctx context.Context, params *lsp.RenameFilesParams) (err error) {
	p.Log.Info("client -> server: DidRenameFiles")
	defer p.Log.Info("client -> server: DidRenameFiles end")
	return p.Target.DidRenameFiles(ctx, params)
}

func (p *Server) WillDeleteFiles(ctx context.Context, params *lsp.DeleteFilesParams) (result *lsp.WorkspaceEdit, err error) {
	p.Log.Info("client -> server: WillDeleteFiles")
	defer p.Log.Info("client -> server: WillDeleteFiles end")
	return p.Target.WillDeleteFiles(ctx, params)
}

func (p *Server) DidDeleteFiles(ctx context.Context, params *lsp.DeleteFilesParams) (err error) {
	p.Log.Info("client -> server: DidDeleteFiles")
	defer p.Log.Info("client -> server: DidDeleteFiles end")
	return p.Target.DidDeleteFiles(ctx, params)
}

func (p *Server) CodeLensRefresh(ctx context.Context) (err error) {
	p.Log.Info("client -> server: CodeLensRefresh")
	defer p.Log.Info("client -> server: CodeLensRefresh end")
	return p.Target.CodeLensRefresh(ctx)
}

func (p *Server) PrepareCallHierarchy(ctx context.Context, params *lsp.CallHierarchyPrepareParams) (result []lsp.CallHierarchyItem, err error) {
	p.Log.Info("client -> server: PrepareCallHierarchy")
	defer p.Log.Info("client -> server: PrepareCallHierarchy end")
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	result, err = p.Target.PrepareCallHierarchy(ctx, params)
	if err != nil || result == nil {
		return
	}
	for i := range result {
		convertCallHierarchyItem(p.templateExt(), p.SourceMapCache, p.Log, &result[i])
	}
	return
}

func (p *Server) IncomingCalls(ctx context.Context, params *lsp.CallHierarchyIncomingCallsParams) (result []lsp.CallHierarchyIncomingCall, err error) {
	p.Log.Info("client -> server: IncomingCalls")
	defer p.Log.Info("client -> server: IncomingCalls end")
	result, err = p.Target.IncomingCalls(ctx, params)
	if err != nil || result == nil {
		return
	}
	for i := range result {
		// Check the original URI before converting the item.
		isTemplGoFile, templURI := convertTemplGoToTemplURI(p.templateExt(), result[i].From.URI)
		convertCallHierarchyItem(p.templateExt(), p.SourceMapCache, p.Log, &result[i].From)
		if isTemplGoFile {
			for j, r := range result[i].FromRanges {
				result[i].FromRanges[j] = convertGoRangeToTemplRange(p.SourceMapCache, p.Log, templURI, r)
			}
		}
	}
	return
}

func (p *Server) OutgoingCalls(ctx context.Context, params *lsp.CallHierarchyOutgoingCallsParams) (result []lsp.CallHierarchyOutgoingCall, err error) {
	p.Log.Info("client -> server: OutgoingCalls")
	defer p.Log.Info("client -> server: OutgoingCalls end")
	result, err = p.Target.OutgoingCalls(ctx, params)
	if err != nil || result == nil {
		return
	}
	for i := range result {
		convertCallHierarchyItem(p.templateExt(), p.SourceMapCache, p.Log, &result[i].To)
	}
	return
}

func (p *Server) SemanticTokensFull(ctx context.Context, params *lsp.SemanticTokensParams) (result *lsp.SemanticTokens, err error) {
	p.Log.Info("client -> server: SemanticTokensFull")
	defer p.Log.Info("client -> server: SemanticTokensFull end")
	isTemplFile, goURI := p.convertTemplToGoURI(params.TextDocument.URI)
	if !isTemplFile {
		return nil, nil
	}
	params.TextDocument.URI = goURI
	return p.Target.SemanticTokensFull(ctx, params)
}

func (p *Server) SemanticTokensFullDelta(ctx context.Context, params *lsp.SemanticTokensDeltaParams) (result any /* SemanticTokens | SemanticTokensDelta */, err error) {
	p.Log.Info("client -> server: SemanticTokensFullDelta")
	defer p.Log.Info("client -> server: SemanticTokensFullDelta end")
	isTemplFile, goURI := p.convertTemplToGoURI(params.TextDocument.URI)
	if !isTemplFile {
		return nil, nil
	}
	params.TextDocument.URI = goURI
	return p.Target.SemanticTokensFullDelta(ctx, params)
}

func (p *Server) SemanticTokensRange(ctx context.Context, params *lsp.SemanticTokensRangeParams) (result *lsp.SemanticTokens, err error) {
	p.Log.Info("client -> server: SemanticTokensRange")
	defer p.Log.Info("client -> server: SemanticTokensRange end")
	isTemplFile, goURI := p.convertTemplToGoURI(params.TextDocument.URI)
	if !isTemplFile {
		return nil, nil
	}
	params.TextDocument.URI = goURI
	return p.Target.SemanticTokensRange(ctx, params)
}

func (p *Server) SemanticTokensRefresh(ctx context.Context) (err error) {
	p.Log.Info("client -> server: SemanticTokensRefresh")
	defer p.Log.Info("client -> server: SemanticTokensRefresh end")
	return p.Target.SemanticTokensRefresh(ctx)
}

func (p *Server) LinkedEditingRange(ctx context.Context, params *lsp.LinkedEditingRangeParams) (result *lsp.LinkedEditingRanges, err error) {
	p.Log.Info("client -> server: LinkedEditingRange")
	defer p.Log.Info("client -> server: LinkedEditingRange end")
	return p.Target.LinkedEditingRange(ctx, params)
}

func (p *Server) Moniker(ctx context.Context, params *lsp.MonikerParams) (result []lsp.Moniker, err error) {
	p.Log.Info("client -> server: Moniker")
	defer p.Log.Info("client -> server: Moniker end")
	if isPlainGoFile(params.TextDocument.URI) {
		return nil, nil
	}
	isTempl, goURI, goPos, ok := p.proxyPositionRequest(params.TextDocument.URI, params.Position)
	if !ok {
		return nil, nil
	}
	if isTempl {
		params.TextDocument.URI = goURI
		params.Position = goPos
	}
	return p.Target.Moniker(ctx, params)
}

func (p *Server) Request(ctx context.Context, method string, params any) (result any, err error) {
	p.Log.Info("client -> server: Request")
	defer p.Log.Info("client -> server: Request end")
	return p.Target.Request(ctx, method, params)
}
