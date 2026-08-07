package generatecmd

import (
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"runtime"
	"slices"
	"strings"

	_ "net/http/pprof"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/sloghandler"
	"github.com/go-monolith/ghtmx/internal/config"
	"github.com/go-monolith/ghtmx/internal/diag"
	"github.com/go-monolith/ghtmx/internal/htmxsurface"
)

const generateUsageText = `usage: ghtmx generate [<args>...]

Generates Go code from templ files.

Args:
  -path <path>
    Generates code for all files in path. (default .)
  -f <file>
    Optionally generates code for a single file, e.g. -f header.ghtmx
  -stdout
    Prints to stdout instead of writing generated files to the filesystem.
    Only applicable when -f is used.
  -source-map-visualisations
    Set to true to generate HTML files to visualise the templ code and its corresponding Go code.
  -include-version
    Set to false to skip inclusion of the ghtmx version in the generated code. (default true)
  -include-timestamp
    Set to true to include the current time in the generated code.
  -watch
    Set to true to watch the path for changes and regenerate code.
  -watch-pattern <regexp>
    Set the regexp pattern of files that will be watched for changes. (default: '(.+\.go$)|(.+\.ghtmx$)', following -template-extension)
  -ignore-pattern <regexp>
    Set the regexp pattern of files to ignore when watching for changes. (default: '')
  -open-browser
    Set to false to prevent the browser from opening when using the -proxy flag. (default true)
  -cmd <cmd>
    Set the command to run after generating code. The command is executed via
    the system shell ($SHELL on Unix, %COMSPEC% on Windows).
  -proxy
    Set the URL to proxy after generating code and executing the command.
  -proxyport
    The port the proxy will listen on. (default 7331)
  -proxybind
    The address the proxy will listen on. (default 127.0.0.1)
  -proxy-tls-crt <file>
    Path to a TLS certificate file to serve the proxy over HTTPS. Must be used with -proxy-tls-key and -proxy.
  -proxy-tls-key <file>
    Path to a TLS key file to serve the proxy over HTTPS. Must be used with -proxy-tls-crt and -proxy.
  -notify-proxy
    If present, the command will issue a reload event to the proxy 127.0.0.1:7331, or use proxyport and proxybind to specify a different address.
  -w
    Number of workers to use when generating code. (default runtime.NumCPUs)
  -lazy
    Only generate .go files if the source .ghtmx file is newer.
  -cache
    Serve unchanged templates from the on-disk build cache. (default true)
  -pprof
    Port to run the pprof server on.
  -keep-orphaned-files
    Keeps orphaned generated templ files. (default false)
  -check
    Checks that generated files are up to date, without writing changes.
    Returns a non-zero exit code if any files need regenerating.
  -htmx-version <version>
    The pinned htmx version driving attribute validation. (default from ghtmx.json, else 2.0.10)
  -source-dir <dir>
    Template source directory; repeat the flag for several directories. (default from ghtmx.json, else .)
  -route-scope <pattern>
    Go package pattern scanned by route discovery; repeatable. (default from ghtmx.json, else ./...)
  -generated-pkg-dir <dir>
    Directory of the central generated package. (default from ghtmx.json, else ghtmxgen)
  -generated-pkg-name <name>
    Package name of the central generated package. (default from ghtmx.json, else ghtmxgen)
  -template-extension <ext>
    Template file extension: .ghtmx (default) or .htmx. (default from ghtmx.json, else .ghtmx)
  -generated-suffix <suffix>
    Generated Go file suffix replacing the .ghtmx extension. (default from ghtmx.json, else _ghtmx.go)
  -check-severity <ID=severity>
    Override a warning-class check severity, e.g. -check-severity GHTMX-W0101=off; repeatable.
  -strict-targets
    Promote dangling swap target warnings (GHTMX-W0201) to errors.
  -htmx-script
    Set to false to omit the ghtmxgen.HTMXScript() helper for projects
    that load no htmx at all. (default true)
  -v
    Set log verbosity level to "debug". (default "info")
  -log-level
    Set log verbosity level. (default "info", options: "debug", "info", "warn", "error")
  -help
    Print help and exit.

Configuration is read from ghtmx.json in the current directory when present.
Precedence is flag > configuration file > default.

Examples:

  Generate code for all files in the current directory and subdirectories:

    ghtmx generate

  Generate code for a single file:

    ghtmx generate -f header.ghtmx

  Watch the current directory and subdirectories for changes and regenerate code:

    ghtmx generate -watch

  Check generated code is up to date (e.g. in CI):

    ghtmx generate -check
`

// defaultWatchPattern watches Go sources and the project's templates. The
// template extension is configurable, so the pattern is derived rather
// than constant.
func defaultWatchPattern(templateExtension string) string {
	return `(.+\.go$)|(.+\` + templateExtension + `$)`
}

// stringSliceFlag collects repeatable string flags.
type stringSliceFlag []string

func (f *stringSliceFlag) String() string { return strings.Join(*f, ",") }

func (f *stringSliceFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// severityMapFlag collects repeatable ID=severity overrides.
type severityMapFlag map[string]diag.Severity

func (f severityMapFlag) String() string { return "" }

func (f severityMapFlag) Set(v string) error {
	id, sev, ok := strings.Cut(v, "=")
	if !ok || id == "" || sev == "" {
		return fmt.Errorf("expected ID=severity, e.g. GHTMX-W0101=off, got %q", v)
	}
	f[id] = diag.Severity(sev)
	return nil
}

func NewArguments(stdout, stderr io.Writer, args []string) (cmdArgs Arguments, log *slog.Logger, help bool, err error) {
	cmd := flag.NewFlagSet("generate", flag.ContinueOnError)
	// Discarded, not sent to stderr: every flag here is registered with
	// an empty description, so the flag package's auto-generated listing
	// is noise beside generateUsageText. Matches the other subcommands.
	cmd.SetOutput(io.Discard)
	cmd.StringVar(&cmdArgs.FileName, "f", "", "")
	cmd.StringVar(&cmdArgs.Path, "path", ".", "")
	toStdoutFlag := cmd.Bool("stdout", false, "")
	cmd.BoolVar(&cmdArgs.GenerateSourceMapVisualisations, "source-map-visualisations", false, "")
	cmd.BoolVar(&cmdArgs.IncludeVersion, "include-version", true, "")
	cmd.BoolVar(&cmdArgs.IncludeTimestamp, "include-timestamp", false, "")
	cmd.BoolVar(&cmdArgs.Watch, "watch", false, "")
	watchPatternFlag := cmd.String("watch-pattern", "", "")
	ignorePatternFlag := cmd.String("ignore-pattern", "", "")
	cmd.BoolVar(&cmdArgs.OpenBrowser, "open-browser", true, "")
	cmd.StringVar(&cmdArgs.Command, "cmd", "", "")
	cmd.StringVar(&cmdArgs.Proxy, "proxy", "", "")
	cmd.IntVar(&cmdArgs.ProxyPort, "proxyport", 7331, "")
	cmd.StringVar(&cmdArgs.ProxyBind, "proxybind", "127.0.0.1", "")
	cmd.StringVar(&cmdArgs.ProxyTLSCrt, "proxy-tls-crt", "", "")
	cmd.StringVar(&cmdArgs.ProxyTLSKey, "proxy-tls-key", "", "")
	cmd.BoolVar(&cmdArgs.NotifyProxy, "notify-proxy", false, "")
	cmd.IntVar(&cmdArgs.WorkerCount, "w", runtime.NumCPU(), "")
	cmd.IntVar(&cmdArgs.PPROFPort, "pprof", 0, "")
	cmd.BoolVar(&cmdArgs.KeepOrphanedFiles, "keep-orphaned-files", false, "")
	cmd.BoolVar(&cmdArgs.Lazy, "lazy", false, "")
	cmd.BoolVar(&cmdArgs.Cache, "cache", true, "")
	cmd.BoolVar(&cmdArgs.Check, "check", false, "")
	htmxVersionFlag := cmd.String("htmx-version", "", "")
	var sourceDirsFlag stringSliceFlag
	cmd.Var(&sourceDirsFlag, "source-dir", "")
	var routeScopeFlag stringSliceFlag
	cmd.Var(&routeScopeFlag, "route-scope", "")
	generatedPkgDirFlag := cmd.String("generated-pkg-dir", "", "")
	generatedPkgNameFlag := cmd.String("generated-pkg-name", "", "")
	generatedSuffixFlag := cmd.String("generated-suffix", "", "")
	templateExtensionFlag := cmd.String("template-extension", "", "")
	checkSeverityFlag := severityMapFlag{}
	cmd.Var(checkSeverityFlag, "check-severity", "")
	strictTargetsFlag := cmd.Bool("strict-targets", false, "")
	htmxScriptFlag := cmd.Bool("htmx-script", true, "")
	verboseFlag := cmd.Bool("v", false, "")
	logLevelFlag := cmd.String("log-level", "info", "")
	helpFlag := cmd.Bool("help", false, "")
	if err = cmd.Parse(args); err != nil {
		// -h is not a registered flag; the flag package reports it as
		// ErrHelp rather than a parse failure. Asking for help is not an
		// error: it belongs on stdout with a zero exit, the same as
		// -help and the same as every other subcommand.
		if errors.Is(err, flag.ErrHelp) {
			return Arguments{}, sloghandler.NewLogger("info", false, stderr), true, nil
		}
		return Arguments{}, nil, false, fmt.Errorf("failed to parse arguments: %w", err)
	}

	log = sloghandler.NewLogger(*logLevelFlag, *verboseFlag, stderr)

	if cmdArgs.Watch && cmdArgs.FileName != "" {
		return Arguments{}, log, *helpFlag, fmt.Errorf("cannot watch a single file, remove the -f or -watch flag")
	}
	if cmdArgs.Check && cmdArgs.Watch {
		return Arguments{}, log, *helpFlag, fmt.Errorf("cannot use -check with -watch")
	}
	if cmdArgs.Check && *toStdoutFlag {
		return Arguments{}, log, *helpFlag, fmt.Errorf("cannot use -check with -stdout")
	}
	if *ignorePatternFlag != "" {
		cmdArgs.IgnorePattern, err = regexp.Compile(*ignorePatternFlag)
		if err != nil {
			return cmdArgs, log, *helpFlag, fmt.Errorf("invalid ignore pattern %q: %w", *ignorePatternFlag, err)
		}
	}

	// Default to writing to files unless the stdout flag is set.
	cmdArgs.FileWriter = FileWriter
	if *toStdoutFlag {
		if cmdArgs.FileName == "" {
			return Arguments{}, log, *helpFlag, fmt.Errorf("only a single file can be output to stdout, add the -f flag to specify the file to generate code for")
		}
		cmdArgs.FileWriter = WriterFileWriter(stdout)
	}

	// Validate TLS certificate flags.
	if (cmdArgs.ProxyTLSCrt == "") != (cmdArgs.ProxyTLSKey == "") {
		return Arguments{}, log, *helpFlag, fmt.Errorf("both -proxy-tls-crt and -proxy-tls-key must be provided together")
	}
	if cmdArgs.ProxyTLSCrt != "" && cmdArgs.Proxy == "" {
		return Arguments{}, log, *helpFlag, fmt.Errorf("-proxy-tls-crt and -proxy-tls-key can only be used with the -proxy flag")
	}

	// Resolve project configuration: flag > ghtmx.json > default (FR-073).
	// The file is discovered at the generation target, not the invoker's
	// working directory, so `ghtmx generate -path elsewhere` honours that
	// project's configuration.
	fileCfg, err := config.Load(cmdArgs.Path)
	if err != nil {
		return Arguments{}, log, *helpFlag, err
	}
	flags := config.Flags{
		SourceDirs:      sourceDirsFlag,
		RouteScope:      routeScopeFlag,
		CheckSeverities: checkSeverityFlag,
	}
	pathSet := false
	cmd.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "htmx-version":
			flags.HtmxVersion = htmxVersionFlag
		case "generated-pkg-dir":
			flags.GeneratedPkgDir = generatedPkgDirFlag
		case "generated-pkg-name":
			flags.GeneratedPkgName = generatedPkgNameFlag
		case "generated-suffix":
			flags.GeneratedSuffix = generatedSuffixFlag
		case "template-extension":
			flags.TemplateExtension = templateExtensionFlag
		case "strict-targets":
			flags.StrictTargets = strictTargetsFlag
		case "htmx-script":
			flags.HtmxScript = htmxScriptFlag
		case "path":
			pathSet = true
		}
	})
	cmdArgs.Config = config.Resolve(fileCfg, flags)
	if err := cmdArgs.Config.Validate(); err != nil {
		return Arguments{}, log, *helpFlag, err
	}
	// Compiled only now: the default watches the configured template
	// extension, which is not known until the config has resolved.
	watchPattern := *watchPatternFlag
	if watchPattern == "" {
		watchPattern = defaultWatchPattern(cmdArgs.Config.TemplateExtension)
	}
	cmdArgs.WatchPattern, err = regexp.Compile(watchPattern)
	if err != nil {
		return cmdArgs, log, *helpFlag, fmt.Errorf("invalid watch pattern %q: %w", watchPattern, err)
	}
	if _, err := htmxsurface.ForVersion(cmdArgs.Config.HtmxVersion); err != nil {
		return Arguments{}, log, *helpFlag, err
	}
	// The version also needs a pinned script asset: failing here beats a
	// render-time E0502 in every page (FR-052, FR-091). With htmxScript
	// disabled no tag is ever rendered, so the asset requirement lapses —
	// only the attribute surface check above still applies. Today the two
	// version sets coincide, so the relaxation is future-proofing for a
	// version that gains a surface before its asset is pinned.
	if cmdArgs.Config.EmitHtmxScript() && !slices.Contains(ghtmx.SupportedHtmxVersions(), cmdArgs.Config.HtmxVersion) {
		return Arguments{}, log, *helpFlag, fmt.Errorf("htmx version %q has no pinned script asset; supported versions: %v", cmdArgs.Config.HtmxVersion, ghtmx.SupportedHtmxVersions())
	}
	// An explicit -path (or -f) narrows generation to that location; the
	// configured source directories apply otherwise.
	if pathSet || cmdArgs.FileName != "" {
		cmdArgs.SourceDirs = []string{cmdArgs.Path}
	} else {
		cmdArgs.SourceDirs = cmdArgs.Config.SourceDirs
		cmdArgs.Path = cmdArgs.SourceDirs[0]
	}

	return cmdArgs, log, *helpFlag, nil
}

type Arguments struct {
	FileName                        string
	FileWriter                      FileWriterFunc
	Path                            string
	Check                           bool
	Watch                           bool
	WatchPattern                    *regexp.Regexp
	IgnorePattern                   *regexp.Regexp
	OpenBrowser                     bool
	Command                         string
	ProxyBind                       string
	ProxyPort                       int
	Proxy                           string
	ProxyTLSCrt                     string
	ProxyTLSKey                     string
	NotifyProxy                     bool
	WorkerCount                     int
	GenerateSourceMapVisualisations bool
	IncludeVersion                  bool
	IncludeTimestamp                bool
	// PPROFPort is the port to run the pprof server on.
	PPROFPort         int
	KeepOrphanedFiles bool
	Lazy              bool
	// Cache enables the on-disk build cache (D6). Modes whose output is
	// not a pure function of source, config, and bindings bypass it
	// regardless.
	Cache bool
	// SourceDirs are the resolved template source directories to walk.
	SourceDirs []string
	// Config is the resolved project configuration
	// (flag > ghtmx.json > default).
	Config config.Config
}

type ArgumentError struct {
	Message string
}

func (e *ArgumentError) Error() string {
	return e.Message
}

func (a *ArgumentError) Code() int {
	return 64 // EX_USAGE
}

func Run(ctx context.Context, stdout, stderr io.Writer, args []string) (err error) {
	cmdArgs, log, help, err := NewArguments(stdout, stderr, args)
	if err != nil {
		_, _ = fmt.Fprint(stderr, generateUsageText)
		return &ArgumentError{
			Message: err.Error(),
		}
	}
	if help {
		_, _ = fmt.Fprint(stdout, generateUsageText)
		return nil
	}
	if cmdArgs.Check {
		// Lazy skips generation when the artifact's mtime is newer than
		// the source — exactly what a hand edit produces. Check mode must
		// always generate to compare.
		cmdArgs.Lazy = false
		var getChanged func() []string
		cmdArgs.FileWriter, getChanged = NewCheckWriter()
		g, err := NewGenerate(log, cmdArgs)
		if err != nil {
			return err
		}
		if err := g.Run(ctx); err != nil {
			return err
		}
		if changed := getChanged(); len(changed) > 0 {
			// Drift is a GHTMX-W0301 diagnostic per file; check mode wrote
			// nothing and the run exits non-zero (FR-054).
			sink := diag.NewSink(cmdArgs.Config.SeverityOverrides())
			for _, f := range changed {
				sink.Add(diag.StaleOutput, diag.Position{File: f, Line: 1, Col: 1},
					"generated output is stale relative to its source",
					"run ghtmx generate and commit the result")
			}
			for _, d := range sink.Diagnostics() {
				if d.Severity == diag.Error {
					log.Error(d.String())
					continue
				}
				log.Warn(d.String())
			}
			// The non-zero exit is independent of diagnostic severity:
			// silencing the log line does not make drift pass.
			return fmt.Errorf("generated files are not up to date: %d file(s) need regenerating", len(changed))
		}
		return nil
	}
	g, err := NewGenerate(log, cmdArgs)
	if err != nil {
		return err
	}
	return g.Run(ctx)
}
