package analyzer

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/diag"
)

func validate(t *testing.T, version, src string) []diag.Diagnostic {
	t.Helper()
	return validateWith(t, version, nil, src)
}

func TestValidAttributesProduceNoDiagnostics(t *testing.T) {
	diags := validate(t, "2.0.10", `package main

templ page() {
	<button hx-target="#out" hx-swap="outerHTML transition:true" hx-trigger="click once">Go</button>
	<div hx-select="#content" hx-vals='{"a":1}'></div>
	<form hx-boost="true" hx-history-elt>
		<input hx-validate="true"/>
	</form>
	<button hx-on::after-request="alert('done')" hx-on:click="beep()">B</button>
	<div hx-target-404="#errors"></div>
	<span data-foo="not htmx"></span>
}
`)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", diags)
	}
}

func TestUnknownAttributeNameSuggestsClosest(t *testing.T) {
	diags := validate(t, "2.0.10", `package main

templ page() {
	<button hx-swp="outerHTML">Go</button>
}
`)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %+v", diags)
	}
	d := diags[0]
	if d.ID != diag.UnknownAttribute {
		t.Errorf("ID = %s", d.ID)
	}
	if !strings.Contains(d.Suggest, "hx-swap") {
		t.Errorf("suggestion must list the closest names, got %q", d.Suggest)
	}
	if d.Pos.File != "app/page.ghtmx" || d.Pos.Line != 4 {
		t.Errorf("position = %+v, want line 4", d.Pos)
	}
}

func TestInvalidConstrainedValueListsValidValues(t *testing.T) {
	diags := validate(t, "2.0.10", `package main

templ page() {
	<button hx-swap="replace">Go</button>
}
`)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %+v", diags)
	}
	d := diags[0]
	if d.ID != diag.InvalidAttributeValue {
		t.Errorf("ID = %s", d.ID)
	}
	if !strings.Contains(d.Message, "innerHTML") {
		t.Errorf("message must list valid values, got %q", d.Message)
	}
}

func TestContradictoryCombination(t *testing.T) {
	// Valueless verb attributes: presence is what conflicts (values would
	// separately trip the carve-out reporter).
	diags := validate(t, "2.0.10", `package main

templ page() {
	<button hx-get hx-post>Go</button>
}
`)
	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic, got %+v", diags)
	}
	d := diags[0]
	if d.ID != diag.AttributeConflict {
		t.Errorf("ID = %s", d.ID)
	}
	if !strings.Contains(d.Message, "hx-get") || !strings.Contains(d.Message, "hx-post") {
		t.Errorf("message must name the conflicting attributes, got %q", d.Message)
	}
}

func TestConditionalBranchesExemptFromCombinationCheck(t *testing.T) {
	// One verb in each conditional branch is legal: only one is present at
	// render time.
	diags := validate(t, "2.0.10", `package main

templ page(edit bool) {
	<button
		if edit {
			hx-put
		} else {
			hx-post
		}
	>Go</button>
}
`)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", diags)
	}
}

func TestConditionalBranchValuesStillValidated(t *testing.T) {
	diags := validate(t, "2.0.10", `package main

templ page(edit bool) {
	<button
		if edit {
			hx-swap="bogus"
		}
	>Go</button>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.InvalidAttributeValue {
		t.Fatalf("expected the branch value to be validated, got %+v", diags)
	}
}

func TestDynamicValuesAndKeysExempt(t *testing.T) {
	diags := validate(t, "2.0.10", `package main

templ page(mode string, attrs templ.Attributes) {
	<div hx-swap={ mode } { attrs... }></div>
	<div { "hx-bogus" }="x"></div>
}
`)
	if len(diags) != 0 {
		t.Fatalf("dynamic values, spreads, and expression keys are exempt, got %+v", diags)
	}
}

func TestBareHxOnRequiresEvent(t *testing.T) {
	diags := validate(t, "2.0.10", `package main

templ page() {
	<button hx-on="alert(1)">Go</button>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.UnknownAttribute {
		t.Fatalf("expected an unknown-attribute diagnostic, got %+v", diags)
	}
	if !strings.Contains(diags[0].Suggest, "hx-on:") {
		t.Errorf("suggestion must show the event forms, got %q", diags[0].Suggest)
	}
}

func TestVersionGatedValueMentionsIntroducingVersion(t *testing.T) {
	diags := validate(t, "2.0.0", `package main

templ page() {
	<div hx-include="inherit"></div>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.InvalidAttributeValue {
		t.Fatalf("expected a version-gated value diagnostic, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "2.0.5") {
		t.Errorf("message must name the introducing version, got %q", diags[0].Message)
	}
}

func TestNestedElementsAreWalked(t *testing.T) {
	diags := validate(t, "2.0.10", `package main

templ page(items []string) {
	for _, i := range items {
		if i != "" {
			<li><a hx-bogus-attr="x">{ i }</a></li>
		}
	}
}
`)
	if len(diags) != 1 || diags[0].ID != diag.UnknownAttribute {
		t.Fatalf("expected the nested attribute to be found, got %+v", diags)
	}
}
