package analyzer

import (
	"fmt"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/diag"
)

// htmx 4 changes attribute names, adds name modifiers, and renames the
// events. A template pinned to 4.0.0 must accept the new syntax without a
// murmur and turn every htmx 2 leftover into a message that names the
// replacement — a generic "unknown attribute" would send people to the
// docs for something the compiler already knows.

func TestHtmx4CleanFixtureProducesNoDiagnostics(t *testing.T) {
	diags := validate(t, "4.0.0", `package main

templ page() {
	<form hx-post={ handlers.Save } hx-target="#out" hx-status:422="target:#errors select:#validation-errors" hx-confirm:inherited="Sure?" hx-config='{"timeout":30000}' hx-vals:inherited:append='{"a":1}'>
		<button hx-on::after:swap="done()" hx-on:click="beep()" hx-disable="this" hx-sync="closest form:abort">Go</button>
	</form>
	<div hx-sse:connect="/events" hx-sse:close="done" hx-live:disabled="!ok" hx-live="init()"></div>
	<div hx-swap="innerMorph show:top showTarget:#other" hx-trigger="click prevent, htmx:after:swap from:body"></div>
	<hx-partial hx-target="#messages" hx-swap="beforeend"><div>New</div></hx-partial>
	<template hx type="partial" hx-target="#x" hx-swap="append">x</template>
	<div hx-ignore hx-morph-skip hx-morph-skip-children hx-history-elt hx-preload="mouseover" hx-preserve></div>
	<a hx-boost="true" href="/x">x</a>
	<form hx-action="/search" hx-method="QUERY" hx-validate="true" hx-encoding="multipart/form-data"></form>
	<span hx-prompt="Reason?" hx-targets=".row" hx-history="false"></span>
}
`)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %+v", diags)
	}
}

func TestHtmx4MigrationHints(t *testing.T) {
	cases := []struct {
		name        string
		markup      string
		wantID      string
		wantMsg     string
		wantSuggest string
	}{
		{"hx-vars", `<div hx-vars="a:1"></div>`, diag.VersionMismatch, "hx-vars is not available in htmx 4.0.0: it was removed in 4.0.0", "js:"},
		{"hx-disabled-elt", `<button hx-disabled-elt="this">x</button>`, diag.VersionMismatch, "renamed to hx-disable", "use hx-disable"},
		{"hx-request", `<div hx-request='{"timeout":1}'></div>`, diag.VersionMismatch, "renamed to hx-config", "use hx-config"},
		{"hx-ext", `<div hx-ext="sse"></div>`, diag.VersionMismatch, "removed in 4.0.0", "extension script"},
		{"hx-disinherit", `<div hx-disinherit="*"></div>`, diag.VersionMismatch, "removed in 4.0.0", ":inherited"},
		{"bare hx-disable", `<div hx-disable></div>`, diag.InvalidAttributeValue, "hx-disable requires a value in htmx 4.0.0", "hx-ignore"},
		{"hx-target-404", `<div hx-target-404="#e"></div>`, diag.VersionMismatch, "hx-target-404 is not available in htmx 4.0.0", "hx-status"},
		{"hx-on-click", `<div hx-on-click="x()"></div>`, diag.VersionMismatch, "dash-form listeners", "hx-on:<event>"},
		{"hx-on::after-request", `<div hx-on::after-request="x()"></div>`, diag.VersionMismatch, "the htmx event after-request is named after:request", "hx-on::after:request"},
		{"hx-on:htmx:afterSwap", `<div hx-on:htmx:afterSwap="x()"></div>`, diag.VersionMismatch, "named after:swap", "hx-on::after:swap"},
		{"hx-on::xhr:progress", `<div hx-on::xhr:progress="x()"></div>`, diag.VersionMismatch, "was removed", "fetch"},
		{"queue modifier", `<div hx-trigger="click queue:first"></div>`, diag.InvalidAttributeValue, "hx-sync", ""},
		{"combined show form", `<div hx-swap="innerHTML show:#x:top"></div>`, diag.InvalidAttributeValue, "showTarget", ""},
		{"focus-scroll", `<div hx-swap="innerHTML focus-scroll:true"></div>`, diag.InvalidAttributeValue, "focusScroll", ""},
		{"trigger event rename", `<div hx-trigger="htmx:afterSwap from:body"></div>`, diag.InvalidAttributeValue, "htmx:after:swap", ""},
		{"inherit keyword", `<div hx-include="inherit"></div>`, diag.InvalidAttributeValue, "removed in htmx 4.0.0", ""},
		{"bare hx-status", `<div hx-status="swap:none"></div>`, diag.UnknownAttribute, "hx-status requires a status code suffix", "hx-status:404"},
		{"bad hx-status suffix", `<div hx-status:42="swap:none"></div>`, diag.UnknownAttribute, `"42" is not a valid hx-status suffix`, "hx-status:4xx"},
		{"bad hx-status value", `<div hx-status:404="bogus:1"></div>`, diag.InvalidAttributeValue, "unknown key", ""},
		{"not inheritable", `<div hx-trigger:inherited="click"></div>`, diag.UnknownAttribute, "hx-trigger does not inherit", "remove :inherited"},
		{"unknown modifier", `<div hx-swap:bogus="innerHTML"></div>`, diag.UnknownAttribute, "unknown attribute-name modifier :bogus", ":inherited"},
		{"duplicate modifier", `<div hx-swap:inherited:inherited="innerHTML"></div>`, diag.UnknownAttribute, "duplicate", ""},
		{"trailing colon", `<div hx-swap:="innerHTML"></div>`, diag.UnknownAttribute, "hx-swap: has a trailing colon", ":inherited"},
		{"trailing colon after a modifier", `<div hx-swap:inherited:="innerHTML"></div>`, diag.UnknownAttribute, "trailing colon", ":append"},
		{"typo", `<div hx-quer="x"></div>`, diag.UnknownAttribute, "unknown attribute hx-quer", "hx-query"},
		{"verb conflict", `<div hx-action="/x" hx-post></div>`, diag.AttributeConflict, "hx-action", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := validate(t, "4.0.0", fmt.Sprintf("package main\n\ntempl page() {\n\t%s\n}\n", tc.markup))
			if len(diags) != 1 {
				t.Fatalf("expected exactly 1 diagnostic, got %+v", diags)
			}
			d := diags[0]
			if d.ID != tc.wantID {
				t.Errorf("ID = %s, want %s (%s)", d.ID, tc.wantID, d.Message)
			}
			if !strings.Contains(d.Message, tc.wantMsg) {
				t.Errorf("message %q must contain %q", d.Message, tc.wantMsg)
			}
			if tc.wantSuggest != "" && !strings.Contains(d.Suggest, tc.wantSuggest) {
				t.Errorf("suggestion %q must contain %q", d.Suggest, tc.wantSuggest)
			}
			if d.Pos.Line != 4 {
				t.Errorf("position = %+v, want line 4", d.Pos)
			}
		})
	}
}

func TestHtmx4VerbAttributeStringURLIsCarveOut(t *testing.T) {
	diags := validate(t, "4.0.0", `package main

templ page() {
	<button hx-query="/search">Go</button>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.CarveOutStringURL {
		t.Fatalf("hx-query is a verb attribute and takes a typed binding, got %+v", diags)
	}
}

func TestHtmx2RejectsHtmx4SyntaxWithVersionHint(t *testing.T) {
	diags := validate(t, "2.0.10", `package main

templ page() {
	<div hx-target:inherited="#x"></div>
}
`)
	if len(diags) != 1 || diags[0].ID != diag.VersionMismatch {
		t.Fatalf("expected a version-mismatch diagnostic, got %+v", diags)
	}
	if !strings.Contains(diags[0].Message, "introduced in htmx 4.0.0") || !strings.Contains(diags[0].Suggest, "4.0.0") {
		t.Errorf("the message must name htmx 4, got %q / %q", diags[0].Message, diags[0].Suggest)
	}
	diags = validate(t, "2.0.10", `package main

templ page() {
	<div hx-status:422="swap:none" hx-query={ handlers.Search }></div>
}
`)
	if len(diags) != 2 {
		t.Fatalf("expected two unknown-attribute diagnostics, got %+v", diags)
	}
	for _, d := range diags {
		if d.ID != diag.UnknownAttribute {
			t.Errorf("ID = %s, want %s (%s)", d.ID, diag.UnknownAttribute, d.Message)
		}
	}
}

func TestHtmx2FixtureUnchangedUnderHtmx2(t *testing.T) {
	// htmx 2 syntax that htmx 4 rejects stays silent when pinned to 2.
	diags := validate(t, "2.0.10", `package main

templ page() {
	<div hx-vars="a:1" hx-ext="sse" hx-disable hx-on-click="x()" hx-on::after-request="y()" hx-swap="innerHTML show:#x:top focus-scroll:true" hx-trigger="click queue:first, htmx:afterSwap from:body">
		<button hx-target="#out" hx-get={ handlers.Load }>x</button>
	</div>
}
`)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics under htmx 2, got %+v", diags)
	}
}
