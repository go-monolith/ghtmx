package diag

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// NFR-009 says every diagnostic carries a registered ID, a real
// severity, and a position that resolves to a file. Validate is what
// enforces it, and a diagnostic that slips through with a missing
// position is one an editor cannot place and a user cannot navigate to.

func validDiagnostic() Diagnostic {
	return Diagnostic{
		ID:       DuplicateRoute,
		Severity: Error,
		Message:  "two registrations of the same verb and path",
		Pos:      Position{File: "main.go", Line: 12, Col: 3},
	}
}

func TestDiagnosticValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Diagnostic)
		wantErr string // substring
	}{
		{name: "valid", mutate: func(*Diagnostic) {}},
		{
			name:    "no ID",
			mutate:  func(d *Diagnostic) { d.ID = "" },
			wantErr: "no ID",
		},
		{
			// An unregistered ID means the catalogue and the code have
			// drifted, so the user gets a code they cannot look up.
			name:    "unregistered ID",
			mutate:  func(d *Diagnostic) { d.ID = "GHTMX-X9999" },
			wantErr: "not registered",
		},
		{
			name:    "no severity",
			mutate:  func(d *Diagnostic) { d.Severity = "" },
			wantErr: "invalid severity",
		},
		{
			// "off" is a configuration value, not something a reported
			// diagnostic can carry.
			name:    "severity off",
			mutate:  func(d *Diagnostic) { d.Severity = "off" },
			wantErr: "invalid severity",
		},
		{
			name:    "no file",
			mutate:  func(d *Diagnostic) { d.Pos.File = "" },
			wantErr: "no file position",
		},
		{
			name:    "no line",
			mutate:  func(d *Diagnostic) { d.Pos.Line = 0 },
			wantErr: "no line position",
		},
		{
			name:    "no column",
			mutate:  func(d *Diagnostic) { d.Pos.Col = 0 },
			wantErr: "col",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := validDiagnostic()
			tt.mutate(&d)

			err := d.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate on a well-formed diagnostic: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate accepted a diagnostic that %s", tt.name)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not mention %q", err, tt.wantErr)
			}
		})
	}
}

// TestWriteText pins the one-line-per-diagnostic form editors and CI
// logs parse.
func TestWriteText(t *testing.T) {
	var buf bytes.Buffer
	diags := []Diagnostic{validDiagnostic(), validDiagnostic()}

	if err := WriteText(&buf, diags); err != nil {
		t.Fatalf("WriteText: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != len(diags) {
		t.Fatalf("wrote %d lines for %d diagnostics", len(lines), len(diags))
	}
	for _, line := range lines {
		for _, want := range []string{"main.go", "12", "3", DuplicateRoute} {
			if !strings.Contains(line, want) {
				t.Errorf("line %q is missing %q", line, want)
			}
		}
	}
}

// failingWriter reports a closed pipe, which is how a diagnostic dump
// into a broken pager fails.
type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

func TestWriteTextReportsWriteFailures(t *testing.T) {
	sentinel := errors.New("pipe closed")

	err := WriteText(failingWriter{err: sentinel}, []Diagnostic{validDiagnostic()})
	if !errors.Is(err, sentinel) {
		t.Errorf("WriteText returned %v, want it to wrap %v", err, sentinel)
	}
}

func TestWriteJSON(t *testing.T) {
	t.Run("diagnostics", func(t *testing.T) {
		var buf bytes.Buffer
		if err := WriteJSON(&buf, []Diagnostic{validDiagnostic()}); err != nil {
			t.Fatalf("WriteJSON: %v", err)
		}
		var got []Diagnostic
		if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
			t.Fatalf("output is not valid JSON: %v\n%s", err, buf.String())
		}
		if len(got) != 1 || got[0].ID != DuplicateRoute {
			t.Errorf("decoded %+v, want the single diagnostic", got)
		}
	})

	t.Run("nil renders as an empty array", func(t *testing.T) {
		var buf bytes.Buffer
		if err := WriteJSON(&buf, nil); err != nil {
			t.Fatalf("WriteJSON: %v", err)
		}
		// A consumer ranging over the result should not have to
		// special-case null.
		if got := strings.TrimSpace(buf.String()); got != "[]" {
			t.Errorf("nil rendered as %q, want []", got)
		}
	})
}

func TestWriteJSONReportsWriteFailures(t *testing.T) {
	sentinel := errors.New("pipe closed")

	err := WriteJSON(failingWriter{err: sentinel}, []Diagnostic{validDiagnostic()})
	if !errors.Is(err, sentinel) {
		t.Errorf("WriteJSON returned %v, want it to wrap %v", err, sentinel)
	}
}

// TestEveryRegisteredDiagnosticValidates is the catalogue's own
// consistency check: an entry with a malformed severity would make every
// diagnostic it produces fail Validate at runtime.
func TestEveryRegisteredDiagnosticValidates(t *testing.T) {
	if len(Registry) == 0 {
		t.Fatal("the registry is empty")
	}
	for id, entry := range Registry {
		t.Run(id, func(t *testing.T) {
			if entry.ID != id {
				t.Errorf("entry is keyed %q but carries ID %q", id, entry.ID)
			}
			if entry.DefaultSeverity != Error && entry.DefaultSeverity != Warning {
				t.Errorf("severity %q is neither error nor warning", entry.DefaultSeverity)
			}
			if entry.Description == "" {
				t.Error("no description; the catalogue entry would be blank")
			}
		})
	}
}
