package analyzer

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/diag"
	parser "github.com/go-monolith/ghtmx/internal/parser"
)

// eventDiags parses the files, runs CollectFile plus the whole-set Check,
// and returns diagnostics with the given ID.
func eventDiags(t *testing.T, files map[string]string, id string) []diag.Diagnostic {
	t.Helper()
	sa := NewSetAnalysis()
	for name, src := range files {
		tf, err := parser.ParseString(src)
		if err != nil {
			t.Fatalf("fixture %s does not parse: %v", name, err)
		}
		tf.Filepath = name
		sa.CollectFile(tf)
		sa.CollectFragments(tf, "example.com/app")
	}
	sink := diag.NewSink(nil)
	sa.Check(nil, sink)
	var out []diag.Diagnostic
	for _, d := range sink.Diagnostics() {
		if d.ID == id {
			out = append(out, d)
		}
	}
	return out
}

func TestWireName(t *testing.T) {
	tests := []struct{ in, want string }{
		{"UserCreated", "user-created"},
		{"CartCleared", "cart-cleared"},
		{"UserID", "user-id"},
		{"HTMLLoaded", "html-loaded"},
		{"Saved", "saved"},
	}
	for _, tt := range tests {
		if got := WireName(tt.in); got != tt.want {
			t.Errorf("WireName(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDuplicateEventAcrossFilesNamesBothDeclarations(t *testing.T) {
	diags := eventDiags(t, map[string]string{
		"a.ghtmx": "package a\n\nevent UserCreated(id string)\n",
		"b.ghtmx": "package b\n\nevent UserCreated(id string)\n",
	}, diag.DuplicateEvent)
	if len(diags) != 1 {
		t.Fatalf("expected one E0305, got %+v", diags)
	}
	d := diags[0]
	if d.Severity != diag.Error {
		t.Errorf("duplicates are compile errors, got %+v", d)
	}
	if d.Pos.File != "b.ghtmx" || !strings.Contains(d.Message, "a.ghtmx:3") {
		t.Errorf("both declarations must be named, got %+v", d)
	}
}

func TestWireNameCollisionIsDuplicate(t *testing.T) {
	diags := eventDiags(t, map[string]string{
		"a.ghtmx": "package a\n\nevent UserID(id string)\n",
		"b.ghtmx": "package b\n\nevent UserId(id string)\n",
	}, diag.DuplicateEvent)
	if len(diags) != 1 {
		t.Fatalf("expected one E0305, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, `"user-id"`) {
		t.Errorf("the collision message names the shared wire name, got %q", diags[0].Message)
	}
}

func TestUndeclaredEventReferenceIsError(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"hx-on listener", `<div hx-on:user-created="refresh()"></div>`},
		{"hx-trigger event", `<div hx-get="/x" hx-trigger="user-created from:body"></div>`},
		{"hx-trigger with filter", `<div hx-get="/x" hx-trigger="user-created[detail.ok] from:body"></div>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			diags := eventDiags(t, map[string]string{
				"page.ghtmx": "package main\n\ntempl page() {\n\t" + tt.body + "\n}\n",
			}, diag.UndeclaredEvent)
			if len(diags) != 1 {
				t.Fatalf("expected one E0304, got %+v", diags)
			}
			d := diags[0]
			if d.Severity != diag.Error || !strings.Contains(d.Message, `"user-created"`) {
				t.Errorf("the error names the event, got %+v", d)
			}
		})
	}
}

func TestDeclaredAndReferencedEventIsClean(t *testing.T) {
	files := map[string]string{
		"events.ghtmx": "package main\n\nevent UserCreated(id string)\n",
		"page.ghtmx":   "package main\n\ntempl page() {\n\t<div hx-on:user-created=\"refresh()\"></div>\n}\n",
	}
	for _, id := range []string{diag.UndeclaredEvent, diag.DuplicateEvent, diag.UnemittedEvent} {
		if diags := eventDiags(t, files, id); len(diags) != 0 {
			t.Errorf("expected no %s, got %+v", id, diags)
		}
	}
}

func TestUnreferencedEventWarns(t *testing.T) {
	diags := eventDiags(t, map[string]string{
		"events.ghtmx": "package main\n\nevent CartCleared()\n",
	}, diag.UnemittedEvent)
	if len(diags) != 1 {
		t.Fatalf("expected one W0102, got %+v", diags)
	}
	d := diags[0]
	if d.Severity != diag.Warning {
		t.Errorf("W0102 is a warning, got %+v", d)
	}
	if !strings.Contains(d.Message, `"CartCleared"`) || !strings.Contains(d.Message, `"cart-cleared"`) {
		t.Errorf("the warning names the event and wire name, got %q", d.Message)
	}
}

func TestNamespaceBoundaries(t *testing.T) {
	// DOM events, htmx-namespaced listeners, qualified trigger sources,
	// and polling forms are all outside the contract: no diagnostics.
	files := map[string]string{
		"page.ghtmx": `package main

templ page() {
	<div hx-on:click="go()"></div>
	<div hx-on::after-request="done()"></div>
	<div hx-on--after-swap="done()"></div>
	<div hx-get="/x" hx-trigger="click, keyup changed delay:500ms"></div>
	<div hx-get="/x" hx-trigger="sse:server-msg"></div>
	<div hx-get="/x" hx-trigger="every 2s"></div>
}
`,
	}
	if diags := eventDiags(t, files, diag.UndeclaredEvent); len(diags) != 0 {
		t.Fatalf("names outside the kebab-case contract must pass, got %+v", diags)
	}
}

func TestQualifiedListenersOutsideContract(t *testing.T) {
	// Long-form htmx namespace, dotted custom namespaces, and the all-dash
	// htmx form are all outside the contract.
	files := map[string]string{
		"page.ghtmx": `package main

templ page() {
	<div hx-on:htmx:after-request="done()"></div>
	<div hx-on:my.custom-event="go()"></div>
	<div hx-on-htmx-after-swap="done()"></div>
}
`,
	}
	if diags := eventDiags(t, files, diag.UndeclaredEvent); len(diags) != 0 {
		t.Fatalf("qualified names must pass, got %+v", diags)
	}
}

func TestExpressionValuedListenerCountsAsReference(t *testing.T) {
	// hx-on:user-created={ expr }: the value is dynamic but the key still
	// names the event.
	undeclared := map[string]string{
		"page.ghtmx": "package main\n\ntempl page(handler string) {\n\t<div hx-on:user-created={ handler }></div>\n}\n",
	}
	if diags := eventDiags(t, undeclared, diag.UndeclaredEvent); len(diags) != 1 {
		t.Fatalf("an expression-valued listener still references the event, got %+v", diags)
	}

	declared := map[string]string{
		"events.ghtmx": "package main\n\nevent UserCreated(id string)\n",
		"page.ghtmx":   "package main\n\ntempl page(handler string) {\n\t<div hx-on:user-created={ handler }></div>\n}\n",
	}
	if diags := eventDiags(t, declared, diag.UnemittedEvent); len(diags) != 0 {
		t.Fatalf("the listener credits the declaration, got %+v", diags)
	}
}

func TestFileDependencyFacts(t *testing.T) {
	sa := NewSetAnalysis()
	files := map[string]string{
		"shared/row.ghtmx": "package shared\n\nfragment Row(x string) {\n\t<tr><td>{ x }</td></tr>\n}\n",
		"pages/list.ghtmx": "package pages\n\nimport \"example.com/app/shared\"\n\nevent ItemSaved(id string)\n\ntempl list(x string) {\n\t@shared.Row(x)\n\t<div hx-on:item-saved=\"go()\"></div>\n}\n",
	}
	pkgs := map[string]string{
		"shared/row.ghtmx": "example.com/app/shared",
		"pages/list.ghtmx": "example.com/app/pages",
	}
	for name, src := range files {
		tf, err := parser.ParseString(src)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		tf.Filepath = name
		sa.CollectFile(tf)
		sa.CollectFragments(tf, pkgs[name])
	}
	sa.MarkBindingFile("pages/list.ghtmx")

	facts := sa.FileDependencyFacts()
	row := facts["shared/row.ghtmx"]
	if len(row.Decls) != 1 || row.Decls[0] != "example.com/app/shared.Row" {
		t.Errorf("row decls = %v", row.Decls)
	}
	list := facts["pages/list.ghtmx"]
	if len(list.Refs) != 1 || list.Refs[0] != "example.com/app/shared.Row" {
		t.Errorf("list refs = %v", list.Refs)
	}
	if !list.BindsRoutes {
		t.Error("the binding mark must surface in the facts")
	}
	if len(list.EventDecls) != 1 || list.EventDecls[0] != "item-saved" {
		t.Errorf("list event decls = %v", list.EventDecls)
	}
	if len(list.EventRefs) != 1 || list.EventRefs[0] != "item-saved" {
		t.Errorf("list event refs = %v", list.EventRefs)
	}
}

func TestDependencyFactsMethodTemplateEdges(t *testing.T) {
	sa := NewSetAnalysis()
	tf, err := parser.ParseString("package pages\n\nfragment Row(x string) {\n\t<tr><td>{ x }</td></tr>\n}\n\ntempl (r Repo) Page(x string) {\n\t@Row(x)\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	tf.Filepath = "pages/page.ghtmx"
	sa.Collect(tf, "example.com/app/pages")

	facts := sa.FileDependencyFacts()["pages/page.ghtmx"]
	found := false
	for _, ref := range facts.Refs {
		if ref == "example.com/app/pages.Row" {
			found = true
		}
	}
	if !found {
		t.Errorf("method-template references must create edges, got %v", facts.Refs)
	}
	// The method template itself declares nothing addressable, and the
	// templ declaration set holds only the fragment.
	if len(facts.Decls) != 1 || facts.Decls[0] != "example.com/app/pages.Row" {
		t.Errorf("decls = %v", facts.Decls)
	}
}

func TestRecollectionResetsBindingMark(t *testing.T) {
	sa := NewSetAnalysis()
	src := "package main\n\ntempl page() {\n\t<p>hi</p>\n}\n"
	tf, err := parser.ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	tf.Filepath = "page.ghtmx"
	sa.Collect(tf, "example.com/app")
	sa.MarkBindingFile("page.ghtmx")
	if !sa.FileDependencyFacts()["page.ghtmx"].BindsRoutes {
		t.Fatal("the mark must be visible")
	}
	// Re-analysis without a binding (the attribute was removed) resets.
	tf2, err := parser.ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	tf2.Filepath = "page.ghtmx"
	sa.Collect(tf2, "example.com/app")
	if sa.FileDependencyFacts()["page.ghtmx"].BindsRoutes {
		t.Error("re-collection must reset the binding mark until the resolver re-marks")
	}
}

func TestRemoveFilePurgesFacts(t *testing.T) {
	sa := NewSetAnalysis()
	tf, err := parser.ParseString("package main\n\nfragment Row(x string) {\n\t<tr><td>{ x }</td></tr>\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	tf.Filepath = "row.ghtmx"
	sa.Collect(tf, "example.com/app")
	if _, ok := sa.FileDependencyFacts()["row.ghtmx"]; !ok {
		t.Fatal("facts must be present after collection")
	}
	sa.RemoveFile("row.ghtmx")
	if _, ok := sa.FileDependencyFacts()["row.ghtmx"]; ok {
		t.Error("a removed file's facts must be purged")
	}
}
