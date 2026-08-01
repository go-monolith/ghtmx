package diag

import (
	"encoding/json"
	"fmt"
	"io"
)

// WriteText writes diagnostics in the human-readable one-per-line format:
//
//	path/file.ghtmx:3:14: error: GHTMX-E0101: message
//		suggested fix
func WriteText(w io.Writer, diags []Diagnostic) error {
	for _, d := range diags {
		if _, err := fmt.Fprintln(w, d.String()); err != nil {
			return err
		}
	}
	return nil
}

// WriteJSON writes diagnostics as a stable, machine-parseable JSON array,
// for CI and editor integration (FR-045).
func WriteJSON(w io.Writer, diags []Diagnostic) error {
	if diags == nil {
		diags = []Diagnostic{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(diags)
}
