package htmxsurface

import (
	"strings"
	"testing"
)

// The surface is what tells a user their hx-* attribute is misspelled,
// removed, or not yet available in the version they pinned. Getting a
// query wrong here means either a false warning on valid markup — which
// trains people to ignore the diagnostics — or silence on a typo that
// will not work at runtime.

func testSurface(t *testing.T) *Surface {
	t.Helper()
	s, err := ForVersion("2.0.10")
	if err != nil {
		t.Fatalf("ForVersion: %v", err)
	}
	return s
}

func TestIntroducedAndRemoved(t *testing.T) {
	s := testSurface(t)

	// A core attribute that has always existed reports no introduction
	// version, and has certainly not been removed.
	if _, ok := s.Removed("hx-get"); ok {
		t.Error("hx-get is reported as removed")
	}

	// Both queries have to answer cleanly for something that is not an
	// attribute at all, rather than reporting a version for it.
	if v, ok := s.Introduced("hx-not-a-real-attribute"); ok {
		t.Errorf("Introduced reported %q for an unknown attribute", v)
	}
	if v, ok := s.Removed("hx-not-a-real-attribute"); ok {
		t.Errorf("Removed reported %q for an unknown attribute", v)
	}
}

// TestHtmxEventNames pins the event list the LSP offers for hx-on:
// completions. An empty list means no suggestions at all; kebab-case is
// what the attribute syntax actually accepts.
func TestHtmxEventNames(t *testing.T) {
	names := testSurface(t).HtmxEventNames()

	if len(names) == 0 {
		t.Fatal("no htmx event names; hx-on: completion would offer nothing")
	}
	for _, name := range names {
		if strings.ToLower(name) != name {
			t.Errorf("event %q is not lower case; hx-on: does not accept it", name)
		}
		if strings.Contains(name, " ") {
			t.Errorf("event %q contains a space", name)
		}
		// Colons are legitimate here — htmx groups events as xhr:abort,
		// validation:failed and so on — but the name must not be empty
		// or the completion offers a blank entry.
		if name == "" {
			t.Error("the event list contains an empty name")
		}
	}

	// A well-known event has to be present, or the list is not what it
	// claims to be.
	var found bool
	for _, name := range names {
		if name == "after-request" || name == "before-request" {
			found = true
		}
	}
	if !found {
		t.Errorf("the event list has %d entries but none of the well-known request events: %v", len(names), names)
	}
}

func TestLikelyModifierTypo(t *testing.T) {
	modifiers := []string{"changed", "delay", "throttle", "once"}

	tests := []struct {
		name string
		word string
		want bool
	}{
		{"exact", "changed", true},
		{"one character off", "chagned", true},
		{"two characters off", "chaned", true},
		// Beyond two edits it is a different word, and suggesting a
		// modifier the user did not mean is worse than staying quiet.
		{"unrelated", "completelydifferent", false},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := likelyModifierTypo(tt.word, modifiers); got != tt.want {
				t.Errorf("likelyModifierTypo(%q) = %v, want %v", tt.word, got, tt.want)
			}
		})
	}
}

// TestForVersionRejectsAnUnknownVersion pins the error path: a pinned
// version the surface data does not carry has to be reported, since
// silently validating against a different version would produce
// warnings that make no sense for the user's htmx build.
func TestForVersionRejectsAnUnknownVersion(t *testing.T) {
	if _, err := ForVersion("0.0.1-not-a-real-htmx"); err == nil {
		t.Error("ForVersion accepted a version the surface data does not carry")
	}
}
