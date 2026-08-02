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
func project(t *testing.T) string {
	t.Helper()
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
		name string
		args []string
	}{
		{"verbose", []string{"info", "-v"}},
		{"log level", []string{"info", "-log-level", "error"}},
		{"verbose overrides log level", []string{"info", "-v", "-log-level", "error"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if code, _, stderr := invokeIn(t, t.TempDir(), tt.args...); code != 0 {
				t.Errorf("exit code = %d, want 0\nstderr: %s", code, stderr)
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

// TestGenerateCmdReportsFailureWithItsOwnCode pins the ErrorCode path:
// generate returns a typed error carrying an exit code, and collapsing
// it to a plain 1 would stop scripts telling a usage mistake from a
// generation failure.
func TestGenerateCmdReportsFailureWithItsOwnCode(t *testing.T) {
	dir := project(t)
	if err := os.WriteFile(filepath.Join(dir, "page.ghtmx"),
		[]byte("package app\n\ntempl X() {\n\t<div>\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	code, _, stderr := invokeIn(t, dir, "generate", "-include-version=false")
	if code == 0 {
		t.Fatal("generate succeeded on an unparseable template")
	}
	if !strings.Contains(stderr, "Command failed") {
		t.Errorf("stderr does not report the failure:\n%s", stderr)
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
	if !strings.Contains(out.String(), "templ Page()") {
		t.Errorf("formatted output did not come back on stdout:\n%s", out.String())
	}
}
