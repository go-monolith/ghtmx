package generatecmd

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	goruntime "runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/cli/browser"
	"github.com/fsnotify/fsnotify"
	"golang.org/x/mod/modfile"
	"golang.org/x/sync/errgroup"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd/modcheck"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd/proxy"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd/run"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd/watcher"
	"github.com/go-monolith/ghtmx/internal/analyzer"
	"github.com/go-monolith/ghtmx/internal/build"
	"github.com/go-monolith/ghtmx/internal/buildcache"
	"github.com/go-monolith/ghtmx/internal/config"
	"github.com/go-monolith/ghtmx/internal/diag"
	"github.com/go-monolith/ghtmx/internal/generator"
	"github.com/go-monolith/ghtmx/internal/generator/central"
	"github.com/go-monolith/ghtmx/internal/htmxsurface"
	"github.com/go-monolith/ghtmx/internal/ignorefile"
	"github.com/go-monolith/ghtmx/internal/routes"
	"github.com/go-monolith/ghtmx/internal/skipdir"
	ghtmxruntime "github.com/go-monolith/ghtmx/runtime"
)

func NewGenerate(log *slog.Logger, args Arguments) (g *Generate, err error) {
	g = &Generate{
		Log:  log,
		Args: args,
	}
	return g, nil
}

type Generate struct {
	Log        *slog.Logger
	Args       Arguments
	ShouldSkip func(string) bool
	// refreshCentral rewrites the central generated package with the
	// events collected so far; set once the route table is known and
	// called before each post-generation command runs, so -cmd builds and
	// watch-mode reloads always see current emitters (FR-037).
	refreshCentral func() error
	// dependentsOf returns the other units invalidated by a change to a
	// .ghtmx file (FR-061 tier two): pages referencing its fragments and
	// files listening for its events. Nil outside watch mode.
	dependentsOf func(file string) []string
	// walkDone flips once the initial filesystem walk finishes: tier-two
	// expansion applies to watch events only — during the walk every file
	// is processed anyway.
	walkDone *atomic.Bool
	// rediscoverRoutes re-runs route discovery after a hand-written Go
	// change (FR-061 tier one); nil outside watch mode.
	rediscoverRoutes func(ctx context.Context)
	// watchCheck re-runs the whole-set checks after a watch batch,
	// reporting diagnostics without terminating the watch; nil outside
	// watch mode.
	watchCheck func()
	// fsehRef is the event handler, for watch-mode closures.
	fsehRef *FSEventHandler
	// centralChecked flips on the run's first central write: only that
	// write can observe pre-run drift — later refreshes in the same run
	// legitimately differ as events are collected.
	centralChecked *atomic.Bool
}

type GenerationEvent struct {
	Event                fsnotify.Event
	GoFileWritten        bool
	WatchedFileUpdated   bool
	TemplFileTextUpdated bool
	TemplFileGoUpdated   bool
	GoSourceUpdated      bool
	// Errors counts failed regenerations in this event: errors coalesce
	// into their own batch, so gating attributes them to the change that
	// caused them, never to the fix that follows (FR-063).
	Errors int
}

func (cmd Generate) Run(ctx context.Context) (err error) {
	if cmd.Args.NotifyProxy {
		return proxy.NotifyProxy(cmd.Args.ProxyBind, cmd.Args.ProxyPort)
	}
	if cmd.Args.PPROFPort > 0 {
		go func() {
			_ = http.ListenAndServe(fmt.Sprintf("localhost:%d", cmd.Args.PPROFPort), nil)
		}()
	}

	// Use absolute path.
	if !path.IsAbs(cmd.Args.Path) {
		cmd.Args.Path, err = filepath.Abs(cmd.Args.Path)
		if err != nil {
			return fmt.Errorf("failed to get absolute path: %w", err)
		}
	}

	// Load ignore patterns.
	cmd.ShouldSkip, err = ignorefile.ShouldSkipFunc(cmd.Args.Path, ".ghtmxignore_generate")
	if err != nil {
		return fmt.Errorf("failed to parse .ghtmxignore_generate: %w", err)
	}

	// Configure generator.
	var opts []generator.GenerateOpt
	if cmd.Args.IncludeVersion {
		opts = append(opts, generator.WithVersion(ghtmx.Version()))
	}
	if cmd.Args.IncludeTimestamp {
		opts = append(opts, generator.WithTimestamp(time.Now()))
	}

	// Check the version of the templ module.
	if err := modcheck.Check(cmd.Args.Path); err != nil {
		cmd.Log.Warn("ghtmx version check: " + err.Error())
	}

	discoveryStart := time.Now()
	table, fragRefs, modRoot, modulePath, discoveryErrors, _ := cmd.discoverRoutes()
	discoveryDuration := time.Since(discoveryStart)
	setAnalysis := analyzer.NewSetAnalysis()
	setAnalysis.MarkGoFragmentRefs(fragRefs)
	constructors, nameConflicts := central.Naming(table)
	for _, group := range nameConflicts {
		discoveryErrors++
		var sites []string
		for _, c := range group {
			sites = append(sites, fmt.Sprintf("%s %s at %s", c.Route.Verb, c.Route.Path, c.Route.Pos))
		}
		cmd.Log.Error(fmt.Sprintf("GHTMX-E0404: route constructor name %q collides across routes: %s", group[0].Name, strings.Join(sites, "; ")))
	}
	if cmd.Args.FileName == "" && !cmd.Args.Check {
		// Bootstrap the central package only when it does not exist yet:
		// events are collected during the file pass, so rewriting an
		// existing file here would strip its emitters until the
		// post-pass refresh. Check mode writes nothing, so the post-pass
		// record is the single source of drift.
		if target := cmd.centralFilePath(modRoot); target != "" {
			if _, statErr := os.Stat(target); os.IsNotExist(statErr) {
				if err := cmd.writeCentralPackage(table, modRoot, nil); err != nil {
					return fmt.Errorf("failed to write the central generated package: %w", err)
				}
			}
		}
	}
	var routeMu sync.Mutex
	// refreshCentral and the watch closures below share Run's table and
	// constructors variables. Mutation happens only in rediscoverRoutes on
	// the post-generation goroutine; every other reader runs on that same
	// goroutine or after grp.Wait, so the mutex covers the mutators and
	// cross-checks the invariant.
	cmd.refreshCentral = func() error {
		routeMu.Lock()
		currentTable := table
		routeMu.Unlock()
		return cmd.writeCentralPackage(currentTable, modRoot, cmd.centralEvents(setAnalysis, modRoot))
	}
	buildCache := cmd.openBuildCache(table, constructors, modRoot, modulePath)
	cmd.walkDone = &atomic.Bool{}
	cmd.centralChecked = &atomic.Bool{}
	if cmd.Args.Watch {
		startupBatchSeen := &atomic.Bool{}
		cmd.rediscoverRoutes = func(ctx context.Context) {
			if !cmd.walkDone.Load() {
				return // The startup table is still fresh during the walk.
			}
			if startupBatchSeen.CompareAndSwap(false, true) {
				// The first batch after the walk carries the walk's own Go
				// events; the startup discovery just covered them.
				return
			}
			// Diagnostic-level errors are legitimate long-lived states and
			// are already logged; only a structural load failure keeps the
			// previous table.
			newTable, newFragRefs, _, _, _, loadFailed := cmd.discoverRoutes()
			if loadFailed {
				cmd.Log.Warn("Keeping the previous route table until discovery recovers")
				return
			}
			setAnalysis.MarkGoFragmentRefs(newFragRefs)
			routeMu.Lock()
			changed := build.RoutesChanged(table, newTable)
			if changed {
				table = newTable
				var conflicts [][]central.Constructor
				constructors, conflicts = central.Naming(newTable)
				for _, group := range conflicts {
					var sites []string
					for _, c := range group {
						sites = append(sites, fmt.Sprintf("%s %s at %s", c.Route.Verb, c.Route.Path, c.Route.Pos))
					}
					cmd.Log.Error(fmt.Sprintf("GHTMX-E0404: route constructor name %q collides across routes: %s", group[0].Name, strings.Join(sites, "; ")))
				}
			}
			currentConstructors := constructors
			routeMu.Unlock()
			if !changed {
				return
			}
			cmd.Log.Info("Route registrations changed; refreshing bindings and bound templates")
			if cmd.fsehRef != nil {
				cmd.fsehRef.UpdateRouteBindings(newTable, currentConstructors)
				bound := build.NewGraph(setAnalysis.FileDependencyFacts()).BoundFiles()
				for _, f := range bound {
					if _, err := cmd.fsehRef.HandleDependent(ctx, f); err != nil {
						cmd.Log.Error("Failed to regenerate bound template", slog.String("file", f), slog.Any("error", err))
					}
				}
			}
			// refreshCentral runs unconditionally right after this in the
			// batch pipeline; no extra write needed here.
		}
		cmd.watchCheck = func() {
			if !cmd.walkDone.Load() {
				return // Partial facts mid-walk would yield spurious warnings.
			}
			checkSink := diag.NewSink(cmd.Args.Config.SeverityOverrides())
			routeMu.Lock()
			currentTable, currentConstructors := table, constructors
			routeMu.Unlock()
			setAnalysis.Check(currentTable, checkSink)
			for _, d := range checkSink.Diagnostics() {
				if d.Severity == diag.Error {
					cmd.Log.Error(d.String())
					continue
				}
				cmd.Log.Warn(d.String())
			}
			for _, msg := range central.EventCollisions(currentConstructors, cmd.centralEvents(setAnalysis, modRoot)) {
				cmd.Log.Error("GHTMX-E0404: " + msg)
			}
		}
		cmd.dependentsOf = func(file string) []string {
			if !cmd.walkDone.Load() {
				return nil
			}
			all := build.NewGraph(setAnalysis.FileDependencyFacts()).OnTemplateChange(file)
			deps := all[:0]
			for _, f := range all {
				if f != file {
					deps = append(deps, f)
				}
			}
			return deps
		}
	}

	cmd.Log.Debug("Creating filesystem event handler")
	fseh := NewFSEventHandler(
		cmd.Log,
		cmd.Args.Path,
		cmd.Args.Watch,
		opts,
		cmd.Args.GenerateSourceMapVisualisations,
		cmd.Args.KeepOrphanedFiles,
		cmd.Args.FileWriter,
		cmd.Args.Lazy,
		WithGeneratedSuffix(cmd.Args.Config.GeneratedSuffix),
		WithTemplateExtension(cmd.Args.Config.TemplateExtension),
		attributeValidationOption(cmd.Log, cmd.Args.Config),
		WithRouteBindings(table, modulePath, cmd.Args.Config.GeneratedPackage.Name, constructors),
		WithCentralFile(cmd.centralFilePath(modRoot)),
		WithSetAnalysis(setAnalysis),
		WithBuildCache(buildCache),
		WithSeverityOverrides(cmd.Args.Config.SeverityOverrides()),
		WithStaleDiagnostics(!cmd.Args.Check),
	)
	cmd.fsehRef = fseh

	// If we're processing a single file, don't bother setting up the channels/multithreaing.
	if cmd.Args.FileName != "" {
		_, err = fseh.HandleEvent(ctx, fsnotify.Event{
			Name: cmd.Args.FileName,
			Op:   fsnotify.Create,
		})
		if hits, misses, _ := buildCache.Stats(); hits+misses > 0 {
			cmd.Log.Info("Build cache", slog.Int64("hits", hits), slog.Int64("misses", misses))
		}
		return err
	}

	// Start timer.
	start := time.Now()

	// For the initial filesystem walk and subsequent (optional) fsnotify events.
	events := make(chan fsnotify.Event)
	// For errs from the watcher.
	errs := make(chan error)

	// Start process to push events into the events channel.
	grp, ctx := errgroup.WithContext(ctx)
	grp.Go(func() error {
		defer close(events)
		cmd.walkAndWatch(ctx, events, errs)
		return nil
	})

	// For triggering actions after generation has completed.
	postGeneration := make(chan *GenerationEvent, 256)

	// Start process to handle events.
	grp.Go(func() error {
		defer close(postGeneration)
		cmd.handleEvents(ctx, events, errs, fseh, postGeneration)
		return nil
	})

	// Start process to handle post-generation events.
	var updates int
	grp.Go(func() error {
		defer close(errs)
		updates, err = cmd.handlePostGenerationEvents(ctx, postGeneration)
		return err
	})

	// Read errors.
	var errorCount int
	for err := range errs {
		if err == nil {
			continue
		}
		if errors.Is(err, FatalError{}) {
			cmd.Log.Debug("Fatal error, exiting")
			return err
		}
		cmd.Log.Error("Error", slog.Any("error", err))
		errorCount++
	}

	// Wait for everything to complete.
	cmd.Log.Debug("Waiting for processes to complete")
	if err = grp.Wait(); err != nil {
		return err
	}
	if cmd.Args.Command != "" {
		cmd.Log.Debug("Killing command", slog.String("command", cmd.Args.Command))
		if err := run.KillAll(); err != nil {
			cmd.Log.Error("Error killing command", slog.Any("error", err))
		}
	}

	// Clean up temporary watch mode text files.
	if err := cmd.deleteWatchModeTextFiles(); err != nil {
		cmd.Log.Warn("Failed to delete watch mode text files", slog.Any("error", err))
	}

	if hits, misses, _ := buildCache.Stats(); hits+misses > 0 {
		cmd.Log.Info("Build cache", slog.Int64("hits", hits), slog.Int64("misses", misses))
	}

	// Second central write: event declarations collected during the file
	// pass join the generated package (FR-037). Symbol collisions with
	// route constructors are E0404 errors; the colliding events are
	// excluded from emission.
	centralEvents := cmd.centralEvents(setAnalysis, modRoot)
	for _, msg := range central.EventCollisions(constructors, centralEvents) {
		errorCount++
		cmd.Log.Error("GHTMX-E0404: " + msg)
	}
	// Emitted-symbol semantics: a paramless route named HTMXScript emits
	// HTMXScriptPath, which coexists with the helper.
	if c, taken := constructors["HTMXScript"]; taken && len(c.Route.Params) > 0 {
		errorCount++
		cmd.Log.Error(fmt.Sprintf("GHTMX-E0404: route %s %s (%s) claims the reserved symbol HTMXScript; rename the handler", c.Route.Verb, c.Route.Path, c.Route.Handler))
	}
	if err := cmd.writeCentralPackage(table, modRoot, centralEvents); err != nil {
		return fmt.Errorf("failed to write the central generated package: %w", err)
	}

	// Whole-set checks run after every file was analyzed: dangling swap
	// targets (FR-042, warning unless strict mode promotes it) and
	// unreachable routes (FR-043, warning).
	if !cmd.Args.Watch {
		checkSink := diag.NewSink(cmd.Args.Config.SeverityOverrides())
		setAnalysis.Check(table, checkSink)
		for _, d := range checkSink.Diagnostics() {
			if d.Severity == diag.Error {
				errorCount++
				cmd.Log.Error(d.String())
				continue
			}
			cmd.Log.Warn(d.String())
		}
	}

	// Check for errors after everything has completed. Discovery errors are
	// error-level diagnostics and fail the run (FR-051, FR-060).
	if errorCount+discoveryErrors > 0 {
		return fmt.Errorf("generation completed with %d errors", errorCount+discoveryErrors)
	}

	// Verbose per-phase attribution (NFR-001): parse, discovery, analyze,
	// and emit, so a budget breach is immediately attributable.
	parseTime, analyzeTime, emitTime := fseh.PhaseTimings()
	cmd.Log.Debug("Phase timing",
		slog.Duration("parse", parseTime),
		slog.Duration("discovery", discoveryDuration),
		slog.Duration("analyze", analyzeTime),
		slog.Duration("emit", emitTime),
	)
	cmd.Log.Info("Complete", slog.Int("updates", updates), slog.Duration("duration", time.Since(start)))
	return nil
}

func (cmd Generate) groupUntilNoMessagesReceivedFor100ms(postGeneration chan *GenerationEvent) (grouped *GenerationEvent, updates int, ok bool, err error) {
	timeout := time.NewTimer(time.Hour * 24 * 365)
loop:
	for {
		select {
		case ge := <-postGeneration:
			if ge == nil {
				cmd.Log.Debug("Post-generation event channel closed, exiting")
				if grouped != nil {
					return grouped, updates, true, nil
				}
				return nil, 0, false, nil
			}
			if grouped == nil {
				// Copy with a zeroed counter: the merge below adds every
				// event's errors exactly once, including the first.
				first := *ge
				first.Errors = 0
				grouped = &first
			}
			grouped.GoFileWritten = grouped.GoFileWritten || ge.GoFileWritten
			grouped.WatchedFileUpdated = grouped.WatchedFileUpdated || ge.WatchedFileUpdated
			grouped.TemplFileTextUpdated = grouped.TemplFileTextUpdated || ge.TemplFileTextUpdated
			grouped.TemplFileGoUpdated = grouped.TemplFileGoUpdated || ge.TemplFileGoUpdated
			grouped.GoSourceUpdated = grouped.GoSourceUpdated || ge.GoSourceUpdated
			grouped.Errors += ge.Errors
			if ge.GoFileWritten {
				updates++
			}
			// Now we have received an event, wait for 100ms.
			// If no further messages are received in that time, the timeout will trigger.
			timeout = time.NewTimer(time.Millisecond * 100)
		case <-timeout.C:
			// If grouped is nil, or if nothing observable happened, reset
			// the timer and continue waiting. A batch holding only errors
			// IS observable (FR-063): it must return so the suppression
			// policy runs — holding it would merge the eventual fix into
			// the failing batch and swallow its reload.
			if grouped == nil || (grouped.Errors == 0 && !grouped.GoFileWritten && !grouped.WatchedFileUpdated && !grouped.TemplFileTextUpdated && !grouped.TemplFileGoUpdated) {
				timeout = time.NewTimer(time.Hour * 24 * 365)
				continue loop
			}
			// We have a grouped event, and no events have been sent in the last 100ms, so we need to return.
			return grouped, updates, true, nil
		}
	}
}

func (cmd Generate) handlePostGenerationEvents(ctx context.Context, postGeneration chan *GenerationEvent) (updates int, err error) {
	cmd.Log.Debug("Starting post-generation handler")
	var p *proxy.Handler
	var commandStarted bool
	var pendingRestart, pendingReload bool
loop:
	for {
		grouped, updated, ok, err := cmd.groupUntilNoMessagesReceivedFor100ms(postGeneration)
		if err != nil {
			return 0, fmt.Errorf("error grouping post-generation events: %w", err)
		}
		if !ok {
			break loop
		}

		// The Go application needs to be restarted if any watched non-templ watched files (i.e. non-templ Go files)
		// were updated, or if any Go code within a templ file was updated.
		needsRestart := grouped.WatchedFileUpdated || grouped.TemplFileGoUpdated
		// If the text in a templ file, or any other changes have happened, reload the browser.
		needsBrowserReload := grouped.TemplFileTextUpdated || grouped.TemplFileGoUpdated || grouped.WatchedFileUpdated

		// FR-063: a batch containing failed regenerations must not restart
		// the command or reload the browser — the previous good build
		// keeps serving while the diagnostics sit in the terminal. The
		// batch's obligations are deferred, not dropped: sibling good
		// files' restart and reload needs carry into the next clean
		// batch. Before the first successful command start there is no
		// previous build to protect, so startup still launches the app.
		needsRestart = needsRestart || pendingRestart
		needsBrowserReload = needsBrowserReload || pendingReload
		pendingRestart, pendingReload = false, false
		if grouped.Errors > 0 {
			cmd.Log.Warn("Skipping reload: regeneration reported errors; the previous good build keeps serving",
				slog.Int("errors", grouped.Errors))
			pendingReload = needsBrowserReload
			needsBrowserReload = false
			// Restart suppression protects a running build; before the
			// first successful start there is nothing to protect, so
			// startup still launches the app with its good files.
			if commandStarted {
				pendingRestart = needsRestart
				needsRestart = false
			}
		}

		cmd.Log.Info("Post-generation event received, processing...", slog.Int("updates", updated), slog.Bool("needsRestart", needsRestart), slog.Bool("needsBrowserReload", needsBrowserReload))
		updates += updated

		// A hand-written Go change may have altered route registrations:
		// re-discover once per coalesced batch (FR-061 tier one).
		if cmd.rediscoverRoutes != nil && grouped.GoSourceUpdated {
			cmd.rediscoverRoutes(ctx)
		}

		// Refresh the central package with the events collected so far
		// before any command builds against it or a reload fires.
		if cmd.refreshCentral != nil {
			if err := cmd.refreshCentral(); err != nil {
				cmd.Log.Error("Failed to refresh the central generated package", slog.Any("error", err))
			}
		}

		// Whole-set diagnostics per change, without terminating the watch
		// (FR-061).
		if cmd.watchCheck != nil {
			cmd.watchCheck()
		}

		if cmd.Args.Command != "" && needsRestart {
			cmd.Log.Info("Executing command", slog.String("command", cmd.Args.Command))
			if cmd.Args.Watch {
				if err := os.Setenv("GHTMX_DEV_MODE", "true"); err != nil {
					cmd.Log.Error("Error setting GHTMX_DEV_MODE environment variable", slog.Any("error", err))
				}
				// Check that the path is absolute.
				// It should have already been made absolute at the start of the Run method, but just in case, we need to make sure it's absolute before setting it as an environment variable.
				if !filepath.IsAbs(cmd.Args.Path) {
					cmd.Log.Error("Path is not absolute, this may cause issues with the command execution", slog.String("path", cmd.Args.Path))
				}
				// Evaluate symlinks to match the behavior in runtime/watchmode.go.
				watchRoot := cmd.Args.Path
				if resolved, err := filepath.EvalSymlinks(watchRoot); err == nil {
					watchRoot = resolved
				}
				if err := os.Setenv("GHTMX_DEV_MODE_WATCH_ROOT", watchRoot); err != nil {
					cmd.Log.Error("Error setting GHTMX_DEV_MODE_WATCH_ROOT environment variable", slog.Any("error", err))
				}
			}
			if _, err := run.Run(ctx, cmd.Args.Path, cmd.Args.Command); err != nil {
				cmd.Log.Error("Error executing command", slog.Any("error", err))
			} else {
				commandStarted = true
			}
		}
		if cmd.Args.Proxy != "" {
			if p == nil {
				cmd.Log.Debug("Starting proxy...")
				p, err = cmd.startProxy()
				if err != nil {
					cmd.Log.Error("Failed to start proxy", slog.Any("error", err))
					continue
				}
			}
			if needsBrowserReload {
				cmd.Log.Debug("Sending reload event")
				p.SendSSE("message", "reload")
			}
		}
	}
	return updates, nil
}

func (cmd Generate) handleEvents(ctx context.Context, events chan fsnotify.Event, errs chan error, fseh *FSEventHandler, postGeneration chan *GenerationEvent) {
	var eventsWG sync.WaitGroup
	sem := make(chan struct{}, cmd.Args.WorkerCount)
	cmd.Log.Debug("Starting event handler")
	for event := range events {
		eventsWG.Add(1)
		sem <- struct{}{}
		go func(event fsnotify.Event) {
			cmd.Log.Debug("Processing file", slog.String("file", event.Name))
			defer eventsWG.Done()
			defer func() { <-sem }()
			// A removal purges the file's facts inside HandleEvent, so its
			// dependents must be captured first.
			var removedDeps []string
			if cmd.dependentsOf != nil && event.Op&(fsnotify.Remove|fsnotify.Rename) != 0 && strings.HasSuffix(event.Name, cmd.templateExtension()) {
				removedDeps = cmd.dependentsOf(event.Name)
			}
			r, err := fseh.HandleEvent(ctx, event)
			failures := 0
			if err != nil {
				errs <- err
				failures++
			}
			if failures == 0 && !r.GoFileWritten && !r.WatchedFileUpdated && !r.TemplFileTextUpdated && !r.TemplFileGoUpdated && !r.GoSourceUpdated {
				cmd.Log.Debug("File not updated", slog.String("file", event.Name))
				return
			}
			// Tier-two invalidation (FR-061): a changed template pulls its
			// dependents through regeneration and re-analysis. Dependents
			// are handled directly — no re-expansion, so cycles terminate.
			// Whole-set diagnostics for the refreshed facts are re-emitted
			// when the watch loop gains per-change checks (task 48).
			if cmd.dependentsOf != nil && strings.HasSuffix(event.Name, cmd.templateExtension()) {
				deps := removedDeps
				if deps == nil {
					deps = cmd.dependentsOf(event.Name)
				}
				for _, dep := range deps {
					cmd.Log.Debug("Regenerating dependent", slog.String("file", dep), slog.String("changed", event.Name))
					dr, depErr := fseh.HandleDependent(ctx, dep)
					if depErr != nil {
						errs <- depErr
						failures++
						continue
					}
					r.GoFileWritten = r.GoFileWritten || dr.GoFileWritten
					r.TemplFileTextUpdated = r.TemplFileTextUpdated || dr.TemplFileTextUpdated
					r.TemplFileGoUpdated = r.TemplFileGoUpdated || dr.TemplFileGoUpdated
				}
			}
			e := &GenerationEvent{
				Event:                event,
				GoFileWritten:        r.GoFileWritten,
				WatchedFileUpdated:   r.WatchedFileUpdated,
				TemplFileTextUpdated: r.TemplFileTextUpdated,
				TemplFileGoUpdated:   r.TemplFileGoUpdated,
				GoSourceUpdated:      r.GoSourceUpdated,
				Errors:               failures,
			}
			cmd.Log.Debug("File updated", slog.String("file", event.Name))
			postGeneration <- e
		}(event)
	}
	// Wait for all events to be processed before closing.
	eventsWG.Wait()
}

// discoverRoutes builds the route table by syntax-only analysis of the
// configured route scope (constitution A3.1). Discovery never requires the
// project to compile; a failure to load packages logs a warning and yields
// an empty table, so projects without a Go module still generate.
// Error-level discovery diagnostics (duplicate routes, unresolvable
// registrations) are reported through the handler error path when bindings
// reference them; they are logged here so they are visible even when no
// binding does.
// templateExtension is the project's configured template extension,
// falling back to the default so a zero Config still behaves.
func (cmd *Generate) templateExtension() string {
	if cmd.Args.Config.TemplateExtension == "" {
		return config.DefaultTemplateExtension
	}
	return cmd.Args.Config.TemplateExtension
}

func (cmd *Generate) discoverRoutes() (table *routes.Table, fragRefs map[string]bool, modRoot, modulePath string, errorCount int, loadFailed bool) {
	modRoot, err := modcheck.WalkUp(cmd.Args.Path)
	if err != nil {
		cmd.Log.Debug("route discovery skipped: no go.mod found", slog.Any("error", err))
		return routes.NewTable(), nil, "", "", 0, false
	}
	if data, err := os.ReadFile(filepath.Join(modRoot, "go.mod")); err == nil {
		if mf, err := modfile.ParseLax("go.mod", data, nil); err == nil && mf.Module != nil {
			modulePath = mf.Module.Mod.Path
		}
	}
	sink := diag.NewSink(cmd.Args.Config.SeverityOverrides())
	pkgs, err := routes.Load(cmd.Args.Path, cmd.Args.Config.RouteScope, sink)
	if err != nil {
		// Structural failure (loadFailed), distinct from diagnostic-level
		// errors: the caller must not treat the empty table as truth.
		cmd.Log.Warn("route discovery failed; hx-* bindings will not resolve", slog.Any("error", err))
		return routes.NewTable(), nil, modRoot, modulePath, 0, true
	}
	table = routes.Discover(pkgs, sink)
	// The loaded ASTs also reveal handler-rendered fragments: calls to
	// generated <name>Fragment entry points suppress GHTMX-W0101.
	fragRefs = routes.FragmentEntryRefs(pkgs)
	for _, d := range sink.Diagnostics() {
		if d.Severity == diag.Error {
			errorCount++
			cmd.Log.Error(d.String())
			continue
		}
		cmd.Log.Warn(d.String())
	}
	return table, fragRefs, modRoot, modulePath, errorCount, false
}

// centralEvents converts the whole-set event registry into central
// emission input, with module-relative declaration sites (NFR-004).
// Declaration sites carry the FILE only — like route doc comments,
// a line/column would churn the committed output whenever an edit
// above the declaration shifts it.
func (cmd *Generate) centralEvents(sa *analyzer.SetAnalysis, modRoot string) []central.Event {
	infos := sa.Events()
	out := make([]central.Event, 0, len(infos))
	for _, e := range infos {
		declaredAt := e.Pos.File
		if modRoot != "" {
			if rel, err := filepath.Rel(modRoot, e.Pos.File); err == nil && !strings.HasPrefix(rel, "..") {
				declaredAt = filepath.ToSlash(rel)
			}
		}
		out = append(out, central.Event{Name: e.Name, WireName: e.WireName, Params: e.Params, DeclaredAt: declaredAt})
	}
	return out
}

// centralFilePath returns the absolute path of the central generated
// package's routes file, or "" when there is no module root.
func (cmd *Generate) centralFilePath(modRoot string) string {
	if modRoot == "" {
		return ""
	}
	return filepath.Join(modRoot, cmd.Args.Config.GeneratedPackage.Dir, "routes"+cmd.Args.Config.GeneratedSuffix)
}

// writeCentralPackage emits the central generated package (D5) into the
// configured directory under the module root. The write is skipped when
// nothing would change, and — to avoid polluting projects without routes —
// when the table is empty, no events are declared, and no generated file
// exists yet.
func (cmd *Generate) writeCentralPackage(table *routes.Table, modRoot string, events []central.Event) error {
	if modRoot == "" {
		return nil
	}
	dir := filepath.Dir(cmd.centralFilePath(modRoot))
	target := cmd.centralFilePath(modRoot)
	// A configured htmx version counts as content: HTMXScript() must
	// exist even in a project with no routes and no events yet — unless
	// htmxScript is off, in which case the helper is the only thing the
	// version would have contributed. An already-existing central package
	// is regenerated (to a boilerplate-only file) rather than deleted:
	// generate never removes files it did not just orphan.
	htmxVersion := cmd.Args.Config.HtmxVersion
	if !cmd.Args.Config.EmitHtmxScript() {
		htmxVersion = ""
	}
	if len(table.All()) == 0 && len(events) == 0 && htmxVersion == "" {
		if _, err := os.Stat(target); os.IsNotExist(err) {
			return nil
		}
	}
	version := ""
	if cmd.Args.IncludeVersion {
		version = ghtmx.Version()
	}
	// The after-settle and after-swap emitters exist only while the
	// pinned htmx still honours their response headers (htmx 2).
	omitTriggerAfter := false
	if surface, err := htmxsurface.ForVersion(cmd.Args.Config.HtmxVersion); err == nil {
		_, hasAfterSwap := surface.ResponseHeader("HX-Trigger-After-Swap")
		omitTriggerAfter = !hasAfterSwap
	}
	content, err := central.Generate(table, central.Options{PackageName: cmd.Args.Config.GeneratedPackage.Name, Version: version, ModRoot: modRoot, Events: events, HtmxVersion: htmxVersion, OmitTriggerAfterEmitters: omitTriggerAfter})
	if err != nil {
		return err
	}
	firstWrite := cmd.centralChecked != nil && cmd.centralChecked.CompareAndSwap(false, true)
	existing, readErr := os.ReadFile(target)
	if readErr == nil && string(existing) == string(content) {
		return nil
	}
	if readErr == nil && firstWrite && !cmd.Args.Check {
		// The on-disk central package drifted from its inputs (FR-054).
		sink := diag.NewSink(cmd.Args.Config.SeverityOverrides())
		sink.Add(diag.StaleOutput, diag.Position{File: target, Line: 1, Col: 1},
			"central generated package was stale and has been regenerated",
			"commit the regenerated file, or run ghtmx generate before building")
		for _, d := range sink.Diagnostics() {
			cmd.Log.Warn(d.String())
		}
	}
	if !cmd.Args.Check {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := cmd.Args.FileWriter(target, content); err != nil {
		return err
	}
	cmd.Log.Debug("Central generated package written", slog.String("file", target))
	return nil
}

// attributeValidationOption builds the hx-* validation option from the
// resolved configuration. The version was already validated during argument
// resolution; a failure here only disables validation and is logged.
func attributeValidationOption(log *slog.Logger, cfg config.Config) FSEventHandlerOption {
	surface, err := htmxsurface.ForVersion(cfg.HtmxVersion)
	if err != nil {
		log.Error("hx-* attribute validation disabled", slog.Any("error", err))
		return func(h *FSEventHandler) {}
	}
	return WithAttributeValidation(surface, cfg.SeverityOverrides())
}

func (cmd *Generate) walkAndWatch(ctx context.Context, events chan fsnotify.Event, errs chan error) {
	dirs := cmd.Args.SourceDirs
	if len(dirs) == 0 {
		dirs = []string{cmd.Args.Path}
	}
	cmd.Log.Debug("Walking directories", slog.Any("paths", dirs), slog.Bool("devMode", cmd.Args.Watch))
	for _, dir := range dirs {
		if err := watcher.WalkFiles(ctx, dir, cmd.Args.WatchPattern, cmd.Args.IgnorePattern, cmd.ShouldSkip, events); err != nil {
			cmd.Log.Error("WalkFiles failed, exiting", slog.Any("error", err))
			errs <- FatalError{Err: fmt.Errorf("failed to walk files: %w", err)}
			return
		}
	}
	if cmd.walkDone != nil {
		cmd.walkDone.Store(true)
	}
	if !cmd.Args.Watch {
		cmd.Log.Debug("Dev mode not enabled, process can finish early")
		return
	}
	cmd.Log.Info("Watching files")
	rw, err := watcher.Recursive(ctx, cmd.Args.WatchPattern, cmd.Args.IgnorePattern, cmd.ShouldSkip, events, errs)
	if err != nil {
		cmd.Log.Error("Recursive watcher setup failed, exiting", slog.Any("error", err))
		errs <- FatalError{Err: fmt.Errorf("failed to setup recursive watcher: %w", err)}
		return
	}
	for _, dir := range dirs {
		if err = rw.Add(dir); err != nil {
			cmd.Log.Error("Failed to add path to watcher", slog.Any("error", err))
			errs <- FatalError{Err: fmt.Errorf("failed to add path to watcher: %w", err)}
			return
		}
	}
	defer func() {
		if err := rw.Close(); err != nil {
			cmd.Log.Error("Failed to close watcher", slog.Any("error", err))
		}
	}()
	cmd.Log.Debug("Waiting for context to be cancelled to stop watching files")
	<-ctx.Done()
}

func (cmd *Generate) deleteWatchModeTextFiles() error {
	return fs.WalkDir(os.DirFS(cmd.Args.Path), ".", func(path string, info os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		absPath, err := filepath.Abs(filepath.Join(cmd.Args.Path, path))
		if err != nil {
			return nil
		}
		if info.IsDir() && skipdir.ShouldSkip(absPath) {
			return filepath.SkipDir
		}
		if cmd.ShouldSkip != nil && cmd.ShouldSkip(path) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		generatedSuffix := cmd.Args.Config.GeneratedSuffix
		if generatedSuffix == "" {
			generatedSuffix = "_ghtmx.go"
		}
		if !strings.HasSuffix(absPath, generatedSuffix) && !strings.HasSuffix(absPath, cmd.templateExtension()) {
			return nil
		}
		watchModeFileName := ghtmxruntime.GetDevModeTextFileName(absPath)
		if err := os.Remove(watchModeFileName); err != nil && !errors.Is(err, os.ErrNotExist) {
			cmd.Log.Warn("Failed to remove watch mode text file", slog.Any("error", err))
		}
		return nil
	})
}

func (cmd *Generate) createTLSTransport() *http.Transport {
	certPEM, err := os.ReadFile(cmd.Args.ProxyTLSCrt)
	if err != nil {
		cmd.Log.Error("Failed to read TLS certificate file", slog.Any("error", err))
		return nil
	}
	certPool := x509.NewCertPool()
	if !certPool.AppendCertsFromPEM(certPEM) {
		cmd.Log.Error("Failed to append certificate to pool")
		return nil
	}
	return &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: certPool},
	}
}

func (cmd *Generate) startProxy() (p *proxy.Handler, err error) {
	var target *url.URL
	target, err = url.Parse(cmd.Args.Proxy)
	if err != nil {
		return nil, FatalError{Err: fmt.Errorf("failed to parse proxy URL: %w", err)}
	}
	scheme := "http"
	if cmd.Args.ProxyTLSCrt != "" && cmd.Args.ProxyTLSKey != "" {
		scheme = "https"
	}
	p = proxy.New(cmd.Log, scheme, cmd.Args.ProxyBind, cmd.Args.ProxyPort, target)
	go func() {
		cmd.Log.Info("Proxying", slog.String("from", p.URL), slog.String("to", p.Target.String()))
		server := &http.Server{
			Addr:    fmt.Sprintf("%s:%d", cmd.Args.ProxyBind, cmd.Args.ProxyPort),
			Handler: p,
		}
		// Configure TLS if certificates are provided.
		if cmd.Args.ProxyTLSCrt != "" && cmd.Args.ProxyTLSKey != "" {
			cert, err := tls.LoadX509KeyPair(cmd.Args.ProxyTLSCrt, cmd.Args.ProxyTLSKey)
			if err != nil {
				cmd.Log.Error("Failed to load TLS certificates", slog.Any("error", err))
				return
			}
			server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{cert}}
			if err = server.ListenAndServeTLS(cmd.Args.ProxyTLSCrt, cmd.Args.ProxyTLSKey); err != nil {
				cmd.Log.Error("Proxy failed", slog.Any("error", err))
			}
			return
		}
		if err := server.ListenAndServe(); err != nil {
			cmd.Log.Error("Proxy failed", slog.Any("error", err))
		}
	}()
	if !cmd.Args.OpenBrowser {
		cmd.Log.Debug("Not opening browser")
		return p, nil
	}
	go func() {
		cmd.Log.Debug("Waiting for proxy to be ready", slog.String("url", p.URL))
		backoff := backoff.NewExponentialBackOff()
		backoff.InitialInterval = time.Second
		var client http.Client
		client.Timeout = 1 * time.Second
		// Configure TLS with CA pool for self-signed certificates on localhost.
		if cmd.Args.ProxyTLSCrt != "" && cmd.Args.ProxyTLSKey != "" {
			client.Transport = cmd.createTLSTransport()
		}
		for {
			if resp, err := client.Get(p.URL); err == nil {
				if resp.StatusCode != http.StatusBadGateway {
					break
				}
			}
			d := backoff.NextBackOff()
			cmd.Log.Debug("Proxy not ready, retrying", slog.String("url", p.URL), slog.Any("backoff", d))
			time.Sleep(d)
		}
		if err := browser.OpenURL(p.URL); err != nil {
			cmd.Log.Error("Failed to open browser", slog.Any("error", err))
		}
	}()
	return p, nil
}

// openBuildCache opens the on-disk build cache (D6, NFR-001) for modes
// whose output is a pure function of source content, configuration, and
// binding state. The salt folds in everything else that shapes generated
// output, so a toolchain, config, or route change orphans old entries.
func (cmd *Generate) openBuildCache(table *routes.Table, constructors map[string]central.Constructor, modRoot, modulePath string) *buildcache.Store {
	if !cmd.Args.Cache || cmd.Args.Watch || cmd.Args.IncludeTimestamp || cmd.Args.GenerateSourceMapVisualisations || modRoot == "" {
		return nil
	}
	buildID, ok := toolchainIdentity()
	if !ok {
		cmd.Log.Debug("Build cache disabled: no stable toolchain identity (devel or dirty build)")
		return nil
	}
	dir, ok := buildcache.DefaultDir(modRoot)
	if !ok {
		return nil
	}
	// Bindings shape generated output: serialize the route table and the
	// constructor naming deterministically into the salt. Every field is
	// quoted so no two states can serialize identically.
	var routeState strings.Builder
	names := make([]string, 0, len(constructors))
	for name := range constructors {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		c := constructors[name]
		fmt.Fprintf(&routeState, "%q=%q %q %q.%q;", name, c.Route.Verb, c.Route.Path, c.Route.Handler.PkgPath, c.Route.Handler.Name)
	}
	for _, r := range table.All() {
		fmt.Fprintf(&routeState, "%q %q %q.%q", r.Verb, r.Path, r.Handler.PkgPath, r.Handler.Name)
		for _, p := range r.Params {
			fmt.Fprintf(&routeState, "/%q:%t", p.Name, p.Wildcard)
		}
		routeState.WriteString(";")
	}
	// The generation root shapes unit IDs and embedded file names; a
	// different -path must not share entries.
	pathBase, err := filepath.Rel(modRoot, cmd.Args.Path)
	if err != nil {
		pathBase = cmd.Args.Path
	}
	salt := buildcache.Salt(
		buildID,
		cmd.Args.Config.Hash(),
		modulePath,
		pathBase,
		strconv.FormatBool(cmd.Args.IncludeVersion),
		routeState.String(),
	)
	store, err := buildcache.Open(dir, salt)
	if err != nil {
		cmd.Log.Debug("Build cache unavailable", slog.Any("error", err))
		return nil
	}
	return store
}

// toolchainIdentity returns a string identifying the exact generator
// binary: generated output is a function of the code that produced it, so
// the embedded version alone is not enough — two devel builds share it.
// Unstamped or dirty builds have no stable identity and get no cache;
// GHTMX_BUILD_CACHE_SALT overrides for tests.
func toolchainIdentity() (id string, ok bool) {
	if env := os.Getenv("GHTMX_BUILD_CACHE_SALT"); env != "" {
		return env + "|" + goruntime.Version(), true
	}
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	var revision, modified string
	for _, s := range bi.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value
		}
	}
	if modified == "true" {
		return "", false
	}
	if bi.Main.Version == "(devel)" && revision == "" {
		return "", false
	}
	return strings.Join([]string{ghtmx.Version(), bi.Main.Version, revision, bi.GoVersion}, "|"), true
}
