package diag

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func pos(line, col uint32) Position {
	return Position{File: "app/page.ghtmx", Line: line, Col: col}
}

func TestRegistryIDsAreWellFormed(t *testing.T) {
	for id, check := range Registry {
		if id != check.ID {
			t.Errorf("registry key %q does not match check ID %q", id, check.ID)
		}
		if !strings.HasPrefix(id, "GHTMX-E") && !strings.HasPrefix(id, "GHTMX-W") {
			t.Errorf("ID %q does not follow the GHTMX-Exxxx/GHTMX-Wxxxx scheme", id)
		}
		if strings.HasPrefix(id, "GHTMX-E") && check.DefaultSeverity != Error {
			t.Errorf("ID %q is in the error range but defaults to %q", id, check.DefaultSeverity)
		}
		if strings.HasPrefix(id, "GHTMX-W") && check.DefaultSeverity != Warning {
			t.Errorf("ID %q is in the warning range but defaults to %q", id, check.DefaultSeverity)
		}
		if check.Description == "" {
			t.Errorf("ID %q has no description", id)
		}
	}
}

func TestEveryEmittedDiagnosticCarriesAPosition(t *testing.T) {
	s := NewSink(nil)
	s.Add(UnknownHandler, pos(3, 14), "unknown handler symbol handlers.Missing", "")
	s.Add(UnusedFragment, pos(10, 1), "fragment UserRow is never rendered or bound", "")
	for _, d := range s.Diagnostics() {
		if err := d.Validate(); err != nil {
			t.Errorf("diagnostic failed NFR-009 validation: %v", err)
		}
	}
	// A diagnostic without a position is a defect, caught by Validate.
	bad := Diagnostic{ID: UnknownHandler, Severity: Error, Message: "no position"}
	if err := bad.Validate(); err == nil {
		t.Error("expected a positionless diagnostic to fail validation")
	}
}

func TestSeverityIsDataNotControlFlow(t *testing.T) {
	t.Run("default severity comes from the registry", func(t *testing.T) {
		s := NewSink(nil)
		s.Add(DanglingTarget, pos(1, 1), "no literal ID #missing", "")
		got := s.Diagnostics()
		if len(got) != 1 || got[0].Severity != Warning {
			t.Fatalf("expected one warning, got %+v", got)
		}
		if s.HasErrors() {
			t.Error("warnings alone must not fail the build")
		}
	})
	t.Run("a warning-class check can be promoted to an error", func(t *testing.T) {
		s := NewSink(map[string]Severity{DanglingTarget: Error})
		s.Add(DanglingTarget, pos(1, 1), "no literal ID #missing", "")
		got := s.Diagnostics()
		if len(got) != 1 || got[0].Severity != Error {
			t.Fatalf("expected one error, got %+v", got)
		}
		if !s.HasErrors() {
			t.Error("promoted warning must fail the build")
		}
	})
	t.Run("a warning-class check can be turned off", func(t *testing.T) {
		s := NewSink(map[string]Severity{UnusedFragment: Off})
		s.Add(UnusedFragment, pos(1, 1), "fragment X unused", "")
		if got := s.Diagnostics(); len(got) != 0 {
			t.Fatalf("expected no diagnostics, got %+v", got)
		}
	})
	t.Run("error-class checks cannot be demoted", func(t *testing.T) {
		s := NewSink(map[string]Severity{UnknownHandler: Warning})
		s.Add(UnknownHandler, pos(1, 1), "unknown handler", "")
		got := s.Diagnostics()
		if len(got) != 1 || got[0].Severity != Error {
			t.Fatalf("expected the override to be ignored, got %+v", got)
		}
	})
}

func TestDiagnosticsAreSortedDeterministically(t *testing.T) {
	s := NewSink(nil)
	s.Add(UnusedFragment, Position{File: "b.ghtmx", Line: 2, Col: 1}, "m1", "")
	s.Add(UnknownHandler, Position{File: "a.ghtmx", Line: 9, Col: 5}, "m2", "")
	s.Add(DanglingTarget, Position{File: "a.ghtmx", Line: 3, Col: 1}, "m3", "")
	got := s.Diagnostics()
	if got[0].Pos.File != "a.ghtmx" || got[0].Pos.Line != 3 {
		t.Errorf("expected a.ghtmx:3 first, got %v", got[0].Pos)
	}
	if got[2].Pos.File != "b.ghtmx" {
		t.Errorf("expected b.ghtmx last, got %v", got[2].Pos)
	}
}

func TestTextOutput(t *testing.T) {
	s := NewSink(nil)
	s.Add(UnknownHandler, pos(3, 14), "unknown handler symbol handlers.Missing", "did you mean handlers.ListUsers?")
	var buf bytes.Buffer
	if err := WriteText(&buf, s.Diagnostics()); err != nil {
		t.Fatal(err)
	}
	want := "app/page.ghtmx:3:14: error: GHTMX-E0101: unknown handler symbol handlers.Missing\n\tdid you mean handlers.ListUsers?\n"
	if buf.String() != want {
		t.Errorf("got %q, want %q", buf.String(), want)
	}
}

func TestJSONOutputIsStableAndParseable(t *testing.T) {
	s := NewSink(nil)
	s.Add(UnknownHandler, pos(3, 14), "unknown handler", "")
	var buf bytes.Buffer
	if err := WriteJSON(&buf, s.Diagnostics()); err != nil {
		t.Fatal(err)
	}
	var round []Diagnostic
	if err := json.Unmarshal(buf.Bytes(), &round); err != nil {
		t.Fatalf("output is not parseable JSON: %v", err)
	}
	if len(round) != 1 || round[0].ID != UnknownHandler || round[0].Pos.Line != 3 {
		t.Errorf("round-trip mismatch: %+v", round)
	}
	// An empty set marshals as [], not null.
	buf.Reset()
	if err := WriteJSON(&buf, nil); err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(buf.String()) != "[]" {
		t.Errorf("empty set should encode as [], got %q", buf.String())
	}
}

// Regression tests for review findings.

func TestInvalidOverrideValuesIgnored(t *testing.T) {
	s := NewSink(map[string]Severity{UnusedFragment: "bogus"})
	s.Add(UnusedFragment, pos(1, 1), "m", "")
	got := s.Diagnostics()
	if len(got) != 1 || got[0].Severity != Warning {
		t.Fatalf("a bogus override value must be ignored, got %+v", got)
	}
}

func TestErrorClassOffIsIgnored(t *testing.T) {
	s := NewSink(map[string]Severity{UnknownHandler: Off})
	s.Add(UnknownHandler, pos(1, 1), "m", "")
	if got := s.Diagnostics(); len(got) != 1 || got[0].Severity != Error {
		t.Fatalf("Off must not suppress an error-class check, got %+v", got)
	}
}

func TestOverridesMapIsCopied(t *testing.T) {
	overrides := map[string]Severity{UnusedFragment: Off}
	s := NewSink(overrides)
	overrides[UnusedFragment] = Error // mutate after construction
	s.Add(UnusedFragment, pos(1, 1), "m", "")
	if got := s.Diagnostics(); len(got) != 0 {
		t.Fatalf("the sink must not observe caller mutation, got %+v", got)
	}
}

func TestUnregisteredIDRecordedAsError(t *testing.T) {
	s := NewSink(nil)
	s.Add("GHTMX-X0000", pos(1, 1), "m", "")
	got := s.Diagnostics()
	if len(got) != 1 || got[0].Severity != Error {
		t.Fatalf("unregistered IDs must surface loudly, got %+v", got)
	}
}

func TestSamePositionTieBreak(t *testing.T) {
	s := NewSink(nil)
	s.Add(UnusedFragment, pos(1, 1), "zzz", "")
	s.Add(UnusedFragment, pos(1, 1), "aaa", "")
	got := s.Diagnostics()
	if got[0].Message != "aaa" {
		t.Errorf("same-position diagnostics must sort by message, got %q first", got[0].Message)
	}
}
