package lspcmd

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-monolith/ghtmx/cmd/ghtmx/testproject"
	"github.com/go-monolith/ghtmx/internal/lsp/protocol"
	"github.com/go-monolith/ghtmx/internal/lsp/uri"
)

// Protocol-level tests for the ghtmx capabilities (spec task 56): fixture
// documents driven through the full server — jsonrpc2 streams, gopls
// proxy, analyzer — asserting completion, hover, and definition
// responses. Live diagnostics are covered by TestLiveDiagnostics. The
// fixture wiring lives in testdata: routes.go registers ListItems
// (GET /items) and CreateItem (POST /items) on a ServeMux, and
// htmx.ghtmx declares the ItemSaved event and binds both constructs.

// openHtmxFixture opens htmx.ghtmx on the server and returns its URI and
// content.
func openHtmxFixture(ctx context.Context, t *testing.T, appDir string, server protocol.Server) (docURI string, content string) {
	t.Helper()
	raw, err := os.ReadFile(appDir + "/htmx.ghtmx")
	if err != nil {
		t.Fatalf("failed to read fixture: %v", err)
	}
	docURI = "file://" + appDir + "/htmx.ghtmx"
	err = server.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{
		TextDocument: protocol.TextDocumentItem{
			URI:        uri.URI(docURI),
			LanguageID: "templ",
			Version:    1,
			Text:       string(raw),
		},
	})
	if err != nil {
		t.Fatalf("didOpen failed: %v", err)
	}
	return docURI, string(raw)
}

// completionDetails returns the Detail fields of items whose label or
// filter text contains the needle.
func completionDetails(list *protocol.CompletionList, needle string) []string {
	if list == nil {
		return nil
	}
	var out []string
	for _, item := range list.Items {
		if strings.Contains(item.Label, needle) || strings.Contains(item.FilterText, needle) {
			out = append(out, item.Detail)
		}
	}
	return out
}

// TestGhtmxCompletionProtocol: FR-081/FR-082 at the protocol level —
// verb-filtered route completion, attribute name and value completion for
// the configured version, and declared-event completion.
func TestGhtmxCompletionProtocol(t *testing.T) {
	if testing.Short() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	testOutput := bytes.NewBuffer(nil)
	log := slog.New(slog.NewJSONHandler(testOutput, nil))
	defer func() {
		if t.Failed() {
			fmt.Println(testOutput.String())
		}
	}()

	ctx, appDir, _, server, teardown, err := Setup(ctx, log, Arguments{})
	if err != nil {
		t.Fatalf("failed to setup test: %v", err)
	}
	defer teardown(t)
	defer cancel()

	docURI, content := openHtmxFixture(ctx, t, appDir, server)

	tests := []struct {
		name        string
		line        int // 1-based line to replace
		replacement string
		character   int // cursor column; 0 means end of the replacement
		assert      func(list *protocol.CompletionList) (msg string, ok bool)
	}{
		{
			name:        "attribute names for the configured version",
			line:        6,
			replacement: "\t<div hx-sw",
			assert: func(list *protocol.CompletionList) (msg string, ok bool) {
				if len(completionDetails(list, "hx-swap")) == 0 {
					return fmt.Sprintf("expected hx-swap in %+v", list), false
				}
				// The context is exclusive: nothing beyond the surface's
				// hx-* names may appear.
				for _, item := range list.Items {
					if !strings.HasPrefix(item.Label, "hx-sw") {
						return fmt.Sprintf("attribute-name context is exclusive, unexpected item %q", item.Label), false
					}
				}
				return "", true
			},
		},
		{
			// The line stays well-formed so the position maps into the
			// generated Go and the gopls merge genuinely happens: a
			// ListItems item with a non-route detail can only come from
			// gopls, and it must not carry the GET route's detail.
			name:        "verb expression merges matching-verb routes with gopls",
			line:        7,
			replacement: "\t<button hx-post={ CreateItem }>Create</button>",
			character:   19, // just after "{ "
			assert: func(list *protocol.CompletionList) (msg string, ok bool) {
				var post, goSymbol, getRoute bool
				for _, d := range completionDetails(list, "CreateItem") {
					if strings.Contains(d, "POST /items") {
						post = true
					}
				}
				for _, d := range completionDetails(list, "ListItems") {
					if strings.Contains(d, "GET /items") {
						getRoute = true
					} else {
						goSymbol = true
					}
				}
				if !post {
					return fmt.Sprintf("expected a CreateItem item with detail POST /items in %+v", list), false
				}
				if getRoute {
					return "hx-post must not offer the GET-registered route", false
				}
				if !goSymbol {
					return "expected a gopls-sourced ListItems item merged into the verb expression", false
				}
				return "", true
			},
		},
		{
			name:        "event listeners offer only declared events",
			line:        6,
			replacement: "\t<div hx-on:it",
			assert: func(list *protocol.CompletionList) (msg string, ok bool) {
				if list == nil || len(list.Items) != 1 || list.Items[0].Label != "item-saved" {
					return fmt.Sprintf("expected exactly the declared item-saved event, got %+v", list), false
				}
				return "", true
			},
		},
		{
			name:        "attribute values for the configured version",
			line:        6,
			replacement: "\t<div hx-swap=\"inner",
			assert: func(list *protocol.CompletionList) (msg string, ok bool) {
				if list == nil || len(list.Items) != 1 || list.Items[0].Label != "innerHTML" {
					return fmt.Sprintf("expected exactly innerHTML, got %+v", list), false
				}
				return "", true
			},
		},
		{
			name:        "hx-trigger values offer only declared events",
			line:        6,
			replacement: "\t<div hx-trigger=\"it",
			assert: func(list *protocol.CompletionList) (msg string, ok bool) {
				if list == nil || len(list.Items) != 1 || list.Items[0].Label != "item-saved" {
					return fmt.Sprintf("expected exactly the declared item-saved event, got %+v", list), false
				}
				return "", true
			},
		},
	}

	for i, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			updated := testproject.MustReplaceLine(content, test.line, test.replacement)
			err := server.DidChange(ctx, &protocol.DidChangeTextDocumentParams{
				TextDocument: protocol.VersionedTextDocumentIdentifier{
					TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri.URI(docURI)},
					Version:                int32(i + 2),
				},
				ContentChanges: []protocol.TextDocumentContentChangeEvent{{Range: nil, Text: updated}},
			})
			if err != nil {
				t.Fatalf("didChange failed: %v", err)
			}

			character := test.character
			if character == 0 {
				character = len(test.replacement)
			}
			var ok bool
			var msg string
			for range 10 {
				list, err := server.Completion(ctx, &protocol.CompletionParams{
					TextDocumentPositionParams: protocol.TextDocumentPositionParams{
						TextDocument: protocol.TextDocumentIdentifier{URI: uri.URI(docURI)},
						Position: protocol.Position{
							Line:      uint32(test.line - 1),
							Character: uint32(character),
						},
					},
				})
				if err != nil {
					t.Fatalf("completion failed: %v", err)
				}
				if msg, ok = test.assert(list); ok {
					break
				}
				time.Sleep(500 * time.Millisecond)
			}
			if !ok {
				t.Error(msg)
			}
		})
	}
}

// TestGhtmxHoverProtocol: FR-083 at the protocol level — hovering an
// event reference shows its declared payload type, and hovering a bound
// handler shows the route's verb, path, and registration site.
func TestGhtmxHoverProtocol(t *testing.T) {
	if testing.Short() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	testOutput := bytes.NewBuffer(nil)
	log := slog.New(slog.NewJSONHandler(testOutput, nil))
	defer func() {
		if t.Failed() {
			fmt.Println(testOutput.String())
		}
	}()

	ctx, appDir, _, server, teardown, err := Setup(ctx, log, Arguments{})
	if err != nil {
		t.Fatalf("failed to setup test: %v", err)
	}
	defer teardown(t)
	defer cancel()

	docURI, _ := openHtmxFixture(ctx, t, appDir, server)

	tests := []struct {
		name     string
		position protocol.Position
		contains []string
	}{
		{
			// <div hx-on:item-saved= ... — cursor on the wire name.
			name:     "event reference shows declared payload",
			position: protocol.Position{Line: 5, Character: 15},
			contains: []string{"event ItemSaved(id string)", "item-saved"},
		},
		{
			// <button hx-post={ CreateItem } — cursor on the handler.
			name:     "bound handler shows verb and path",
			position: protocol.Position{Line: 6, Character: 22},
			contains: []string{"POST /items", "CreateItem", "registered at"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			hover, err := server.Hover(ctx, &protocol.HoverParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: uri.URI(docURI)},
					Position:     test.position,
				},
			})
			if err != nil {
				t.Fatalf("hover failed: %v", err)
			}
			if hover == nil {
				t.Fatal("expected hover content, got nil")
			}
			for _, want := range test.contains {
				if !strings.Contains(hover.Contents.Value, want) {
					t.Errorf("hover must contain %q, got %q", want, hover.Contents.Value)
				}
			}
		})
	}
}

// TestGhtmxDefinitionProtocol: FR-084 at the protocol level — definition
// on an event reference opens its .ghtmx declaration, on a bound handler
// opens the Go registration site, and on a component reference opens the
// declaring .ghtmx file through the gopls translation path.
func TestGhtmxDefinitionProtocol(t *testing.T) {
	if testing.Short() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	testOutput := bytes.NewBuffer(nil)
	log := slog.New(slog.NewJSONHandler(testOutput, nil))
	defer func() {
		if t.Failed() {
			fmt.Println(testOutput.String())
		}
	}()

	ctx, appDir, _, server, teardown, err := Setup(ctx, log, Arguments{})
	if err != nil {
		t.Fatalf("failed to setup test: %v", err)
	}
	defer teardown(t)
	defer cancel()

	htmxURI, htmxContent := openHtmxFixture(ctx, t, appDir, server)

	tests := []struct {
		name       string
		docURI     string
		position   protocol.Position
		wantSuffix string
		wantLine   uint32
	}{
		{
			name:       "event reference opens its declaration",
			docURI:     htmxURI,
			position:   protocol.Position{Line: 5, Character: 15},
			wantSuffix: "htmx.ghtmx",
			wantLine:   2, // event ItemSaved(id string)
		},
		{
			name:       "bound handler opens the Go registration site",
			docURI:     htmxURI,
			position:   protocol.Position{Line: 6, Character: 22},
			wantSuffix: "routes.go",
			wantLine:   8, // mux.HandleFunc("POST /items", CreateItem)
		},
		{
			name:       "component reference opens the declaring template",
			docURI:     "file://" + appDir + "/remoteparent.ghtmx",
			position:   protocol.Position{Line: 3, Character: 3}, // @Remote
			wantSuffix: "remotechild.ghtmx",
			wantLine:   2, // templ Remote() {
		},
	}

	assertDefinition := func(t *testing.T, docURI string, position protocol.Position, wantSuffix string, wantLine uint32) {
		t.Helper()
		var ok bool
		var msg string
		for range 10 {
			locations, err := server.Definition(ctx, &protocol.DefinitionParams{
				TextDocumentPositionParams: protocol.TextDocumentPositionParams{
					TextDocument: protocol.TextDocumentIdentifier{URI: uri.URI(docURI)},
					Position:     position,
				},
			})
			if err != nil {
				t.Fatalf("definition failed: %v", err)
			}
			if len(locations) != 1 {
				msg = fmt.Sprintf("expected one location, got %+v", locations)
			} else if loc := locations[0]; !strings.HasSuffix(string(loc.URI), wantSuffix) || loc.Range.Start.Line != wantLine {
				msg = fmt.Sprintf("expected %s line %d, got %s %+v", wantSuffix, wantLine, loc.URI, loc.Range.Start)
			} else {
				ok = true
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if !ok {
			t.Error(msg)
		}
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertDefinition(t, test.docURI, test.position, test.wantSuffix, test.wantLine)
		})
	}

	// A bound symbol the route table cannot resolve must fall through to
	// gopls, which resolves the Go identifier and maps it back into the
	// template: HtmxPage is not a route, so definition lands on its templ
	// declaration.
	t.Run("unresolved bound symbol falls through to gopls", func(t *testing.T) {
		updated := testproject.MustReplaceLine(htmxContent, 7, "\t<button hx-post={ HtmxPage }>Create</button>")
		err := server.DidChange(ctx, &protocol.DidChangeTextDocumentParams{
			TextDocument: protocol.VersionedTextDocumentIdentifier{
				TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri.URI(htmxURI)},
				Version:                2,
			},
			ContentChanges: []protocol.TextDocumentContentChangeEvent{{Range: nil, Text: updated}},
		})
		if err != nil {
			t.Fatalf("didChange failed: %v", err)
		}
		// Cursor on HtmxPage; templ HtmxPage() is on 0-based line 4.
		assertDefinition(t, htmxURI, protocol.Position{Line: 6, Character: 22}, "htmx.ghtmx", 4)
	})
}

// TestLSPLatencyGate: NFR-003 — completion and diagnostic responses
// return in under 100ms on the fixture project. Env-gated like the
// NFR-001 regeneration budget so local runs stay quiet; CI runs it in the
// dedicated perf-gate job. Warm-up is untimed (gopls indexing is not our
// budget), medians damp scheduler noise, and one re-measurement is
// allowed before failing.
func TestLSPLatencyGate(t *testing.T) {
	if os.Getenv("GHTMX_PERF_GATE") == "" {
		t.Skip("set GHTMX_PERF_GATE=1 to run the NFR-003 LSP latency gate")
	}
	const budget = 100 * time.Millisecond
	const samples = 7

	ctx, cancel := context.WithCancel(context.Background())
	testOutput := bytes.NewBuffer(nil)
	log := slog.New(slog.NewJSONHandler(testOutput, nil))
	defer func() {
		if t.Failed() {
			fmt.Println(testOutput.String())
		}
	}()

	ctx, appDir, client, server, teardown, err := Setup(ctx, log, Arguments{})
	if err != nil {
		t.Fatalf("failed to setup test: %v", err)
	}
	defer teardown(t)
	defer cancel()

	recorder := client.(TestClient).diagnostics
	docURI, content := openHtmxFixture(ctx, t, appDir, server)
	version := int32(1)

	// The helpers return errors instead of failing the test: a transient
	// RPC hiccup on a shared runner must count as a failed attempt
	// eligible for the one re-measurement, not kill the gate outright.
	setLine := func(line int, replacement string) error {
		version++
		return server.DidChange(ctx, &protocol.DidChangeTextDocumentParams{
			TextDocument: protocol.VersionedTextDocumentIdentifier{
				TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: uri.URI(docURI)},
				Version:                version,
			},
			ContentChanges: []protocol.TextDocumentContentChangeEvent{
				{Range: nil, Text: testproject.MustReplaceLine(content, line, replacement)},
			},
		})
	}
	complete := func(line int, character int) (*protocol.CompletionList, error) {
		return server.Completion(ctx, &protocol.CompletionParams{
			TextDocumentPositionParams: protocol.TextDocumentPositionParams{
				TextDocument: protocol.TextDocumentIdentifier{URI: uri.URI(docURI)},
				Position:     protocol.Position{Line: uint32(line - 1), Character: uint32(character)},
			},
		})
	}

	// The verb line stays well-formed so its position maps into the
	// generated Go and the timed samples include the gopls merge — the
	// one completion path with a real round trip in it.
	const attrLine, attrPrefix = 6, "\t<div hx-sw"
	const verbLine, verbChar = 7, 19 // just after "{ " on the hx-post line
	const verbContent = "\t<button hx-post={ CreateItem }>Create</button>"

	// Warm-up: the measured paths must already return real results, so
	// the gate times steady-state work, not gopls's first indexing pass.
	// A ListItems item with a non-route detail can only come from gopls.
	if err := setLine(attrLine, attrPrefix); err != nil {
		t.Fatalf("didChange failed: %v", err)
	}
	if list, err := complete(attrLine, len(attrPrefix)); err != nil || len(completionDetails(list, "hx-swap")) == 0 {
		t.Fatalf("attribute completion returned nothing to measure: %+v %v", list, err)
	}
	if err := setLine(verbLine, verbContent); err != nil {
		t.Fatalf("didChange failed: %v", err)
	}
	warm := false
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		list, err := complete(verbLine, verbChar)
		if err != nil {
			continue
		}
		goplsReady := false
		for _, d := range completionDetails(list, "ListItems") {
			if !strings.Contains(d, "GET /items") {
				goplsReady = true
			}
		}
		if goplsReady && len(completionDetails(list, "CreateItem")) > 0 {
			warm = true
			break
		}
	}
	if !warm {
		t.Fatal("the gopls-merged route completion never warmed up")
	}

	median := func(f func() (time.Duration, error)) (time.Duration, error) {
		ds := make([]time.Duration, samples)
		for i := range ds {
			d, err := f()
			if err != nil {
				return 0, err
			}
			ds[i] = d
		}
		sort.Slice(ds, func(a, b int) bool { return ds[a] < ds[b] })
		return ds[samples/2], nil
	}
	// unique persists across measurement attempts, and the fixed digit
	// width keeps names prefix-free: a stale diagnostic from an earlier
	// sample can never satisfy a later one.
	unique := 0
	measure := func(name string, results map[string]time.Duration, f func() (time.Duration, error)) error {
		d, err := median(f)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		results[name] = d
		return nil
	}
	measureAll := func() (map[string]time.Duration, error) {
		results := map[string]time.Duration{}
		if err := setLine(attrLine, attrPrefix); err != nil {
			return results, err
		}
		err := measure("completion attr-name", results, func() (time.Duration, error) {
			start := time.Now()
			_, err := complete(attrLine, len(attrPrefix))
			return time.Since(start), err
		})
		if err != nil {
			return results, err
		}
		if err := setLine(verbLine, verbContent); err != nil {
			return results, err
		}
		err = measure("completion verb-expr", results, func() (time.Duration, error) {
			start := time.Now()
			_, err := complete(verbLine, verbChar)
			return time.Since(start), err
		})
		if err != nil {
			return results, err
		}
		err = measure("diagnostics", results, func() (time.Duration, error) {
			unique++
			attr := fmt.Sprintf("hx-zzz%03d", unique)
			start := time.Now()
			if err := setLine(attrLine, fmt.Sprintf("\t<div %s=\"x\">ok</div>", attr)); err != nil {
				return 0, err
			}
			for {
				diags, _ := recorder.Latest(docURI)
				for _, d := range diags {
					if d.Source == "ghtmx" && strings.Contains(d.Message, attr) {
						return time.Since(start), nil
					}
				}
				if time.Since(start) > 10*time.Second {
					return 0, fmt.Errorf("diagnostic for %s never arrived", attr)
				}
				time.Sleep(time.Millisecond)
			}
		})
		return results, err
	}

	// One re-measurement absorbs a scheduling hiccup on shared CI runners;
	// a genuine regression fails both rounds.
	for attempt := 1; ; attempt++ {
		results, err := measureAll()
		breached := err != nil
		if err != nil {
			t.Logf("attempt %d: measurement error: %v", attempt, err)
		}
		for name, d := range results {
			t.Logf("attempt %d: %s median %v (budget %v)", attempt, name, d, budget)
			if d >= budget {
				breached = true
			}
		}
		if !breached {
			return
		}
		if attempt == 2 {
			t.Fatalf("NFR-003 breached: %v, err=%v (budget %v)", results, err, budget)
		}
	}
}
