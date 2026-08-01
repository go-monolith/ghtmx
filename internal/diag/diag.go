// Package diag defines the ghtmx compiler's diagnostic model: stable
// identifiers, severities, source positions, and an accumulating sink.
//
// Severity is data, not control flow: the same check can be emitted as a
// warning or an error depending on configuration (FR-042 strict mode,
// FR-071 per-check severities). Every diagnostic carries a file:line:col
// position resolving to original source (NFR-009).
package diag

import (
	"fmt"
	"sort"
	"sync"
)

// Severity of a diagnostic.
type Severity string

const (
	Error   Severity = "error"
	Warning Severity = "warning"
	// Off suppresses a warning-class check entirely. It is valid only as a
	// configured override, never as an emitted severity.
	Off Severity = "off"
)

// Position is a location in an original source file. Line and Col are
// 1-indexed for display; Index is the byte offset in the file.
type Position struct {
	File  string `json:"file"`
	Line  uint32 `json:"line"`
	Col   uint32 `json:"col"`
	Index int64  `json:"-"`
}

func (p Position) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Diagnostic is a single positioned finding with a stable identifier.
type Diagnostic struct {
	ID       string   `json:"id"`
	Severity Severity `json:"severity"`
	Pos      Position `json:"pos"`
	Message  string   `json:"message"`
	Suggest  string   `json:"suggest,omitempty"`
}

func (d Diagnostic) String() string {
	s := fmt.Sprintf("%s: %s: %s: %s", d.Pos, d.Severity, d.ID, d.Message)
	if d.Suggest != "" {
		s += "\n\t" + d.Suggest
	}
	return s
}

// Validate reports whether the diagnostic satisfies the NFR-009 contract:
// a stable ID, a known severity, and a position resolving to a file.
func (d Diagnostic) Validate() error {
	if d.ID == "" {
		return fmt.Errorf("diagnostic has no ID: %q", d.Message)
	}
	if _, ok := Registry[d.ID]; !ok {
		return fmt.Errorf("diagnostic ID %q is not registered", d.ID)
	}
	if d.Severity != Error && d.Severity != Warning {
		return fmt.Errorf("diagnostic %s has invalid severity %q", d.ID, d.Severity)
	}
	if d.Pos.File == "" {
		return fmt.Errorf("diagnostic %s has no file position", d.ID)
	}
	if d.Pos.Line == 0 {
		return fmt.Errorf("diagnostic %s has no line position", d.ID)
	}
	if d.Pos.Col == 0 {
		return fmt.Errorf("diagnostic %s has no column position", d.ID)
	}
	if d.Message == "" {
		return fmt.Errorf("diagnostic %s has no message", d.ID)
	}
	return nil
}

// Sink accumulates diagnostics. Analysis is accumulative, never fail-fast:
// all diagnostics for a run are collected so the CLI and LSP report a
// complete set. Sink is safe for concurrent use.
type Sink struct {
	mu        sync.Mutex
	diags     []Diagnostic
	overrides map[string]Severity
}

// NewSink returns a sink applying the given per-check severity overrides,
// keyed by stable diagnostic ID. Overrides apply only to checks whose
// default severity is Warning: error-level checks guard correctness
// (constitution P2) and cannot be demoted or disabled. Invalid override
// values are ignored, and the map is copied so later caller mutation
// cannot change behavior mid-run.
func NewSink(overrides map[string]Severity) *Sink {
	copied := make(map[string]Severity, len(overrides))
	for id, sev := range overrides {
		if sev == Error || sev == Warning || sev == Off {
			copied[id] = sev
		}
	}
	return &Sink{overrides: copied}
}

// Add records a diagnostic for the check identified by id at pos.
// The severity is the check's registered default unless overridden.
// A warning-class check overridden to Off is dropped.
func (s *Sink) Add(id string, pos Position, message, suggest string) {
	check, ok := Registry[id]
	if !ok {
		// An unregistered ID is an internal defect; record it loudly as an
		// error rather than panicking on what may be user-facing output.
		s.append(Diagnostic{ID: id, Severity: Error, Pos: pos, Message: "internal: unregistered diagnostic ID: " + message})
		return
	}
	severity := check.DefaultSeverity
	if o, ok := s.overrides[id]; ok && check.DefaultSeverity == Warning {
		if o == Off {
			return
		}
		severity = o
	}
	s.append(Diagnostic{ID: id, Severity: severity, Pos: pos, Message: message, Suggest: suggest})
}

func (s *Sink) append(d Diagnostic) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.diags = append(s.diags, d)
}

// Diagnostics returns the accumulated diagnostics sorted by file, then
// position, then ID, so output order is deterministic (NFR-004).
func (s *Sink) Diagnostics() []Diagnostic {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Diagnostic, len(s.diags))
	copy(out, s.diags)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Pos.File != b.Pos.File {
			return a.Pos.File < b.Pos.File
		}
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line < b.Pos.Line
		}
		if a.Pos.Col != b.Pos.Col {
			return a.Pos.Col < b.Pos.Col
		}
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.Message < b.Message
	})
	return out
}

// HasErrors reports whether any error-level diagnostic was recorded.
// An error-level diagnostic fails the build; warnings alone never do
// (FR-060).
func (s *Sink) HasErrors() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.diags {
		if d.Severity == Error {
			return true
		}
	}
	return false
}
