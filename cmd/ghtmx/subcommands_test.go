package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The subcommand functions wire flags to the packages that do the work,
// and that wiring is only exercised by actually running them. A flag
// parsed into the wrong field is invisible to unit tests of either side.

const subcommandTemplate = `package app

templ Page(title string) {
	<div>{ title }</div>
}
`

// project scaffolds a module with one template and returns its path.
//
// invokeIn uses t.Chdir, which is process-global: no test in this
// package may call t.Parallel.
func project(t *testing.T) string {
	t.Helper()
	// Keep the build cache inside the sandbox rather than writing into
	// the developer's real ~/.cache/ghtmx.
	t.Setenv("GHTMX_CACHE_DIR", t.TempDir())
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod":     "module example.com/app\n\ngo 1.25\n",
		"page.ghtmx": subcommandTemplate,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// invokeIn runs the CLI with the working directory set to dir.
func invokeIn(t *testing.T, dir string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	t.Chdir(dir)
	var out, errOut bytes.Buffer
	code = run(strings.NewReader(""), &out, &errOut, append([]string{"ghtmx"}, args...))
	return code, out.String(), errOut.String()
}

func TestInfoCmdJSON(t *testing.T) {
	code, stdout, stderr := invokeIn(t, t.TempDir(), "info", "-json")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr: %s", code, stderr)
	}
	var info map[string]any
	if err := json.Unmarshal([]byte(stdout), &info); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, stdout)
	}
	// The point of `ghtmx info` is naming the toolchain pieces, so all
	// four have to be present even when one of them is missing.
	for _, key := range []string{"os", "go", "gopls", "ghtmx"} {
		if _, ok := info[key]; !ok {
			t.Errorf("the JSON output has no %q section: %s", key, stdout)
		}
	}
}

func TestInfoCmdText(t *testing.T) {
	code, _, stderr := invokeIn(t, t.TempDir(), "info")

	if code != 0 {
		t.Errorf("exit code = %d, want 0", code)
	}
	// The human form logs to stderr.
	if !strings.Contains(stderr, "goos") {
		t.Errorf("the text output does not report the platform:\n%s", stderr)
	}
}

// TestInfoCmdVerboseAndLogLevel covers the flag plumbing into the
// logger: -v has to win over -log-level, which is the whole reason it
// exists as a separate flag.
func TestInfoCmdVerboseAndLogLevel(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantDebug bool
	}{
		{"verbose", []string{"info", "-v"}, true},
		{"log level error", []string{"info", "-log-level", "error"}, false},
		{"verbose overrides log level", []string{"info", "-v", "-log-level", "error"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, stderr := invokeIn(t, t.TempDir(), tt.args...)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0\nstderr: %s", code, stderr)
			}
			// -v raises the level, so the platform record is emitted;
			// at error level alone nothing routine reaches stderr.
			if got := strings.Contains(stderr, "goarch"); got != tt.wantDebug {
				t.Errorf("platform detail present = %v, want %v:\n%s", got, tt.wantDebug, stderr)
			}
		})
	}
}

func TestRoutesCmd(t *testing.T) {
	dir := project(t)

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"text", []string{"routes"}, "VERB"},
		{"json", []string{"routes", "-json"}, "["},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := invokeIn(t, dir, tt.args...)
			if code != 0 {
				t.Fatalf("exit code = %d, want 0\nstderr: %s", code, stderr)
			}
			if !strings.Contains(stdout, tt.want) {
				t.Errorf("stdout %q does not contain %q", stdout, tt.want)
			}
		})
	}
}

func TestGenerateCmd(t *testing.T) {
	dir := project(t)

	code, _, stderr := invokeIn(t, dir, "generate", "-include-version=false")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr: %s", code, stderr)
	}
	if _, err := os.Stat(filepath.Join(dir, "page_ghtmx.go")); err != nil {
		t.Errorf("generate produced no output: %v", err)
	}
}

// TestGenerateCmdExitCodes pins the ErrorCode path, which is how a
// script tells a usage mistake from a generation failure. Only
// *ArgumentError implements Code(), so a bad flag exits 64 while a
// broken template takes the default 1 — asserting merely "non-zero"
// would pass with the ErrorCode branch deleted outright.
func TestGenerateCmdExitCodes(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string)
		args     []string
		wantCode int
	}{
		{
			name:     "bad flag is a usage error",
			args:     []string{"generate", "-no-such-flag"},
			wantCode: 64,
		},
		{
			name: "unparseable template is a generation failure",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "page.ghtmx"),
					[]byte("package app\n\ntempl X() {\n\t<div>\n}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			args:     []string{"generate", "-include-version=false"},
			wantCode: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := project(t)
			if tt.setup != nil {
				tt.setup(t, dir)
			}

			code, _, stderr := invokeIn(t, dir, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstderr: %s", code, tt.wantCode, stderr)
			}
		})
	}
}

func TestFmtCmd(t *testing.T) {
	dir := project(t)
	templ := filepath.Join(dir, "page.ghtmx")

	// Already formatted: -fail must not trip.
	if code, _, stderr := invokeIn(t, dir, "fmt", "-fail", templ); code != 0 {
		t.Errorf("fmt -fail on formatted source exited %d\nstderr: %s", code, stderr)
	}

	// Badly formatted: -fail has to notice, which is what CI relies on.
	unformatted := "package app\n\ntempl Page(title string) {\n<div>{ title }</div>\n}\n"
	if err := os.WriteFile(templ, []byte(unformatted), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := invokeIn(t, dir, "fmt", "-fail", templ); code == 0 {
		t.Error("fmt -fail exited 0 on unformatted source")
	}
}

// TestFmtCmdStdin covers the editor path: format-on-save pipes the
// buffer in and reads the result back rather than touching the file.
func TestFmtCmdStdin(t *testing.T) {
	dir := project(t)
	t.Chdir(dir)

	var out, errOut bytes.Buffer
	code := run(
		strings.NewReader("package app\n\ntempl Page() {\n<div></div>\n}\n"),
		&out, &errOut,
		[]string{"ghtmx", "fmt", "-stdin-filepath", filepath.Join(dir, "page.ghtmx")},
	)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstderr: %s", code, errOut.String())
	}
	// The input body is deliberately under-indented, so asserting on the
	// reindentation proves formatting happened rather than stdin being
	// echoed back unchanged.
	if !strings.Contains(out.String(), "\n\t<div></div>\n") {
		t.Errorf("stdout is not the reindented source:\n%s", out.String())
	}
}
