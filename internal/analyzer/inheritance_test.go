package analyzer

import (
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/diag"
	"github.com/go-monolith/ghtmx/internal/htmxsurface"
	parser "github.com/go-monolith/ghtmx/internal/parser"
)

// GHTMX-W0202 exists for one upgrade trap: markup that was correct under
// htmx 2's implicit inheritance and is silently wrong under htmx 4. The
// check has to fire on that shape and stay quiet on everything else — a
// warning on correct code is how people learn to ignore diagnostics.

func validateWith(t *testing.T, version string, overrides map[string]diag.Severity, src string) []diag.Diagnostic {
	t.Helper()
	tf, err := parser.ParseString(src)
	if err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	tf.Filepath = "app/page.ghtmx"
	surface, err := htmxsurface.ForVersion(version)
	if err != nil {
		t.Fatal(err)
	}
	sink := diag.NewSink(overrides)
	ValidateAttributes(tf, surface, sink)
	return sink.Diagnostics()
}

func onlyInheritance(t *testing.T, diags []diag.Diagnostic) []diag.Diagnostic {
	t.Helper()
	for _, d := range diags {
		if d.ID != diag.ImplicitInheritance {
			t.Fatalf("unexpected diagnostic %+v", d)
		}
	}
	return diags
}

const wrapperFixture = `package main

templ page() {
	<div hx-target="#out" hx-swap="innerHTML">
		<button hx-get={ handlers.Load }>Load</button>
	</div>
}
`

func TestImplicitInheritanceWarns(t *testing.T) {
	diags := onlyInheritance(t, validate(t, "4.0.0", wrapperFixture))
	if len(diags) != 2 {
		t.Fatalf("expected a warning per inheritable attribute, got %+v", diags)
	}
	d := diags[0]
	if !strings.Contains(d.Message, "hx-target on <div>") || !strings.Contains(d.Message, "line 5") {
		t.Errorf("message must name the attribute, element, and requesting descendant, got %q", d.Message)
	}
	if !strings.Contains(d.Suggest, "hx-target:inherited") || !strings.Contains(d.Suggest, "GHTMX-W0202") {
		t.Errorf("suggestion must show the :inherited form and the silence switch, got %q", d.Suggest)
	}
	if d.Severity != diag.Warning {
		t.Errorf("severity = %s, want warning", d.Severity)
	}
	if d.Pos.Line != 4 {
		t.Errorf("position = %+v, want the attribute's line 4", d.Pos)
	}
}

func TestImplicitInheritanceSilentCases(t *testing.T) {
	cases := map[string]string{
		"explicit :inherited": `<div hx-target:inherited="#out" hx-swap:inherited:append="innerHTML">
		<button hx-get={ handlers.Load }>Load</button>
	</div>`,
		"no requesting descendant": `<div hx-target="#out"><span>x</span></div>`,
		"element issues its own request": `<form hx-post={ handlers.Save } hx-target="#out" hx-headers='{"X-CSRF-Token":"t"}'>
		<button hx-get={ handlers.Load }>Load</button>
	</form>`,
		"hx-action counts as a request": `<form hx-action="/save" hx-method="POST" hx-target="#out"><input/></form>`,
		"boost on an anchor":            `<a hx-boost="true" href="/x">x</a>`,
		"boost wrapper without links":   `<div hx-boost="true"><span>x</span></div>`,
		"component call is opaque": `<div hx-target="#out">
		@rows()
	</div>`,
		"children are opaque": `<div hx-target="#out">
		{ children... }
	</div>`,
		"partial routing is not inheritance": `<hx-partial hx-target="#m" hx-swap="beforeend"><button hx-get={ handlers.Load }>x</button></hx-partial>`,
		"template partial":                   `<template hx type="partial" hx-target="#m"><button hx-get={ handlers.Load }>x</button></template>`,
		"non-inheritable attribute":          `<div hx-trigger="click"><button hx-get={ handlers.Load }>x</button></div>`,
		"listeners never inherit":            `<div hx-on::after:swap="x()"><button hx-get={ handlers.Load }>x</button></div>`,
	}
	for name, markup := range cases {
		t.Run(name, func(t *testing.T) {
			diags := validate(t, "4.0.0", "package main\n\ntempl page() {\n\t"+markup+"\n}\n")
			if len(diags) != 0 {
				t.Fatalf("expected no diagnostics, got %+v", diags)
			}
		})
	}
}

func TestImplicitInheritanceHeuristics(t *testing.T) {
	cases := []struct {
		name    string
		markup  string
		wantMsg string
	}{
		{"boost wrapper over a link", `<div hx-boost="true"><a href="/x">x</a></div>`, "hx-boost on <div>"},
		{"boost wrapper over a form", `<nav hx-boost="true"><form action="/x"></form></nav>`, "not boosted"},
		{"headers without descendants", `<body hx-headers='{"X-Token":"t"}'></body>`, "hx-headers on <body>"},
		{"headers with a CSRF token", `<body hx-headers='{"X-CSRF-Token":"t"}'></body>`, "CSRF"},
		{"headers with an expression value", `<body hx-headers={ ghtmx.CSRFHeader(token) }></body>`, "hx-headers on <body>"},
		{"conditional attribute counts", `<div if x { hx-target="#a" }><button hx-get={ handlers.Load }>x</button></div>`, "hx-target on <div>"},
		{"control flow is transparent", `<div hx-target="#out">
		for _, i := range items {
			if i != "" {
				<button hx-get={ handlers.Load }>x</button>
			}
		}
	</div>`, "line 7"},
		{"sse connection counts as a request", `<div hx-target="#out"><div hx-sse:connect="/events"></div></div>`, "hx-target on <div>"},
		{"wrapper inside a component block", `@layout() {
		<div hx-target="#out"><button hx-get={ handlers.Load }>x</button></div>
	}`, "hx-target on <div>"},
		{"boost false wrapper names the real effect", `<div hx-boost="false"><a href="/x">x</a></div>`, "still follows the nearest hx-boost:inherited"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			diags := onlyInheritance(t, validate(t, "4.0.0", "package main\n\ntempl page(x bool, items []string, token string) {\n\t"+tc.markup+"\n}\n"))
			if len(diags) != 1 {
				t.Fatalf("expected exactly one warning, got %+v", diags)
			}
			if !strings.Contains(diags[0].Message, tc.wantMsg) {
				t.Errorf("message %q must contain %q", diags[0].Message, tc.wantMsg)
			}
		})
	}
	// A non-CSRF header must not carry the CSRF remark.
	diags := validate(t, "4.0.0", "package main\n\ntempl page() {\n\t<body hx-headers='{\"X-Token\":\"t\"}'></body>\n}\n")
	if len(diags) != 1 || strings.Contains(diags[0].Message, "CSRF") {
		t.Errorf("no CSRF remark for an ordinary header, got %+v", diags)
	}
}

func TestImplicitInheritanceNotCheckedUnderHtmx2(t *testing.T) {
	if diags := validate(t, "2.0.10", wrapperFixture); len(diags) != 0 {
		t.Fatalf("htmx 2 inherits implicitly; got %+v", diags)
	}
}

func TestImplicitInheritanceSilenceable(t *testing.T) {
	diags := validateWith(t, "4.0.0", map[string]diag.Severity{diag.ImplicitInheritance: diag.Off}, wrapperFixture)
	if len(diags) != 0 {
		t.Fatalf("GHTMX-W0202=off must silence the check, got %+v", diags)
	}
	diags = validateWith(t, "4.0.0", map[string]diag.Severity{diag.ImplicitInheritance: diag.Error}, wrapperFixture)
	if len(diags) != 2 || diags[0].Severity != diag.Error {
		t.Fatalf("GHTMX-W0202=error must promote the check, got %+v", diags)
	}
}

func TestBaseAttrName(t *testing.T) {
	cases := map[string]string{
		"hx-target":                  "hx-target",
		"hx-target:inherited":        "hx-target",
		"hx-select:inherited:append": "hx-select",
		"hx-sse:connect":             "hx-sse:connect",
		"hx-on::after:swap":          "hx-on::after:swap",
		"hx-status:422":              "hx-status:422",
		"id":                         "id",
	}
	for in, want := range cases {
		if got := baseAttrName(in); got != want {
			t.Errorf("baseAttrName(%q) = %q, want %q", in, got, want)
		}
	}
}
