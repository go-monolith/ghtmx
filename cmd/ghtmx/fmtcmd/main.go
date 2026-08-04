package fmtcmd

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/go-monolith/ghtmx/cmd/ghtmx/processor"
	"github.com/go-monolith/ghtmx/internal/config"
	"github.com/go-monolith/ghtmx/internal/format"
	"github.com/go-monolith/ghtmx/internal/ignorefile"
	"github.com/natefinch/atomic"
)

type Arguments struct {
	FailIfChanged bool
	ToStdout      bool
	StdinFilepath string
	Files         []string
	WorkerCount   int
}

func Run(log *slog.Logger, stdin io.Reader, stdout io.Writer, args Arguments) (err error) {
	// The template extension is project configuration, so the walk below
	// and the import rewriting must both use it. A missing or unusable
	// ghtmx.json is not fatal here: formatting still works on the default.
	cfg := config.Default()
	if loaded, cfgErr := config.Load("."); cfgErr != nil {
		log.Warn("ghtmx.json not usable; using defaults", slog.Any("error", cfgErr))
	} else {
		cfg = config.Resolve(loaded, config.Flags{})
	}
	// If no files are provided, read from stdin and write to stdout.
	formatterConfig := format.Config{TemplateExtension: cfg.TemplateExtension}
	if len(args.Files) == 0 {
		src, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("failed to read from stdin: %w", err)
		}
		formatted, _, err := format.Templ(src, args.StdinFilepath, formatterConfig)
		if err != nil {
			return fmt.Errorf("failed to format stdin: %w", err)
		}
		if _, err = stdout.Write(formatted); err != nil {
			return fmt.Errorf("failed to write to stdout: %w", err)
		}
		return nil
	}
	// If files are provided, process each file.
	process := func(fileName string) (error, bool) {
		src, err := os.ReadFile(fileName)
		if err != nil {
			return fmt.Errorf("failed to read file %q: %w", fileName, err), false
		}
		formatted, changed, err := format.Templ(src, fileName, formatterConfig)
		if err != nil {
			return fmt.Errorf("failed to format file %q: %w", fileName, err), false
		}
		if !changed && !args.ToStdout {
			return nil, false
		}
		if args.ToStdout {
			if _, err := stdout.Write(formatted); err != nil {
				return fmt.Errorf("failed to write to stdout: %w", err), false
			}
			return nil, true
		}
		if err := atomic.WriteFile(fileName, bytes.NewBuffer(formatted)); err != nil {
			return fmt.Errorf("failed to write file %q: %w", fileName, err), false
		}
		return nil, true
	}
	// Every path is formatted, not just the first. `ghtmx fmt *.ghtmx`
	// is a natural thing to type, and taking Files[0] alone left the
	// rest untouched with nothing said about it — the shell had already
	// expanded the glob, so the user saw a successful run and assumed
	// the whole set was done.
	var errs []error
	for _, dir := range args.Files {
		shouldSkip, err := ignorefile.ShouldSkipFunc(dir, ".ghtmxignore_fmt")
		if err != nil {
			// Collected rather than returned: aborting here would throw
			// away the failures already gathered from earlier paths and
			// skip the remaining ones.
			errs = append(errs, fmt.Errorf("failed to parse .ghtmxignore_fmt in %q: %w", dir, err))
			continue
		}
		if err := NewFormatter(log, dir, process, args.WorkerCount, args.FailIfChanged, shouldSkip, cfg.TemplateExtension).Run(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

type Formatter struct {
	Log          *slog.Logger
	Dir          string
	Process      func(fileName string) (error, bool)
	WorkerCount  int
	FailIfChange bool
	ShouldSkip   func(string) bool
	// TemplateExtension is the project's configured template extension,
	// which decides what the walk treats as a template.
	TemplateExtension string
}

func NewFormatter(log *slog.Logger, dir string, process func(fileName string) (error, bool), workerCount int, failIfChange bool, shouldSkip func(string) bool, templateExtension string) *Formatter {
	f := &Formatter{
		Log:               log,
		Dir:               dir,
		Process:           process,
		WorkerCount:       workerCount,
		FailIfChange:      failIfChange,
		ShouldSkip:        shouldSkip,
		TemplateExtension: templateExtension,
	}
	if f.WorkerCount == 0 {
		f.WorkerCount = runtime.NumCPU()
	}
	return f
}

func (f *Formatter) Run() (err error) {
	var errs []error
	changesMade := 0
	start := time.Now()
	results := make(chan processor.Result)
	f.Log.Debug("Walking directory", slog.String("path", f.Dir))
	go processor.Process(f.Dir, f.TemplateExtension, f.Process, f.WorkerCount, f.ShouldSkip, results)
	var successCount, errorCount int
	for r := range results {
		if r.ChangesMade {
			changesMade += 1
		}
		if r.Error != nil {
			f.Log.Error(r.FileName, slog.Any("error", r.Error))
			errorCount++
			errs = append(errs, r.Error)
			continue
		}
		f.Log.Debug(r.FileName, slog.Duration("duration", r.Duration))
		successCount++
	}

	if f.FailIfChange && changesMade > 0 {
		f.Log.Error("Templates were valid but not properly formatted", slog.Int("count", successCount+errorCount), slog.Int("changed", changesMade), slog.Int("errors", errorCount), slog.Duration("duration", time.Since(start)))
		return fmt.Errorf("templates in %q were not formatted properly", f.Dir)
	}

	f.Log.Info("Format Complete", slog.Int("count", successCount+errorCount), slog.Int("errors", errorCount), slog.Int("changed", changesMade), slog.Duration("duration", time.Since(start)))

	if err = errors.Join(errs...); err != nil {
		return fmt.Errorf("formatting failed: %w", err)
	}

	return nil
}
