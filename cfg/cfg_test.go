package cfg

import (
	"os"
	"testing"
)

// This is the GHTMX_EXPERIMENT feature-flag reader. It has no flags
// defined yet, so what matters is that reading it is safe under every
// shape of the environment variable — a panic here happens before main
// gets a chance to run, and would look like a broken binary.
func TestParseToleratesAnyEnvironment(t *testing.T) {
	tests := []struct {
		name  string
		value string
		set   bool
	}{
		{name: "unset"},
		{name: "empty", value: "", set: true},
		{name: "one flag", value: "somefeature", set: true},
		{name: "several flags", value: "a,b,c", set: true},
		{name: "mixed case", value: "SomeFeature", set: true},
		{name: "empty entries", value: ",,a,,", set: true},
		{name: "whitespace", value: " a , b ", set: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.set {
				t.Setenv("GHTMX_EXPERIMENT", tt.value)
			} else {
				os.Unsetenv("GHTMX_EXPERIMENT")
			}

			got := parse()
			if got == nil {
				t.Fatal("parse returned nil; Experiment would be a nil pointer at init")
			}
		})
	}
}

// TestExperimentIsInitialised pins the package-level value every caller
// reads without a nil check.
func TestExperimentIsInitialised(t *testing.T) {
	if Experiment == nil {
		t.Error("Experiment is nil")
	}
}
