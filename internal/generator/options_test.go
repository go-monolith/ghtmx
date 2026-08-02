package generator

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-monolith/ghtmx/internal/parser"
)

// HasGoChanged and HasTextChanged are the build cache's decision
// function: they say whether a regenerated file needs recompiling or
// only its literals refreshing. A false negative serves stale code from
// the cache — the user edits a template, the browser reloads, and
// nothing changed.

func outputWith(literals []string, expressions []string, opts GeneratorOptions) GeneratorOutput {
	sm := parser.NewSourceMap()
	sm.Expressions = expressions
	return GeneratorOutput{Literals: literals, SourceMap: sm, Options: opts}
}

func TestHasGoChanged(t *testing.T) {
	base := outputWith([]string{"<div>"}, []string{"name"}, GeneratorOptions{FileName: "a.ghtmx"})

	tests := []struct {
		name    string
		updated GeneratorOutput
		want    bool
	}{
		{
			name:    "identical",
			updated: outputWith([]string{"<div>"}, []string{"name"}, GeneratorOptions{FileName: "a.ghtmx"}),
			want:    false,
		},
		{
			// The version is embedded in the output, so a toolchain
			// change has to invalidate the cache.
			name:    "version changed",
			updated: outputWith([]string{"<div>"}, []string{"name"}, GeneratorOptions{FileName: "a.ghtmx", Version: "v2"}),
			want:    true,
		},
		{
			// The filename appears in runtime error messages.
			name:    "filename changed",
			updated: outputWith([]string{"<div>"}, []string{"name"}, GeneratorOptions{FileName: "b.ghtmx"}),
			want:    true,
		},
		{
			name:    "skip-generated-comment changed",
			updated: outputWith([]string{"<div>"}, []string{"name"}, GeneratorOptions{FileName: "a.ghtmx", SkipCodeGeneratedComment: true}),
			want:    true,
		},
		{
			name:    "literal count changed",
			updated: outputWith([]string{"<div>", "<p>"}, []string{"name"}, GeneratorOptions{FileName: "a.ghtmx"}),
			want:    true,
		},
		{
			name:    "expression count changed",
			updated: outputWith([]string{"<div>"}, []string{"name", "other"}, GeneratorOptions{FileName: "a.ghtmx"}),
			want:    true,
		},
		{
			name:    "expression content changed",
			updated: outputWith([]string{"<div>"}, []string{"renamed"}, GeneratorOptions{FileName: "a.ghtmx"}),
			want:    true,
		},
		{
			// Documented exception: the generated date is deliberately
			// not part of the decision, or every build would recompile.
			name:    "only the generated date changed",
			updated: outputWith([]string{"<div>"}, []string{"name"}, GeneratorOptions{FileName: "a.ghtmx", GeneratedDate: "2026-01-01T00:00:00Z"}),
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasGoChanged(base, tt.updated); got != tt.want {
				t.Errorf("HasGoChanged = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHasTextChanged(t *testing.T) {
	base := outputWith([]string{"hello", "world"}, nil, GeneratorOptions{})

	tests := []struct {
		name    string
		updated GeneratorOutput
		want    bool
	}{
		{"identical", outputWith([]string{"hello", "world"}, nil, GeneratorOptions{}), false},
		{"content changed", outputWith([]string{"hello", "there"}, nil, GeneratorOptions{}), true},
		{"one fewer", outputWith([]string{"hello"}, nil, GeneratorOptions{}), true},
		{"one more", outputWith([]string{"hello", "world", "!"}, nil, GeneratorOptions{}), true},
		{"emptied", outputWith(nil, nil, GeneratorOptions{}), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasTextChanged(base, tt.updated); got != tt.want {
				t.Errorf("HasTextChanged = %v, want %v", got, tt.want)
			}
		})
	}
}

// generateSource runs the generator over inline source and returns the
// emitted Go.
func generateSource(t *testing.T, src string, opts ...GenerateOpt) string {
	t.Helper()
	parsed, err := parser.ParseString(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var buf bytes.Buffer
	if _, err := Generate(parsed, &buf, opts...); err != nil {
		t.Fatalf("generate: %v", err)
	}
	return buf.String()
}

const minimalTemplate = "package p\n\ntempl x() {\n\t<div>hi</div>\n}\n"

// filenameTemplate carries a Go expression, which is what makes the
// generator emit a runtime error site — the only place the filename
// appears in the output.
const filenameTemplate = "package p\n\ntempl x(name string) {\n\t<div>{ name }</div>\n}\n"

func TestGenerateOptions(t *testing.T) {
	t.Run("version is emitted", func(t *testing.T) {
		got := generateSource(t, minimalTemplate, WithVersion("v9.9.9"))
		if !strings.Contains(got, "v9.9.9") {
			t.Errorf("the version does not appear in the output:\n%s", firstLines(got, 6))
		}
	})

	t.Run("timestamp is emitted", func(t *testing.T) {
		stamp := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
		got := generateSource(t, minimalTemplate, WithTimestamp(stamp))
		if !strings.Contains(got, "2026-08-02") {
			t.Errorf("the timestamp does not appear in the output:\n%s", firstLines(got, 6))
		}
	})

	t.Run("skipping the generated comment", func(t *testing.T) {
		with := generateSource(t, minimalTemplate)
		without := generateSource(t, minimalTemplate, WithSkipCodeGeneratedComment())

		// gopls disables edit features for files carrying this marker,
		// which is exactly why the LSP asks for it to be left out.
		if !strings.Contains(with, "Code generated by") {
			t.Errorf("the default output has no code-generated marker:\n%s", firstLines(with, 3))
		}
		if strings.Contains(without, "Code generated by") {
			t.Errorf("WithSkipCodeGeneratedComment still emitted the marker:\n%s", firstLines(without, 3))
		}
	})

	t.Run("an absolute filename is reduced to its base", func(t *testing.T) {
		abs, err := filepath.Abs(filepath.Join("some", "dir", "page.ghtmx"))
		if err != nil {
			t.Fatal(err)
		}
		// Absolute paths would leak the generating machine's directory
		// layout into runtime error messages shipped to users.
		got := generateSource(t, filenameTemplate, WithFileName(abs))
		if strings.Contains(got, filepath.Dir(abs)) {
			t.Errorf("the absolute path leaked into the output:\n%s", firstLines(got, 40))
		}
		if !strings.Contains(got, "page.ghtmx") {
			t.Errorf("the base filename is missing from the output:\n%s", firstLines(got, 40))
		}
	})

	t.Run("a relative filename is kept whole", func(t *testing.T) {
		got := generateSource(t, filenameTemplate, WithFileName("internal/x/page.ghtmx"))
		if !strings.Contains(got, "internal/x/page.ghtmx") {
			t.Errorf("the relative path was not preserved:\n%s", firstLines(got, 40))
		}
	})
}

// TestGenerateCoversConstructsTheCorpusMisses reaches the emitters the
// committed corpus never exercises, so a break in them would otherwise
// show up only in someone's project.
func TestGenerateCoversConstructsTheCorpusMisses(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "call expression",
			src:  "package p\n\ntempl inner() {\n\t<b>i</b>\n}\n\ntempl outer() {\n\t{! inner() }\n}\n",
			want: "inner()",
		},
		{
			name: "fallthrough in a switch",
			src:  "package p\n\ntempl x(i int) {\n\tswitch i {\n\t\tcase 1:\n\t\t\tfallthrough\n\t\tcase 2:\n\t\t\t<p>low</p>\n\t}\n}\n",
			want: "fallthrough",
		},
		{
			name: "doctype",
			src:  "package p\n\ntempl x() {\n\t<!DOCTYPE html>\n\t<html></html>\n}\n",
			want: "doctype",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := generateSource(t, tt.src)
			if !strings.Contains(got, tt.want) {
				t.Errorf("generated output does not contain %q", tt.want)
			}
		})
	}
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
