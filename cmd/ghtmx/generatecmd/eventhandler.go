package generatecmd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"go/format"
	"go/scanner"
	"go/token"
	"io"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"golang.org/x/sync/errgroup"

	"github.com/go-monolith/ghtmx/cmd/ghtmx/visualize"
	"github.com/go-monolith/ghtmx/internal/analyzer"
	"github.com/go-monolith/ghtmx/internal/buildcache"
	"github.com/go-monolith/ghtmx/internal/config"
	"github.com/go-monolith/ghtmx/internal/diag"
	"github.com/go-monolith/ghtmx/internal/generator"
	"github.com/go-monolith/ghtmx/internal/generator/central"
	"github.com/go-monolith/ghtmx/internal/htmxsurface"
	"github.com/go-monolith/ghtmx/internal/parser"
	"github.com/go-monolith/ghtmx/internal/routes"
	"github.com/go-monolith/ghtmx/internal/syncmap"
	"github.com/go-monolith/ghtmx/internal/syncset"
	"github.com/go-monolith/ghtmx/runtime"
)

type FileWriterFunc func(name string, contents []byte) error

func FileWriter(fileName string, contents []byte) error {
	return os.WriteFile(fileName, contents, 0o644)
}

func WriterFileWriter(w io.Writer) FileWriterFunc {
	return func(_ string, contents []byte) error {
		_, err := w.Write(contents)
		return err
	}
}

// NewCheckWriter returns a FileWriterFunc that compares generated output against
// existing files without writing, and a function to retrieve the list of files
// that differ.
func NewCheckWriter() (writer FileWriterFunc, getChanged func() []string) {
	var mu sync.Mutex
	var changed []string

	writer = func(name string, contents []byte) error {
		existing, err := os.ReadFile(name)
		if err != nil || !bytes.Equal(existing, contents) {
			mu.Lock()
			defer mu.Unlock()
			changed = append(changed, name)
		}
		return nil
	}
	getChanged = func() []string {
		mu.Lock()
		defer mu.Unlock()
		return changed
	}
	return writer, getChanged
}

// FSEventHandlerOption configures optional event handler behavior.
type FSEventHandlerOption func(*FSEventHandler)

// WithGeneratedSuffix sets the file-name suffix that replaces the template
// extension on generated Go files (config generatedSuffix, default
// "_ghtmx.go").
func WithGeneratedSuffix(suffix string) FSEventHandlerOption {
	return func(h *FSEventHandler) {
		if suffix != "" {
			h.generatedSuffix = suffix
		}
	}
}

// WithTemplateExtension sets the extension templates are written with
// (config templateExtension, default ".ghtmx"). It decides what the
// watcher treats as a template and, critically, which source file the
// orphan check looks for: with the wrong extension every generated file
// looks orphaned and is deleted immediately after being written.
func WithTemplateExtension(ext string) FSEventHandlerOption {
	return func(h *FSEventHandler) {
		if ext != "" {
			h.templateExtension = ext
		}
	}
}

// WithAttributeValidation enables hx-* attribute validation against the
// surface for the configured htmx version (FR-024, FR-041, FR-044).
// Error-level diagnostics fail the file's generation; warnings are logged.
func WithAttributeValidation(surface *htmxsurface.Surface, severityOverrides map[string]diag.Severity) FSEventHandlerOption {
	return func(h *FSEventHandler) {
		h.surface = surface
		h.severityOverrides = severityOverrides
	}
}

// WithRouteBindings enables route-aware hx-* binding resolution (FR-020,
// FR-022) against the discovered route table. modRoot and modulePath are
// the module's directory and import path, used to derive each template's
// package path for bare handler identifiers (relative to the module, so
// a generate root below it resolves correctly; an empty modRoot falls
// back to the generate root); generatedPkgName is the central generated
// package's name.
func WithRouteBindings(table *routes.Table, modRoot, modulePath, generatedPkgName string, constructors map[string]central.Constructor) FSEventHandlerOption {
	return func(h *FSEventHandler) {
		h.routeTable = table
		h.modRoot = modRoot
		h.modulePath = modulePath
		h.generatedPkgName = generatedPkgName
		h.constructors = constructors
	}
}

// WithCentralFile marks the central generated package's file so the event
// handler never treats it as an orphaned template output.
func WithCentralFile(absPath string) FSEventHandlerOption {
	return func(h *FSEventHandler) {
		h.centralFile = absPath
	}
}

// WithSetAnalysis attaches the whole-compiled-set collector for the
// dangling-target and unreachable-route checks (FR-042, FR-043).
func WithSetAnalysis(sa *analyzer.SetAnalysis) FSEventHandlerOption {
	return func(h *FSEventHandler) {
		h.setAnalysis = sa
	}
}

// WithSeverityOverrides applies the configured per-check severities to
// diagnostics the handler emits itself (currently GHTMX-W0301),
// independent of whether attribute validation loaded.
func WithSeverityOverrides(overrides map[string]diag.Severity) FSEventHandlerOption {
	return func(h *FSEventHandler) {
		h.severityOverrides = overrides
	}
}

// WithStaleDiagnostics controls the in-generate GHTMX-W0301 report. Check
// mode disables it: the check writer writes nothing, so "has been
// regenerated" would be false — check mode reports drift itself.
func WithStaleDiagnostics(enabled bool) FSEventHandlerOption {
	return func(h *FSEventHandler) {
		h.staleDiagnostics = enabled
	}
}

// WithBuildCache serves unchanged units from the on-disk build cache
// (D6, NFR-001). The caller only enables it in modes whose output is a
// pure function of source content, configuration, and binding state.
func WithBuildCache(store *buildcache.Store) FSEventHandlerOption {
	return func(h *FSEventHandler) {
		h.buildCache = store
	}
}

func NewFSEventHandler(
	log *slog.Logger,
	dir string,
	devMode bool,
	genOpts []generator.GenerateOpt,
	genSourceMapVis bool,
	keepOrphanedFiles bool,
	fileWriter FileWriterFunc,
	lazy bool,
	options ...FSEventHandlerOption,
) *FSEventHandler {
	if !path.IsAbs(dir) {
		dir, _ = filepath.Abs(dir)
	}
	fseh := &FSEventHandler{
		Log:                   log,
		dir:                   dir,
		generatedSuffix:       "_ghtmx.go",
		templateExtension:     config.DefaultTemplateExtension,
		fileNameToLastModTime: syncmap.New[string, time.Time](),
		fileNameToError:       syncset.New[string](),
		fileNameToOutput:      syncmap.New[string, generator.GeneratorOutput](),
		devMode:               devMode,
		hashes:                syncmap.New[string, [sha256.Size]byte](),
		genOpts:               genOpts,
		genSourceMapVis:       genSourceMapVis,
		keepOrphanedFiles:     keepOrphanedFiles,
		writer:                fileWriter,
		lazy:                  lazy,
	}
	for _, o := range options {
		o(fseh)
	}
	return fseh
}

type FSEventHandler struct {
	Log *slog.Logger
	// dir is the root directory being processed.
	dir                   string
	fileNameToLastModTime *syncmap.Map[string, time.Time]
	fileNameToError       *syncset.Set[string]
	fileNameToOutput      *syncmap.Map[string, generator.GeneratorOutput]
	devMode               bool
	hashes                *syncmap.Map[string, [sha256.Size]byte]
	genOpts               []generator.GenerateOpt
	buildCache            *buildcache.Store
	genSourceMapVis       bool
	generatedSuffix       string
	templateExtension     string
	surface               *htmxsurface.Surface
	severityOverrides     map[string]diag.Severity
	bindingMu             sync.RWMutex
	fileLocks             sync.Map
	seenTargets           sync.Map
	staleDiagnostics      bool
	parseNanos            atomic.Int64
	analyzeNanos          atomic.Int64
	emitNanos             atomic.Int64
	routeTable            *routes.Table
	modRoot               string
	modulePath            string
	generatedPkgName      string
	constructors          map[string]central.Constructor
	centralFile           string
	setAnalysis           *analyzer.SetAnalysis
	Errors                []error
	keepOrphanedFiles     bool
	writer                FileWriterFunc
	lazy                  bool
}

type GenerateResult struct {
	// GoFileWritten indicates that the generated Go file was written because its content changed.
	GoFileWritten bool
	// WatchedFileUpdated indicates that a file matching the watch pattern was updated.
	WatchedFileUpdated bool
	// TemplFileTextUpdated indicates that text literals were updated.
	TemplFileTextUpdated bool
	// TemplFileGoUpdated indicates that Go expressions were updated.
	TemplFileGoUpdated bool
	// GoSourceUpdated indicates that a hand-written Go source file was
	// updated: route registrations may have changed (FR-061 tier one).
	GoSourceUpdated bool
}

func (h *FSEventHandler) HandleEvent(ctx context.Context, event fsnotify.Event) (result GenerateResult, err error) {
	// The central generated package is written by the generate run itself,
	// not derived from a template: never orphan-delete or process it.
	if h.centralFile != "" {
		if abs, err := filepath.Abs(event.Name); err == nil && abs == h.centralFile {
			return GenerateResult{}, nil
		}
	}

	// Handle generated Go files.
	if !event.Has(fsnotify.Remove) && strings.HasSuffix(event.Name, h.generatedSuffix) {
		_, err = os.Stat(strings.TrimSuffix(event.Name, h.generatedSuffix) + h.templateExtension)
		if !os.IsNotExist(err) {
			return GenerateResult{}, err
		}
		// File is orphaned.
		if h.keepOrphanedFiles {
			return GenerateResult{}, nil
		}
		h.Log.Debug("Deleting orphaned Go file", slog.String("file", event.Name))
		if err = os.Remove(event.Name); err != nil {
			h.Log.Warn("Failed to remove orphaned file", slog.Any("error", err))
		}
		return GenerateResult{WatchedFileUpdated: false, TemplFileGoUpdated: true, TemplFileTextUpdated: false}, nil
	}

	if event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 {
		// A removed hand-written Go file can carry route registrations:
		// tier-one re-discovery must run (FR-061).
		if strings.HasSuffix(event.Name, ".go") && !strings.HasSuffix(event.Name, h.generatedSuffix) {
			h.fileNameToLastModTime.Delete(event.Name)
			return GenerateResult{WatchedFileUpdated: true, GoSourceUpdated: true}, nil
		}
		// A removed template's whole-set contribution must not linger:
		// purge its facts and freshness gates. TemplFileGoUpdated makes
		// the batch pipeline run, so the central package and whole-set
		// diagnostics refresh without the removed declarations.
		if strings.HasSuffix(event.Name, h.templateExtension) {
			if h.setAnalysis != nil {
				h.setAnalysis.RemoveFile(event.Name)
			}
			h.fileNameToLastModTime.Delete(event.Name)
			h.hashes.Delete(strings.TrimSuffix(event.Name, h.templateExtension) + h.generatedSuffix)
			return GenerateResult{TemplFileGoUpdated: true}, nil
		}
	}

	// If the file hasn't been updated since the last time we processed it, ignore it.
	fileInfo, err := os.Stat(event.Name)
	if err != nil {
		return GenerateResult{}, fmt.Errorf("failed to stat %q: %w", event.Name, err)
	}
	mustBeInTheFuture := func(previous, updated time.Time) bool {
		return updated.After(previous)
	}
	updatedModTime := h.fileNameToLastModTime.CompareAndSwap(event.Name, mustBeInTheFuture, fileInfo.ModTime())
	if !updatedModTime {
		h.Log.Debug("Skipping file because it wasn't updated", slog.String("file", event.Name))
		return GenerateResult{}, nil
	}

	// Process anything that isn't a templ file.
	if !strings.HasSuffix(event.Name, h.templateExtension) {
		if h.devMode {
			h.Log.Info("Watched file updated", slog.String("file", event.Name))
		}
		result.WatchedFileUpdated = true
		// Generated files churn during regeneration; only hand-written Go
		// can change route registrations.
		if strings.HasSuffix(event.Name, ".go") && !strings.HasSuffix(event.Name, h.generatedSuffix) {
			result.GoSourceUpdated = true
		}
		return result, nil
	}

	// Handle templ files.

	// If the go file is newer than the templ file, skip generation, because it's up-to-date.
	if h.lazy && goFileIsUpToDate(event.Name, h.templateExtension, h.generatedSuffix, fileInfo.ModTime()) {
		h.Log.Debug("Skipping file because the Go file is up-to-date", slog.String("file", event.Name))
		return GenerateResult{}, nil
	}

	// Start a processor.
	start := time.Now()
	var diag []parser.Diagnostic
	result, diag, err = h.generate(ctx, event.Name)
	if err != nil {
		h.fileNameToError.Set(event.Name)
		return result, fmt.Errorf("failed to generate code for %q: %w", event.Name, err)
	}
	if len(diag) > 0 {
		for _, d := range diag {
			h.Log.Warn(d.Message,
				slog.String("from", fmt.Sprintf("%d:%d", d.Range.From.Line, d.Range.From.Col)),
				slog.String("to", fmt.Sprintf("%d:%d", d.Range.To.Line, d.Range.To.Col)),
			)
		}
		return result, nil
	}
	if errorCleared := h.fileNameToError.Delete(event.Name); errorCleared {
		h.Log.Info("Error cleared", slog.String("file", event.Name), slog.Int("errors", h.fileNameToError.Count()))
	}
	h.Log.Debug("Generated code", slog.String("file", event.Name), slog.Duration("in", time.Since(start)))

	return result, nil
}

// pkgPathFor derives the import path of the package containing fileName
// from the module path and the file's directory relative to the module
// root — not the generate root: a -path below the module root (a
// nested project with its own ghtmx.json, such as an example pinned to
// another htmx version) still lives in the module's import space.
func (h *FSEventHandler) pkgPathFor(fileName string) string {
	if h.modulePath == "" {
		return ""
	}
	abs, err := filepath.Abs(fileName)
	if err != nil {
		return h.modulePath
	}
	base := h.modRoot
	if base == "" {
		base = h.dir
	}
	rel, err := filepath.Rel(base, filepath.Dir(abs))
	if err != nil || rel == "." {
		return h.modulePath
	}
	return h.modulePath + "/" + filepath.ToSlash(rel)
}

// analyze runs the semantic analysis passes available at this stage of the
// pipeline. Error-level diagnostics fail the file (constitution P2: no
// runtime surprises); warnings are logged and never fail the run (FR-060).
func (h *FSEventHandler) analyze(t *parser.TemplateFile, fileName string) error {
	if t.Filepath == "" {
		t.Filepath = fileName
	}
	// Whole-set facts feed the dependency graph and checks even when the
	// attribute surface failed to load.
	if h.setAnalysis != nil {
		h.setAnalysis.Collect(t, h.pkgPathFor(fileName))
	}
	sink := diag.NewSink(h.severityOverrides)
	if h.surface != nil {
		analyzer.ValidateAttributes(t, h.surface, sink)
	}
	analyzer.ValidateFragments(t, sink)
	analyzer.ValidateImports(t, sink)
	h.bindingMu.RLock()
	table, constructors := h.routeTable, h.constructors
	h.bindingMu.RUnlock()
	if table != nil {
		analyzer.ResolveBindings(t, analyzer.BindingEnv{
			Table:            table,
			Surface:          h.surface,
			PkgPath:          h.pkgPathFor(fileName),
			GeneratedPkgName: h.generatedPkgName,
			Constructors:     constructors,
			SetAnalysis:      h.setAnalysis,
		}, sink)
	}
	errorCount := 0
	for _, d := range sink.Diagnostics() {
		if d.Severity == diag.Error {
			errorCount++
			h.Log.Error(d.String())
			continue
		}
		h.Log.Warn(d.String())
	}
	if errorCount > 0 {
		return fmt.Errorf("%s: %d hx-* attribute error(s), see diagnostics above", fileName, errorCount)
	}
	return nil
}

func goFileIsUpToDate(templFileName, templateExtension, generatedSuffix string, templFileLastMod time.Time) (upToDate bool) {
	goFileName := strings.TrimSuffix(templFileName, templateExtension) + generatedSuffix
	goFileInfo, err := os.Stat(goFileName)
	if err != nil {
		return false
	}
	return goFileInfo.ModTime().After(templFileLastMod)
}

// generate Go code for a single template.
// If a basePath is provided, the filename included in error messages is relative to it.
func (h *FSEventHandler) generate(ctx context.Context, fileName string) (result GenerateResult, diagnostics []parser.Diagnostic, err error) {
	// Serialize per target file: a tier-one/tier-two dependent pass and a
	// regular worker may reach the same unit concurrently, and interleaved
	// writes would desynchronize the on-disk content from the recorded
	// hash.
	lockAny, _ := h.fileLocks.LoadOrStore(fileName, &sync.Mutex{})
	lock := lockAny.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	// Read once so the cache key and the parse see identical bytes.
	phaseStart := time.Now()
	fc, err := os.ReadFile(fileName)
	if err != nil {
		return GenerateResult{}, nil, fmt.Errorf("%s read error: %w", fileName, err)
	}
	t, err := parser.ParseString(string(fc))
	h.parseNanos.Add(int64(time.Since(phaseStart)))
	if err != nil {
		return GenerateResult{}, nil, fmt.Errorf("%s parsing error: %w", fileName, err)
	}
	phaseStart = time.Now()
	analyzeErr := h.analyze(t, fileName)
	h.analyzeNanos.Add(int64(time.Since(phaseStart)))
	if analyzeErr != nil {
		return GenerateResult{}, nil, analyzeErr
	}
	targetFileName := strings.TrimSuffix(fileName, h.templateExtension) + h.generatedSuffix

	// Only use relative filenames to the basepath for filenames in runtime error messages.
	absFilePath, err := filepath.Abs(fileName)
	if err != nil {
		return GenerateResult{}, nil, fmt.Errorf("failed to get absolute path for %q: %w", fileName, err)
	}
	// A file on another Windows drive cannot be made relative; the name is
	// only used in error messages and cache keys, so fall back to absolute.
	relFilePath := absFilePath
	if rel, err := filepath.Rel(h.dir, absFilePath); err == nil {
		relFilePath = rel
	}
	// Convert Windows file paths to Unix-style for consistency.
	relFilePath = filepath.ToSlash(relFilePath)

	// The cache replaces only generation and formatting: parsing and
	// analysis always run, because whole-set checks need every file's
	// facts. Dev mode and source-map visualisation need GeneratorOutput,
	// so the cache is bypassed there.
	useCache := h.buildCache != nil && !h.devMode && !h.genSourceMapVis
	var cacheKey [sha256.Size]byte
	var formattedGoCode []byte
	var generatorOutput generator.GeneratorOutput
	cached := false
	phaseStart = time.Now()
	defer func() { h.emitNanos.Add(int64(time.Since(phaseStart))) }()
	if useCache {
		cacheKey = h.buildCache.Key(relFilePath, fc)
		formattedGoCode, cached = h.buildCache.Get(cacheKey)
	}
	if !cached {
		var b bytes.Buffer
		generatorOutput, err = generator.Generate(t, &b, append(h.genOpts, generator.WithFileName(relFilePath))...)
		if err != nil {
			return GenerateResult{}, nil, fmt.Errorf("%s generation error: %w", fileName, err)
		}

		formattedGoCode, err = format.Source(b.Bytes())
		if err != nil {
			err = remapErrorList(err, generatorOutput.SourceMap, fileName)
			return GenerateResult{}, nil, fmt.Errorf("%s source formatting error %w", fileName, err)
		}
		if useCache {
			if err := h.buildCache.Put(cacheKey, formattedGoCode); err != nil {
				h.Log.Debug("Build cache put failed", slog.Any("error", err))
			}
		}
	}

	// Hash output, and write out the file if the goCodeHash has changed.
	// On first sight of a unit, an on-disk artifact that differs from the
	// fresh output (or is missing) is stale (FR-054, GHTMX-W0301);
	// subsequent in-run changes are ordinary regeneration.
	goCodeHash := sha256.Sum256(formattedGoCode)
	// Stale means genuine first sight of the unit this run: watch-mode
	// atomic saves delete the hash entry, so a separate seen set keeps
	// ordinary re-edits from re-reporting (FR-054).
	_, seenBefore := h.seenTargets.LoadOrStore(targetFileName, true)
	stale := false
	if _, ok := h.hashes.Get(targetFileName); !ok && !seenBefore {
		if existingContent, readErr := os.ReadFile(targetFileName); readErr == nil {
			existingHash := sha256.Sum256(existingContent)
			h.hashes.CompareAndSwap(targetFileName, syncmap.UpdateIfChanged, existingHash)
			stale = existingHash != goCodeHash
		} else {
			// Missing artifacts are drift; unreadable ones will fail the
			// write below anyway, so reporting them as stale is harmless.
			stale = true
		}
	}
	if h.hashes.CompareAndSwap(targetFileName, syncmap.UpdateIfChanged, goCodeHash) {
		result.GoFileWritten = true
		if err = h.writer(targetFileName, formattedGoCode); err != nil {
			return result, nil, fmt.Errorf("failed to write target file %q: %w", targetFileName, err)
		}
		if stale && h.staleDiagnostics {
			sink := diag.NewSink(h.severityOverrides)
			sink.Add(diag.StaleOutput, diag.Position{File: fileName, Line: 1, Col: 1},
				fmt.Sprintf("generated output %s was stale and has been regenerated", targetFileName),
				"commit the regenerated file, or run ghtmx generate before building")
			for _, d := range sink.Diagnostics() {
				if d.Severity == diag.Error {
					// An escalated W0301 fails the file, consistent with
					// FR-060: error-level diagnostics fail the build.
					h.Log.Error(d.String())
					return result, nil, fmt.Errorf("%s: stale generated output (GHTMX-W0301 escalated to error)", fileName)
				}
				h.Log.Warn(d.String())
			}
		}
	}

	// Add the txt file if it has changed.
	if h.devMode {
		txtFileName := runtime.GetDevModeTextFileName(fileName)
		h.Log.Debug("Writing development mode text file", slog.String("file", fileName), slog.String("output", txtFileName))
		joined := strings.Join(generatorOutput.Literals, "\n")
		txtHash := sha256.Sum256([]byte(joined))
		if h.hashes.CompareAndSwap(txtFileName, syncmap.UpdateIfChanged, txtHash) {
			if err = os.WriteFile(txtFileName, []byte(joined), 0o644); err != nil {
				return result, nil, fmt.Errorf("failed to write string literal file %q: %w", txtFileName, err)
			}
		}
		// Check whether the change would require a recompilation or text update to take effect.
		previous, hasPrevious := h.fileNameToOutput.Get(fileName)
		if hasPrevious {
			result.TemplFileTextUpdated = generator.HasTextChanged(previous, generatorOutput)
			result.TemplFileGoUpdated = generator.HasGoChanged(previous, generatorOutput)
		}
		h.fileNameToOutput.Set(fileName, generatorOutput)
	}

	parsedDiagnostics, err := parser.Diagnose(t)
	if err != nil {
		return result, nil, fmt.Errorf("%s diagnostics error: %w", fileName, err)
	}

	if h.genSourceMapVis {
		err = generateSourceMapVisualisation(ctx, fileName, targetFileName, h.templateExtension, generatorOutput.SourceMap)
	}

	return result, parsedDiagnostics, err
}

// Takes an error from the formatter and attempts to convert the positions reported in the target file to their positions
// in the source file.
func remapErrorList(err error, sourceMap *parser.SourceMap, fileName string) error {
	list, ok := err.(scanner.ErrorList)
	if !ok || len(list) == 0 {
		return err
	}
	for i, e := range list {
		// The positions in the source map are off by one line because of the package definition.
		srcPos, ok := sourceMap.SourcePositionFromTarget(uint32(e.Pos.Line-1), uint32(e.Pos.Column))
		if !ok {
			continue
		}
		list[i].Pos = token.Position{
			Filename: fileName,
			Offset:   int(srcPos.Index),
			Line:     int(srcPos.Line) + 1,
			Column:   int(srcPos.Col),
		}
	}
	return list
}

func generateSourceMapVisualisation(ctx context.Context, templFileName, goFileName, templateExtension string, sourceMap *parser.SourceMap) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var templContents, goContents []byte
	var grp errgroup.Group
	grp.Go(func() (err error) {
		templContents, err = os.ReadFile(templFileName)
		return err
	})
	grp.Go(func() (err error) {
		goContents, err = os.ReadFile(goFileName)
		return err
	})
	if err := grp.Wait(); err != nil {
		return err
	}
	component := visualize.HTML(templFileName, string(templContents), string(goContents), sourceMap)

	targetFileName := strings.TrimSuffix(templFileName, templateExtension) + "_ghtmx_sourcemap.html"
	w, err := os.Create(targetFileName)
	if err != nil {
		return fmt.Errorf("%s sourcemap visualisation error: %w", templFileName, err)
	}
	b := bufio.NewWriter(w)
	if err = component.Render(ctx, b); err != nil {
		_ = w.Close()
		return fmt.Errorf("%s sourcemap visualisation render error: %w", templFileName, err)
	}
	if err = b.Flush(); err != nil {
		_ = w.Close()
		return fmt.Errorf("%s sourcemap visualisation flush error: %w", templFileName, err)
	}
	if err = w.Close(); err != nil {
		return fmt.Errorf("%s sourcemap visualisation close error: %w", templFileName, err)
	}
	return nil
}

// HandleDependent re-processes a unit invalidated by another file's
// change (FR-061 tier two). The unit's own mtime has not advanced, so its
// freshness gate is reset first — a plain HandleEvent would skip it.
func (h *FSEventHandler) HandleDependent(ctx context.Context, fileName string) (GenerateResult, error) {
	h.fileNameToLastModTime.Delete(fileName)
	return h.HandleEvent(ctx, fsnotify.Event{Name: fileName, Op: fsnotify.Write})
}

// UpdateRouteBindings swaps the route table and constructor naming after
// a watch-mode re-discovery (FR-061 tier one): analysis of subsequent
// events resolves against the new state.
func (h *FSEventHandler) UpdateRouteBindings(table *routes.Table, constructors map[string]central.Constructor) {
	h.bindingMu.Lock()
	defer h.bindingMu.Unlock()
	h.routeTable = table
	h.constructors = constructors
}

// PhaseTimings reports cumulative wall time spent in each per-file phase
// across all workers (NFR-001 verbose attribution). Concurrent workers
// overlap, so sums can exceed elapsed wall time; per-file lock waits are
// attributed to no phase, so under contention the sum can also
// understate a file's wall time.
func (h *FSEventHandler) PhaseTimings() (parse, analyze, emit time.Duration) {
	return time.Duration(h.parseNanos.Load()), time.Duration(h.analyzeNanos.Load()), time.Duration(h.emitNanos.Load())
}
