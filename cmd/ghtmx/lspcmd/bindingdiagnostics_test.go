package lspcmd

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-monolith/ghtmx/internal/lsp/protocol"
	"github.com/go-monolith/ghtmx/internal/lsp/uri"
)

// A bound handler is a func, not a URL. When `ghtmx generate` resolves
// the binding it folds it into the registered path, so the func is never
// assigned anywhere; the language server generates without a route
// table, so it used to emit `var v ghtmx.SafeURL = CreateItem` and gopls
// reported IncompatibleAssign against a template that builds.
//
// This is the symptom test: it asserts what the user sees, at the
// protocol level, rather than the shape of the generated Go.
func TestBoundHandlerProducesNoTypeError(t *testing.T) {
	if testing.Short() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	log := slog.New(slog.NewJSONHandler(bytes.NewBuffer(nil), nil))

	ctx, appDir, client, server, teardown, err := Setup(ctx, log, Arguments{})
	if err != nil {
		t.Fatalf("failed to setup test: %v", err)
	}
	defer teardown(t)
	defer cancel()

	recorder := client.(TestClient).diagnostics
	docURI := string(uri.File(appDir + "/bound.ghtmx"))

	// The undefined symbol is a deliberate control. Waiting for gopls to
	// report it proves gopls actually type-checked this document, which
	// is what makes the absence of a binding complaint below meaningful
	// rather than a race that always passes.
	const undefined = "deliberatelyUndefined"
	src := "package main\n\ntempl Bound() {\n" +
		"\t<button hx-post={ CreateItem }>Create</button>\n" +
		"\t<div>{ " + undefined + " }</div>\n}\n"
	if err := os.WriteFile(appDir+"/bound.ghtmx", []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := server.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        uri.URI(docURI),
			LanguageID: "templ",
			Version:    1,
			Text:       src,
		},
	}); err != nil {
		t.Fatalf("didOpen failed: %v", err)
	}

	var diags []protocol.Diagnostic
	var checked bool
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		diags, _ = recorder.Latest(docURI)
		for _, d := range diags {
			if strings.Contains(d.Message, undefined) {
				checked = true
				break
			}
		}
		if checked {
			break
		}
	}
	if !checked {
		// Skipping here would delete the only symptom-level assertion for
		// this bug while leaving CI green, so fail instead — as the other
		// gopls-dependent tests in this package do.
		t.Fatalf("gopls never reported the control error, so it never type-checked the document; got %+v", diags)
	}

	for _, d := range diags {
		if strings.Contains(d.Message, "ghtmx.SafeURL") || d.Code == "IncompatibleAssign" {
			t.Errorf("the bound handler produced a type error: %s (code %v, source %s)", d.Message, d.Code, d.Source)
		}
	}
}
