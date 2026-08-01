package generatecmd

import (
	"testing"

	"github.com/go-monolith/ghtmx/internal/analyzer"
	parser "github.com/go-monolith/ghtmx/internal/parser"
)

// TestCentralEventsDeclarationSitesAreFileOnly pins the drift-class
// fix for events: declaration sites in the committed central package
// carry the module-relative file only, never line/column, so edits
// above an event declaration cannot churn generated output.
func TestCentralEventsDeclarationSitesAreFileOnly(t *testing.T) {
	tf, err := parser.ParseString("package x\n\nevent ItemSaved(id string)\n")
	if err != nil {
		t.Fatal(err)
	}
	tf.Filepath = "/mod/site/page.ghtmx"
	sa := analyzer.NewSetAnalysis()
	sa.Collect(tf, "example.com/x")
	events := (&Generate{}).centralEvents(sa, "/mod")
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].DeclaredAt != "site/page.ghtmx" {
		t.Errorf("DeclaredAt = %q, want the module-relative file only", events[0].DeclaredAt)
	}
}
