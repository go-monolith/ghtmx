package main

import (
	"bytes"
	"strings"
	"testing"
)

// run is the whole CLI with its I/O injected, so the dispatch table and
// every subcommand's argument handling are testable in-process. The
// cases that matter are the ones a user hits by accident: a bad flag, a
// missing subcommand, -help. Each must print usage and exit 64 (EX_USAGE)
// rather than exiting 2 with the flag package's own message, which is
// what happened before the flag sets moved to ContinueOnError.

// invoke runs the CLI and returns its exit code and streams.
func invoke(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(strings.NewReader(""), &out, &errOut, append([]string{"ghtmx"}, args...))
	return code, out.String(), errOut.String()
}

func TestRunDispatch(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantCode int
		wantOut  string // substring expected on stdout
		wantErr  string // substring expected on stderr
	}{
		{
			name:     "no subcommand prints usage",
			args:     nil,
			wantCode: 64,
			wantErr:  "usage: ghtmx",
		},
		{
			name:     "unknown subcommand prints usage",
			args:     []string{"nonsense"},
			wantCode: 64,
			wantErr:  "usage: ghtmx",
		},
		{
			name:     "version",
			args:     []string{"version"},
			wantCode: 0,
			wantOut:  "v",
		},
		{
			name:     "--version",
			args:     []string{"--version"},
			wantCode: 0,
			wantOut:  "v",
		},
		{name: "help", args: []string{"help"}, wantCode: 0, wantOut: "usage: ghtmx"},
		{name: "-help", args: []string{"-help"}, wantCode: 0, wantOut: "usage: ghtmx"},
		{name: "--help", args: []string{"--help"}, wantCode: 0, wantOut: "usage: ghtmx"},
		{name: "-h", args: []string{"-h"}, wantCode: 0, wantOut: "usage: ghtmx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := invoke(tt.args...)

			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d\nstdout: %s\nstderr: %s", code, tt.wantCode, stdout, stderr)
			}
			if tt.wantOut != "" && !strings.Contains(stdout, tt.wantOut) {
				t.Errorf("stdout %q does not contain %q", stdout, tt.wantOut)
			}
			if tt.wantErr != "" && !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("stderr %q does not contain %q", stderr, tt.wantErr)
			}
		})
	}
}

// TestSubcommandHelp pins that every subcommand prints its own usage to
// stdout and exits 0. -help is not an error, so it must not go to stderr
// or return a failure code — a user piping the output would otherwise
// get nothing.
func TestSubcommandHelp(t *testing.T) {
	// -h is not a registered flag: the flag package handles it itself and
	// reports ErrHelp, a different path from -help. Both have to land on
	// stdout with exit 0, or `ghtmx fmt -h | less` shows nothing and any
	// wrapper treating non-zero as failure breaks.
	for _, flagName := range []string{"-help", "-h"} {
		for _, sub := range []string{"info", "generate", "fmt", "routes", "lsp"} {
			t.Run(sub+flagName, func(t *testing.T) {
				code, stdout, stderr := invoke(sub, flagName)

				if code != 0 {
					t.Errorf("exit code = %d, want 0 (stderr: %s)", code, stderr)
				}
				if !strings.Contains(stdout, "usage: ghtmx "+sub) {
					t.Errorf("stdout does not carry %s's usage text:\n%s", sub, stdout)
				}
			})
		}
	}
}

// TestSubcommandRejectsAnUnknownFlag is the branch the ContinueOnError
// change made reachable. With ExitOnError the flag package called
// os.Exit(2) before any of this ran, so the CLI could never print its
// own usage or return EX_USAGE for a typo'd flag.
// All five share the convention now: hand-written usage, the offending
// flag named, no auto-generated flag listing, exit 64. `generate` gets
// there by a different route — it parses its own arguments and reports
// 64 through ArgumentError.Code — which is why it is included here
// rather than carved out.
func TestSubcommandRejectsAnUnknownFlag(t *testing.T) {
	for _, sub := range []string{"info", "generate", "fmt", "routes", "lsp"} {
		t.Run(sub, func(t *testing.T) {
			code, stdout, stderr := invoke(sub, "-no-such-flag")

			if code != 64 {
				t.Errorf("exit code = %d, want 64 (EX_USAGE)\nstdout: %s\nstderr: %s", code, stdout, stderr)
			}
			if !strings.Contains(stderr, "usage: ghtmx "+sub) {
				t.Errorf("stderr does not carry %s's usage text:\n%s", sub, stderr)
			}
			// The flag package's message names the offending flag, and
			// it is the only thing that says *what* was wrong; the usage
			// block alone leaves the user comparing by eye.
			if !strings.Contains(stderr, "no-such-flag") {
				t.Errorf("stderr does not name the offending flag:\n%s", stderr)
			}
		})
	}
}
