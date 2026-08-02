package ghtmx

import (
	"errors"
	"strings"
	"testing"
)

// Error is what a user sees when a template fails at render time, and
// its FileName/Line/Col are the only pointer they get back to the .ghtmx
// they wrote. An unwrapped cause or a message missing the position turns
// a locatable failure into a guess.
func TestErrorMessage(t *testing.T) {
	cause := errors.New("boom")

	tests := []struct {
		name     string
		err      Error
		contains []string
	}{
		{
			name:     "with a filename",
			err:      Error{Err: cause, FileName: "page.ghtmx", Line: 12, Col: 4},
			contains: []string{"page.ghtmx", "12", "4", "boom"},
		},
		{
			// Without one there is still a name, so the message never
			// starts with a bare colon.
			name:     "without a filename",
			err:      Error{Err: cause, Line: 1, Col: 2},
			contains: []string{"templ", "1", "2", "boom"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.err.Error()
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("Error() = %q, missing %q", got, want)
				}
			}
		})
	}
}

func TestErrorUnwrap(t *testing.T) {
	cause := errors.New("boom")
	err := Error{Err: cause, FileName: "page.ghtmx"}

	// errors.Is has to reach the cause, or callers cannot classify a
	// render failure at all.
	if !errors.Is(err, cause) {
		t.Error("errors.Is could not reach the wrapped cause")
	}
	if got := err.Unwrap(); !errors.Is(got, cause) {
		t.Errorf("Unwrap() = %v, want %v", got, cause)
	}
}

func TestBool(t *testing.T) {
	// Bool exists so generated code can pass a bool through the same
	// attribute-value path as every other type.
	if !Bool(true) {
		t.Error("Bool(true) = false")
	}
	if Bool(false) {
		t.Error("Bool(false) = true")
	}
}

// versionLess orders the supported htmx versions, so getting it wrong
// picks the wrong script for a user's configured version.
func TestVersionLess(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.9.2", "1.9.10", true},  // numeric, not lexical
		{"1.9.10", "1.9.2", false}, //
		{"1.9.2", "1.9.2", false},  // equal
		{"1.9", "1.9.1", true},     // shorter is lower
		{"1.9.1", "1.9", false},    //
		{"2.0.0", "1.9.12", false}, // major wins
		{"1.9.12", "2.0.0", true},  //
		{"1.9.2", "1.10.0", true},  // minor is numeric too
		// A non-numeric segment falls back to string comparison, where
		// "0-beta" sorts after "0" — so a pre-release reads as *newer*
		// than its release. Recorded rather than asserted as desired:
		// the supported-versions list holds no pre-releases, so nothing
		// depends on it today.
		{"1.0.0-beta", "1.0.0", false},
		{"1.0.0", "1.0.0-beta", true},
	}
	for _, tt := range tests {
		t.Run(tt.a+" < "+tt.b, func(t *testing.T) {
			if got := versionLess(tt.a, tt.b); got != tt.want {
				t.Errorf("versionLess(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestSupportedHtmxVersionsAreOrdered pins the invariant versionLess
// exists to maintain.
func TestSupportedHtmxVersionsAreOrdered(t *testing.T) {
	versions := SupportedHtmxVersions()
	if len(versions) < 2 {
		t.Skip("fewer than two supported versions; nothing to order")
	}
	for i := 1; i < len(versions); i++ {
		if versionLess(versions[i], versions[i-1]) {
			t.Errorf("supported versions are out of order: %q comes after %q", versions[i], versions[i-1])
		}
	}
}
