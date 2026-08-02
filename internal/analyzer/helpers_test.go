package analyzer

import (
	"go/parser"
	"testing"
)

// countParams decides whether a fragment call has the right number of
// arguments, and isEscapingSelector decides whether a URL was escaped
// before being bound. Both fail quietly when wrong: the first skips an
// arity check the user needed, the second warns about escaping that is
// already correct — and a warning on correct code is how people learn to
// ignore diagnostics.

func TestCountParams(t *testing.T) {
	tests := []struct {
		name         string
		signature    string
		wantCount    int
		wantVariadic bool
	}{
		{"no parameters", "()", 0, false},
		{"one", "(id string)", 1, false},
		{"two", "(id string, name string)", 2, false},
		// Grouped parameters count individually, or a fragment taking
		// (a, b string) would look like it takes one.
		{"grouped", "(a, b string)", 2, false},
		{"grouped and separate", "(a, b string, c int)", 3, false},
		{"variadic", "(parts ...string)", 1, true},
		{"variadic after fixed", "(id string, parts ...string)", 2, true},
		{"with a receiver-like prefix", "Row(id string)", 1, false},
		// -1 means "cannot tell", and the caller skips its checks rather
		// than reporting a wrong arity.
		{"no parentheses", "notASignature", -1, false},
		{"unparseable", "(((", -1, false},
		{"empty", "", -1, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			count, variadic := countParams(tt.signature)
			if count != tt.wantCount {
				t.Errorf("count = %d, want %d", count, tt.wantCount)
			}
			if variadic != tt.wantVariadic {
				t.Errorf("variadic = %v, want %v", variadic, tt.wantVariadic)
			}
		})
	}
}

func TestIsEscapingSelector(t *testing.T) {
	tests := []struct {
		name string
		pkg  string
		fn   string
		want bool
	}{
		{"ghtmx URL", "ghtmx", "URL", true},
		{"ghtmx SafeURL", "ghtmx", "SafeURL", true},
		{"ghtmx JoinURLErrs", "ghtmx", "JoinURLErrs", true},
		// The templ alias is accepted for projects migrating across.
		{"templ URL", "templ", "URL", true},
		// A same-named helper from another package escapes nothing this
		// analyzer knows about, so treating it as safe would suppress a
		// real warning.
		{"another package", "mypkg", "URL", false},
		{"unrelated function", "ghtmx", "Sprintf", false},
		{"empty", "", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isEscapingSelector(tt.pkg, tt.fn); got != tt.want {
				t.Errorf("isEscapingSelector(%q, %q) = %v, want %v", tt.pkg, tt.fn, got, tt.want)
			}
		})
	}
}

// TestReceiverBaseNameParsesRealExpressions guards countParams's
// dependency on go/parser: if the signature shape it builds ever stops
// parsing, every arity check silently turns off.
func TestCountParamsBuildsAParseableFuncType(t *testing.T) {
	// The same construction countParams performs internally.
	if _, err := parser.ParseExpr("func(id string, parts ...string)"); err != nil {
		t.Fatalf("the func-type shape countParams builds no longer parses: %v", err)
	}
}
