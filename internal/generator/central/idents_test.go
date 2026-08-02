package central

import (
	"testing"

	"github.com/go-monolith/ghtmx/internal/routes"
)

// These build the identifiers the generated central package exposes. A
// name that comes out unexported, or that collides with another, breaks
// the user's build with an error pointing at generated code they did not
// write — so the shapes that produce awkward names are worth pinning.

func TestVerbSuffix(t *testing.T) {
	tests := []struct {
		verb routes.Verb
		want string
	}{
		{"GET", "Get"},
		{"POST", "Post"},
		{"DELETE", "Delete"},
		{"PATCH", "Patch"},
		// A route with no verb matches every method, and its symbol has
		// to say so rather than being named after an empty string.
		{routes.AnyVerb, "Any"},
	}
	for _, tt := range tests {
		t.Run(string(tt.verb), func(t *testing.T) {
			if got := verbSuffix(tt.verb); got != tt.want {
				t.Errorf("verbSuffix(%q) = %q, want %q", tt.verb, got, tt.want)
			}
		})
	}
}

func TestExportIdent(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"handler", "Handler"},
		{"Handler", "Handler"},
		{"h", "H"},
		{"", ""},
		// A multi-byte first rune must not be sliced mid-character, or
		// the generated file is not valid UTF-8.
		{"ünicode", "Ünicode"},
		{"日本", "日本"},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := exportIdent(tt.in); got != tt.want {
				t.Errorf("exportIdent(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExportedPkgBase(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"simple", "example.com/handlers", "Handlers"},
		// Separators are what make a package directory name unusable as
		// an identifier, so each has to be folded into camel case.
		{"hyphenated", "example.com/my-handlers", "MyHandlers"},
		{"underscored", "example.com/my_handlers", "MyHandlers"},
		{"dotted", "example.com/my.handlers", "MyHandlers"},
		{"several separators", "example.com/a-b_c.d", "ABCD"},
		{"no path", "handlers", "Handlers"},
		{"trailing separator", "example.com/handlers-", "Handlers"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := exportedPkgBase(tt.in); got != tt.want {
				t.Errorf("exportedPkgBase(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeIdent(t *testing.T) {
	// Whatever comes out has to be usable as a Go identifier, since it
	// is written straight into the generated package.
	for _, in := range []string{
		"already_fine", "with-hyphens", "with.dots", "with spaces",
		"123leading", "", "ünicode",
	} {
		t.Run(in, func(t *testing.T) {
			got := sanitizeIdent(in)
			for _, r := range got {
				ok := r == '_' ||
					(r >= 'a' && r <= 'z') ||
					(r >= 'A' && r <= 'Z') ||
					(r >= '0' && r <= '9')
				if !ok {
					t.Errorf("sanitizeIdent(%q) = %q, which contains %q — not a legal identifier rune", in, got, r)
				}
			}
		})
	}
}
