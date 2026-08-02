package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/fatih/color"
	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/fmtcmd"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/infocmd"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/lspcmd"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/routescmd"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/sloghandler"
	"github.com/go-monolith/ghtmx/internal/format"
)

func main() {
	code := run(os.Stdin, os.Stdout, os.Stderr, os.Args)
	if code != 0 {
		os.Exit(code)
	}
}

const usageText = `usage: ghtmx <command> [<args>...]

ghtmx - build htmx-first HTML UIs with Go

commands:
  generate   Generates Go code from ghtmx files
  fmt        Formats ghtmx files
  routes     Prints the discovered route table
  lsp        Starts a language server for ghtmx files
  info       Displays information about the ghtmx environment
  version    Prints the version
`

func run(stdin io.Reader, stdout, stderr io.Writer, args []string) (code int) {
	if len(args) < 2 {
		_, _ = fmt.Fprint(stderr, usageText)
		return 64 // EX_USAGE
	}
	switch args[1] {
	case "info":
		return infoCmd(stdout, stderr, args[2:])
	case "generate":
		return generateCmd(stdout, stderr, args[2:])
	case "fmt":
		return fmtCmd(stdin, stdout, stderr, args[2:])
	case "routes":
		return routesCmd(stdout, stderr, args[2:])
	case "lsp":
		return lspCmd(stdin, stdout, stderr, args[2:])
	case "version", "--version":
		_, _ = fmt.Fprintln(stdout, ghtmx.Version())
		return 0
	case "help", "-help", "--help", "-h":
		_, _ = fmt.Fprint(stdout, usageText)
		return 0
	}
	_, _ = fmt.Fprint(stderr, usageText)
	return 64 // EX_USAGE
}

const infoUsageText = `usage: ghtmx info [<args>...]

Displays information about the ghtmx environment.

Args:
  -json
    Output information in JSON format to stdout. (default false)
  -v
    Set log verbosity level to "debug". (default "info")
  -log-level
    Set log verbosity level. (default "info", options: "debug", "info", "warn", "error")
  -help
    Print help and exit.
`

func infoCmd(stdout, stderr io.Writer, args []string) (code int) {
	cmd := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	// The usage text below is the one users should see; the flag
	// package\'s own output would print alongside it.
	cmd.SetOutput(io.Discard)
	jsonFlag := cmd.Bool("json", false, "")
	verboseFlag := cmd.Bool("v", false, "")
	logLevelFlag := cmd.String("log-level", "info", "")
	helpFlag := cmd.Bool("help", false, "")
	err := cmd.Parse(args)
	if err != nil {
		_, _ = fmt.Fprint(stderr, infoUsageText)
		return 64 // EX_USAGE
	}
	if *helpFlag {
		_, _ = fmt.Fprint(stdout, infoUsageText)
		return
	}

	log := sloghandler.NewLogger(*logLevelFlag, *verboseFlag, stderr)

	ctx, cancel := context.WithCancel(context.Background())
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	go func() {
		<-signalChan
		_, _ = fmt.Fprintln(stderr, "Stopping...")
		cancel()
	}()

	err = infocmd.Run(ctx, log, stdout, infocmd.Arguments{
		JSON: *jsonFlag,
	})
	if err != nil {
		_, _ = color.New(color.FgRed).Fprint(stderr, "(✗) ")
		_, _ = fmt.Fprintln(stderr, "Command failed: "+err.Error())
		return 1
	}
	return 0
}

func generateCmd(stdout, stderr io.Writer, args []string) (code int) {
	ctx, cancel := context.WithCancel(context.Background())
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signalChan
		_, _ = fmt.Fprintln(stderr, "Stopping...")
		cancel()
	}()

	err := generatecmd.Run(ctx, stdout, stderr, args)
	if err != nil {
		_, _ = color.New(color.FgRed).Fprint(stderr, "(✗) ")
		_, _ = fmt.Fprintln(stderr, "Command failed: "+err.Error())
		exitCode := 1
		if e, ok := err.(ErrorCode); ok {
			exitCode = e.Code()
		}
		return exitCode
	}
	return 0
}

type ErrorCode interface {
	error
	Code() int
}

const fmtUsageText = `usage: ghtmx fmt [<args> ...]

Format all files in directory:

  ghtmx fmt .

Format stdin to stdout:

  ghtmx fmt < header.ghtmx

Format file or directory to stdout:

  ghtmx fmt -stdout FILE

Args:
  -stdout
    Prints to stdout instead of in-place format
  -stdin-filepath
    Provides the formatter with filepath context when using -stdout.
    Required for organising imports.
  -v
    Set log verbosity level to "debug". (default "info")
  -log-level
    Set log verbosity level. (default "info", options: "debug", "info", "warn", "error")
  -w
    Number of workers to use when formatting code. (default runtime.NumCPUs).
  -fail
    Fails with exit code 1 if files are changed. (e.g. in CI)
  -help
    Print help and exit.
`

func fmtCmd(stdin io.Reader, stdout, stderr io.Writer, args []string) (code int) {
	cmd := flag.NewFlagSet("fmt", flag.ContinueOnError)
	// The usage text below is the one users should see; the flag
	// package\'s own output would print alongside it.
	cmd.SetOutput(io.Discard)
	helpFlag := cmd.Bool("help", false, "")
	workerCountFlag := cmd.Int("w", runtime.NumCPU(), "")
	verboseFlag := cmd.Bool("v", false, "")
	logLevelFlag := cmd.String("log-level", "info", "")
	failIfChanged := cmd.Bool("fail", false, "")
	stdoutFlag := cmd.Bool("stdout", false, "")
	stdinFilepath := cmd.String("stdin-filepath", "", "")
	err := cmd.Parse(args)
	if err != nil {
		_, _ = fmt.Fprint(stderr, fmtUsageText)
		return 64 // EX_USAGE
	}
	if *helpFlag {
		_, _ = fmt.Fprint(stdout, fmtUsageText)
		return
	}

	log := sloghandler.NewLogger(*logLevelFlag, *verboseFlag, stderr)

	err = fmtcmd.Run(log, stdin, stdout, fmtcmd.Arguments{
		ToStdout:      *stdoutFlag,
		Files:         cmd.Args(),
		WorkerCount:   *workerCountFlag,
		StdinFilepath: *stdinFilepath,
		FailIfChanged: *failIfChanged,
	})
	if err != nil {
		return 1
	}
	return 0
}

const routesUsageText = `usage: ghtmx routes [<args> ...]

Prints the route table discovered from the application's Go source,
including escape-hatch //ghtmx:route declarations.

Args:
  -json
    Output the route table as JSON to stdout. (default false)
  -dir
    The module root to analyze. (default the current directory)
  -v
    Set log verbosity level to "debug". (default "info")
  -log-level
    Set log verbosity level. (default "info", options: "debug", "info", "warn", "error")
  -help
    Print help and exit.
`

func routesCmd(stdout, stderr io.Writer, args []string) (code int) {
	cmd := flag.NewFlagSet("routes", flag.ContinueOnError)
	// The usage text below is the one users should see; the flag
	// package\'s own output would print alongside it.
	cmd.SetOutput(io.Discard)
	jsonFlag := cmd.Bool("json", false, "")
	dirFlag := cmd.String("dir", "", "")
	verboseFlag := cmd.Bool("v", false, "")
	logLevelFlag := cmd.String("log-level", "info", "")
	helpFlag := cmd.Bool("help", false, "")
	err := cmd.Parse(args)
	if err != nil {
		_, _ = fmt.Fprint(stderr, routesUsageText)
		return 64 // EX_USAGE
	}
	if *helpFlag {
		_, _ = fmt.Fprint(stdout, routesUsageText)
		return
	}

	log := sloghandler.NewLogger(*logLevelFlag, *verboseFlag, stderr)

	err = routescmd.Run(log, stdout, routescmd.Arguments{
		JSON: *jsonFlag,
		Dir:  *dirFlag,
	})
	if err != nil {
		_, _ = color.New(color.FgRed).Fprint(stderr, "(✗) ")
		_, _ = fmt.Fprintln(stderr, "Command failed: "+err.Error())
		return 1
	}
	return 0
}

const lspUsageText = `usage: ghtmx lsp [<args> ...]

Starts a language server for ghtmx.

Args:
  -log string
    The file to log ghtmx LSP output to, or leave empty to disable logging.
  -goplsLog string
    The file to log gopls output, or leave empty to disable logging.
  -goplsRPCTrace
    Set gopls to log input and output messages.
  -gopls-remote
    Specify remote gopls instance to connect to.
  -help
    Print help and exit.
  -pprof
    Enable pprof web server (default address is localhost:9999)
  -http string
    Enable http debug server by setting a listen address (e.g. localhost:7474)
  -no-preload
    Disable preloading of ghtmx files on server startup and use custom GOPACKAGESDRIVER for lazy loading (useful for large monorepos). GOPACKAGESDRIVER environment variable must be set.
`

func lspCmd(stdin io.Reader, stdout, stderr io.Writer, args []string) (code int) {
	cmd := flag.NewFlagSet("lsp", flag.ContinueOnError)
	// The usage text below is the one users should see; the flag
	// package\'s own output would print alongside it.
	cmd.SetOutput(io.Discard)
	logFlag := cmd.String("log", "", "")
	goplsLog := cmd.String("goplsLog", "", "")
	goplsRPCTrace := cmd.Bool("goplsRPCTrace", false, "")
	goplsRemote := cmd.String("gopls-remote", "", "")
	helpFlag := cmd.Bool("help", false, "")
	pprofFlag := cmd.Bool("pprof", false, "")
	httpDebugFlag := cmd.String("http", "", "")
	noPreloadFlag := cmd.Bool("no-preload", false, "")
	err := cmd.Parse(args)
	if err != nil {
		_, _ = fmt.Fprint(stderr, lspUsageText)
		return 64 // EX_USAGE
	}
	if *helpFlag {
		_, _ = fmt.Fprint(stdout, lspUsageText)
		return
	}

	err = lspcmd.Run(stdin, stdout, stderr, lspcmd.Arguments{
		Log:           *logFlag,
		GoplsLog:      *goplsLog,
		GoplsRPCTrace: *goplsRPCTrace,
		GoplsRemote:   *goplsRemote,
		PPROF:         *pprofFlag,
		HTTPDebug:     *httpDebugFlag,
		NoPreload:     *noPreloadFlag && os.Getenv("GOPACKAGESDRIVER") != "",
		FormatConfig:  format.Config{},
	})
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err.Error())
		return 1
	}
	return 0
}
