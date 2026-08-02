package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ghtmx.json is hand-edited, so its error messages are the whole user
// interface for getting it right. A decode error that says
// "cannot unmarshal string into Go struct field .checks of type
// diag.Severity" tells someone nothing; the point of this layer is to
// say which key, which line, and what a valid value looks like.

// loadConfig writes a ghtmx.json and loads it.
func loadConfig(t *testing.T, contents string) (Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ghtmx.json"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(dir)
}

func TestConfigErrorMessages(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		wants    []string // substrings the message must contain
	}{
		{
			name:     "not an object",
			contents: `["a", "b"]`,
			wants:    []string{"must be a JSON object"},
		},
		{
			name:     "truncated",
			contents: `{"sourceDirs": [`,
			wants:    []string{"sourceDirs", "unexpected EOF"},
		},
		{
			name:     "unknown key",
			contents: `{"sourceDir": ["a"]}`,
			wants:    []string{"sourceDir"},
		},
		{
			name:     "duplicate key",
			contents: `{"htmxVersion": "2.0.10", "htmxVersion": "2.0.9"}`,
			wants:    []string{"htmxVersion"},
		},
		{
			// The type name a user needs is "a list of strings", not
			// "[]string".
			name:     "wrong type for a string list",
			contents: `{"sourceDirs": "not-a-list"}`,
			wants:    []string{"sourceDirs", "a list of strings"},
		},
		{
			name:     "wrong type for a string",
			contents: `{"htmxVersion": 210}`,
			wants:    []string{"htmxVersion", "a string"},
		},
		{
			name:     "wrong type for a bool",
			contents: `{"strictTargets": "yes"}`,
			wants:    []string{"strictTargets", "true or false"},
		},
		{
			name:     "wrong type for the checks map",
			contents: `{"checks": "off"}`,
			wants:    []string{"checks", "object"},
		},
		{
			name:     "wrong type for the generated package",
			contents: `{"generatedPackage": "ghtmxgen"}`,
			wants:    []string{"generatedPackage", "dir and name"},
		},
		{
			name:     "invalid severity value",
			contents: `{"checks": {"GHTMX-W0101": "loud"}}`,
			wants:    []string{"GHTMX-W0101", "error", "warning", "off"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := loadConfig(t, tt.contents)
			if err == nil {
				t.Fatalf("Load accepted %s, want an error", tt.contents)
			}
			msg := err.Error()
			for _, want := range tt.wants {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q does not mention %q — the user cannot tell what to fix", msg, want)
				}
			}
			// Every message has to point at the file, or someone with
			// several modules cannot tell which config is wrong.
			if !strings.Contains(msg, "ghtmx.json") {
				t.Errorf("message %q does not name the file", msg)
			}
		})
	}
}

// TestConfigErrorsCarryAPosition pins the line:column prefix. Without it
// a duplicate key in a hundred-line config is a hunt.
func TestConfigErrorsCarryAPosition(t *testing.T) {
	_, err := loadConfig(t, "{\n  \"htmxVersion\": \"2.0.10\",\n  \"htmxVersion\": \"2.0.9\"\n}\n")
	if err == nil {
		t.Fatal("Load accepted a duplicate key")
	}
	// The offending key is on line 3.
	if !strings.Contains(err.Error(), ":3:") {
		t.Errorf("message %q does not point at line 3", err)
	}
}

func TestValidConfigsLoad(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{"empty object", `{}`},
		{"source dirs", `{"sourceDirs": ["a", "b"]}`},
		{"htmx version", `{"htmxVersion": "2.0.10"}`},
		{"route scope", `{"routeScope": ["./..."]}`},
		{"generated package", `{"generatedPackage": {"dir": "gen", "name": "gen"}}`},
		{"generated suffix", `{"generatedSuffix": "_gen.go"}`},
		{"strict targets", `{"strictTargets": true}`},
		{"checks", `{"checks": {"GHTMX-W0101": "off"}}`},
		{"severity off", `{"checks": {"GHTMX-W0101": "off"}}`},
		{"severity warning", `{"checks": {"GHTMX-W0101": "warning"}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := loadConfig(t, tt.contents); err != nil {
				t.Errorf("Load rejected a valid config %s: %v", tt.contents, err)
			}
		})
	}
}

// TestLoadWithoutAConfigFile pins the default path: ghtmx.json is
// optional, and a project without one has to work.
func TestLoadWithoutAConfigFile(t *testing.T) {
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatalf("Load with no config file: %v", err)
	}
	// The defaults have to be usable rather than zero values.
	if cfg.GeneratedSuffix == "" {
		t.Error("GeneratedSuffix has no default; generation would write files with no extension")
	}
	if cfg.HtmxVersion == "" {
		t.Error("HtmxVersion has no default; attribute validation would be disabled")
	}
}

func TestFriendlyType(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"string", "a string"},
		{"bool", "true or false"},
		{"[]string", "a list of strings"},
		{"diag.Severity", `a severity: "error", "warning", or "off"`},
		{"map[string]diag.Severity", "an object mapping diagnostic IDs to severities"},
		{"config.GeneratedPackage", "an object with dir and name"},
		// Anything unrecognised falls through unchanged, which is worse
		// than a translation but better than nothing.
		{"chan int", "chan int"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := friendlyType(tt.in); got != tt.want {
				t.Errorf("friendlyType(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestUnknownFieldName(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		want   string
		wantOK bool
	}{
		{
			name:   "a real decoder message",
			err:    errString(`json: unknown field "sourceDir"`),
			want:   "sourceDir",
			wantOK: true,
		},
		{
			name: "an unrelated error",
			err:  errString("some other failure"),
		},
		{
			name: "a truncated marker",
			err:  errString(`json: unknown field "unterminated`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := unknownFieldName(tt.err)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Errorf("field = %q, want %q", got, tt.want)
			}
		})
	}
}

type errString string

func (e errString) Error() string { return string(e) }
