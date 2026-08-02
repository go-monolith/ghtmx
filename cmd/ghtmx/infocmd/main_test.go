package infocmd

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx"
)

// `ghtmx info` is the command a user runs when something is wrong with
// their toolchain, so the thing worth pinning is that it reports each
// tool's state honestly: an error level and an explanation when a tool
// is missing, broken, or a different version than the one running.
// Reporting a missing gopls at info level would send someone looking in
// the wrong place.

// fakeTool writes an executable script that prints output and exits with
// code. Building a fake PATH is the only way to drive the not-found,
// broken, and version-mismatch branches without depending on whatever
// happens to be installed on the machine running the tests.
func fakeTool(t *testing.T, dir, name, output string, code int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake-PATH technique needs a POSIX shell")
	}
	script := "#!/bin/sh\nprintf '%s\\n' " + shellQuote(output) + "\nexit " + strconv.Itoa(code) + "\n"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// emptyPath points PATH at a directory with nothing in it, so every
// lookup fails.
func emptyPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	return dir
}

func TestFindGhtmxReportsAHelpfulMessageWhenMissing(t *testing.T) {
	emptyPath(t)

	_, err := findGhtmx()
	if err == nil {
		t.Fatal("findGhtmx succeeded with an empty PATH")
	}
	// The message has to carry the install command: "not found" alone
	// leaves the user to guess how to fix it.
	if !strings.Contains(err.Error(), "go install") {
		t.Errorf("error %q does not tell the user how to install ghtmx", err)
	}
}

func TestFindGhtmxFindsItOnThePath(t *testing.T) {
	dir := emptyPath(t)
	name := "ghtmx"
	if runtime.GOOS == "windows" {
		name = "ghtmx.exe"
	}
	fakeTool(t, dir, name, "v1.2.3", 0)

	got, err := findGhtmx()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, name); got != want {
		t.Errorf("findGhtmx() = %q, want %q", got, want)
	}
}

func TestGetGhtmxInfo(t *testing.T) {
	tests := []struct {
		name        string
		install     bool
		output      string
		exitCode    int
		wantLevel   slog.Level
		wantMessage string // substring
	}{
		{
			name:        "not installed",
			wantLevel:   slog.LevelError,
			wantMessage: "go install",
		},
		{
			name:        "version command fails",
			install:     true,
			exitCode:    1,
			wantLevel:   slog.LevelError,
			wantMessage: "failed to get ghtmx version",
		},
		{
			// The mismatch case is the reason this command exists: a
			// stale binary on PATH silently generating with a different
			// compiler than the one the user thinks they are running.
			name:        "version mismatch",
			install:     true,
			output:      "v0.0.0-not-the-running-version",
			wantLevel:   slog.LevelError,
			wantMessage: "version mismatch",
		},
		{
			name:      "matching version",
			install:   true,
			output:    ghtmx.Version(),
			wantLevel: slog.LevelInfo,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := emptyPath(t)
			if tt.install {
				fakeTool(t, dir, "ghtmx", tt.output, tt.exitCode)
			}

			got := getGhtmxInfo()

			if got.Level != tt.wantLevel {
				t.Errorf("Level = %v, want %v (message: %q)", got.Level, tt.wantLevel, got.Message)
			}
			if tt.wantMessage == "" {
				if got.Message != "" {
					t.Errorf("Message = %q, want empty", got.Message)
				}
			} else if !strings.Contains(got.Message, tt.wantMessage) {
				t.Errorf("Message = %q, want it to contain %q", got.Message, tt.wantMessage)
			}
		})
	}
}

func TestGetGoInfo(t *testing.T) {
	tests := []struct {
		name        string
		install     bool
		exitCode    int
		wantLevel   slog.Level
		wantMessage string
	}{
		{name: "not installed", wantLevel: slog.LevelError, wantMessage: "failed to find go"},
		{name: "version command fails", install: true, exitCode: 1, wantLevel: slog.LevelError, wantMessage: "check that Go is installed"},
		{name: "installed", install: true, wantLevel: slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := emptyPath(t)
			if tt.install {
				fakeTool(t, dir, "go", "go version go1.26.0 linux/amd64", tt.exitCode)
			}

			got := getGoInfo()

			if got.Level != tt.wantLevel {
				t.Errorf("Level = %v, want %v (message: %q)", got.Level, tt.wantLevel, got.Message)
			}
			if tt.wantMessage != "" && !strings.Contains(got.Message, tt.wantMessage) {
				t.Errorf("Message = %q, want it to contain %q", got.Message, tt.wantMessage)
			}
			if tt.wantLevel == slog.LevelInfo && !strings.Contains(got.Version, "go version") {
				t.Errorf("Version = %q, want the `go version` output", got.Version)
			}
		})
	}
}

func TestGetGoplsInfo(t *testing.T) {
	tests := []struct {
		name        string
		install     bool
		output      string
		exitCode    int
		wantLevel   slog.Level
		wantMessage string
	}{
		{name: "not installed", wantLevel: slog.LevelError, wantMessage: "failed to find gopls"},
		{name: "version command fails", install: true, exitCode: 1, wantLevel: slog.LevelError, wantMessage: "failed to get gopls version"},
		{name: "installed", install: true, output: "golang.org/x/tools/gopls v0.23.0", wantLevel: slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := emptyPath(t)
			// pls.FindGopls also probes $HOME/go/bin, so HOME is
			// redirected too or a developer's real gopls leaks in.
			t.Setenv("HOME", t.TempDir())
			if tt.install {
				fakeTool(t, dir, "gopls", tt.output, tt.exitCode)
			}

			got := getGoplsInfo()

			if got.Level != tt.wantLevel {
				t.Errorf("Level = %v, want %v (message: %q)", got.Level, tt.wantLevel, got.Message)
			}
			if tt.wantMessage != "" && !strings.Contains(got.Message, tt.wantMessage) {
				t.Errorf("Message = %q, want it to contain %q", got.Message, tt.wantMessage)
			}
		})
	}
}

func TestGetInfoReportsTheHostPlatform(t *testing.T) {
	got := getInfo()
	if got.OS.GOOS != runtime.GOOS {
		t.Errorf("GOOS = %q, want %q", got.OS.GOOS, runtime.GOOS)
	}
	if got.OS.GOARCH != runtime.GOARCH {
		t.Errorf("GOARCH = %q, want %q", got.OS.GOARCH, runtime.GOARCH)
	}
}

func TestRunJSON(t *testing.T) {
	dir := emptyPath(t)
	t.Setenv("HOME", t.TempDir())
	fakeTool(t, dir, "go", "go version go1.26.0 linux/amd64", 0)

	var stdout bytes.Buffer
	if err := Run(context.Background(), discardLog(), &stdout, Arguments{JSON: true}); err != nil {
		t.Fatal(err)
	}

	var got Info
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, stdout.String())
	}
	if got.OS.GOOS != runtime.GOOS {
		t.Errorf("goos = %q, want %q", got.OS.GOOS, runtime.GOOS)
	}
	// gopls is absent from the fake PATH, so its state must say so
	// rather than being reported as fine.
	if got.Gopls.Level != slog.LevelError {
		t.Errorf("gopls level = %v with gopls absent, want error", got.Gopls.Level)
	}
}

func TestRunTextLogsEveryTool(t *testing.T) {
	dir := emptyPath(t)
	t.Setenv("HOME", t.TempDir())
	fakeTool(t, dir, "go", "go version go1.26.0 linux/amd64", 0)

	var logged bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	if err := Run(context.Background(), log, &bytes.Buffer{}, Arguments{}); err != nil {
		t.Fatal(err)
	}

	out := logged.String()
	for _, want := range []string{"os", "goos", "go", "gopls", "ghtmx"} {
		if !strings.Contains(out, want) {
			t.Errorf("log output is missing %q:\n%s", want, out)
		}
	}
	// The missing tools must be reported at error level, not buried.
	if !strings.Contains(out, "ERROR") {
		t.Errorf("no error level entry for the absent tools:\n%s", out)
	}
}

func TestLogInfoIncludesTheMessageOnlyWhenPresent(t *testing.T) {
	tests := []struct {
		name    string
		info    ToolInfo
		wantMsg bool
	}{
		{
			name:    "with message",
			info:    ToolInfo{Location: "/x", Version: "v1", Level: slog.LevelError, Message: "broken"},
			wantMsg: true,
		},
		{
			name: "without message",
			info: ToolInfo{Location: "/x", Version: "v1", Level: slog.LevelInfo},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logged bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

			logInfo(context.Background(), log, "tool", tt.info)

			out := logged.String()
			if got := strings.Contains(out, "message="); got != tt.wantMsg {
				t.Errorf("message attribute present = %v, want %v:\n%s", got, tt.wantMsg, out)
			}
			for _, want := range []string{"location=/x", "version=v1"} {
				if !strings.Contains(out, want) {
					t.Errorf("log output is missing %q:\n%s", want, out)
				}
			}
		})
	}
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(new(bytes.Buffer), nil))
}
