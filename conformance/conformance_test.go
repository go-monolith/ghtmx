// The NFR-007 rendering conformance suite: exact expected-HTML
// comparison for every supported output context — HTML text, attribute,
// URL, script element and JS attribute, CSS (css blocks, style strings,
// style maps), spread attributes, the route-binding URL path (FR-023) —
// each with a context-confusion negative case, plus fragment
// render-mode byte-identity (FR-031) and HX-Trigger payload
// serialization (FR-037). It runs in every `go test ./...` and as a
// named CI step. (Root-level CONFORMANCE.md is a different artifact:
// the templ syntax-compatibility corpus.)
//
// The sanitizer sentinels pinned here (about:invalid#TemplFailedSanitizationURL,
// zTemplUnsafeCSSPropertyValue) keep the ported templ spelling: they
// are frozen wire format, deliberately pinned as literals rather than
// referencing the constants, so a rename cannot slip through unnoticed.
package conformance

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/ghtmxgen"
	ghtmxruntime "github.com/go-monolith/ghtmx/runtime"
)

// cssWant derives the expected css-block rendering from the same CSSID
// the runtime computes, so hash drift shows up as a readable class-name
// diff instead of an opaque hex mismatch.
func cssWant(declaration string) string {
	class := ghtmx.CSSID("conformanceColor", declaration)
	return `<style type="text/css">.` + class + `{` + declaration + `}</style><div class="` + class + `">styled</div>`
}

func render(t *testing.T, c ghtmx.Component) string {
	t.Helper()
	var b strings.Builder
	if err := c.Render(context.Background(), &b); err != nil {
		t.Fatalf("render failed: %v", err)
	}
	return b.String()
}

// TestEscapingContexts: every context neutralizes its hostile input —
// the forbidden substring is what successful context confusion would
// leave behind.
func TestEscapingContexts(t *testing.T) {
	tests := []struct {
		name      string
		component ghtmx.Component
		want      string
		forbidden string
	}{
		{
			name:      "html text escapes markup",
			component: TextContext(`<script>alert("x&y")</script>`),
			want:      `<p>&lt;script&gt;alert(&#34;x&amp;y&#34;)&lt;/script&gt;</p>`,
			forbidden: `<script>`,
		},
		{
			name:      "attribute value cannot close the quote",
			component: AttributeContext(`"><script>alert(1)</script>`),
			want:      `<div title="&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;">x</div>`,
			// A raw quote directly after the opening delimiter is the
			// breakout even when the angle brackets got escaped.
			forbidden: `title=""`,
		},
		{
			name:      "url context neutralizes javascript URLs",
			component: URLContext(`javascript:alert(1)`),
			want:      `<a href="about:invalid#TemplFailedSanitizationURL">link</a>`,
			forbidden: `javascript:`,
		},
		{
			name:      "url context keeps safe URLs attribute-escaped",
			component: URLContext(`/items?q=a&b=c d`),
			want:      `<a href="/items?q=a&amp;b=c d">link</a>`,
			forbidden: `q=a&b`,
		},
		{
			name:      "route binding escapes the path parameter",
			component: RouteBindingContext(`a/b?c#d&e f`),
			want:      `<button hx-get="/items/a%2Fb%3Fc%23d&amp;e%20f" hx-target="#detail">load</button>`,
			forbidden: `/items/a/b`,
		},
		{
			name:      "js argument cannot terminate the script",
			component: JSContext(`</script><script>alert(1)`),
			want:      `<button onClick="alert(&#34;\u003c/script\u003e\u003cscript\u003ealert(1)&#34;)">go</button>`,
			forbidden: `</script><script>`,
		},
		{
			name:      "json script cannot terminate its element",
			component: JSONScriptContext(map[string]string{"k": `</script><script>`}),
			want:      "<script id=\"conformance-data\" type=\"application/json\">{\"k\":\"\\u003c/script\\u003e\\u003cscript\\u003e\"}\n</script>",
			forbidden: `</script><script>`,
		},
		{
			name:      "css property value is sanitized",
			component: CSSContext(`red;} body{background:url(javascript:x)`),
			want:      cssWant("color:zTemplUnsafeCSSPropertyValue;"),
			forbidden: `body{`,
		},
		{
			name:      "css keeps safe values",
			component: CSSContext(`#00ff00`),
			want:      cssWant("color:#00ff00;"),
			forbidden: `zTemplUnsafeCSSPropertyValue`,
		},
		{
			name:      "script element cannot be terminated from either js context",
			component: ScriptElementContext(`</script><script>alert(1)`),
			want:      "<script>\n\t\tvar a = \"\\u003c/script\\u003e\\u003cscript\\u003ealert(1)\";\n\t\tvar b = \"\\u003c\\/script\\u003e\\u003cscript\\u003ealert(1)\";\n\t</script>",
			forbidden: `</script><script>`,
		},
		{
			// Plain-string style values are developer CSS: sanitized
			// against context breakout (the quote becomes a CSS escape),
			// not at the property level.
			name:      "style string cannot close the attribute quote",
			component: StyleAttributeContext(`color:red" onmouseover="alert(1)`),
			want:      `<button style="color:red\000022 onmouseover=\000022alert(1);">x</button>`,
			forbidden: `" onmouseover="`,
		},
		{
			// Map values are data: property-level sanitization applies.
			name:      "style map sanitizes property values",
			component: StyleMapContext(map[string]string{"background": "url(javascript:x)", "color": "#00ff00"}),
			want:      `<button style="background:zTemplUnsafeCSSPropertyValue;color:#00ff00;">x</button>`,
			forbidden: `url(javascript:`,
		},
		{
			name:      "spread attribute names cannot break out",
			component: SpreadAttributesContext(ghtmx.Attributes{`a"><b`: "v", "data-ok": `x"y`}),
			want:      `<div a&#34;&gt;&lt;b="v" data-ok="x&#34;y">x</div>`,
			forbidden: `"><b=`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := render(t, tt.component)
			if got != tt.want {
				t.Errorf("rendered\n%q\nwant\n%q", got, tt.want)
			}
			if strings.Contains(got, tt.forbidden) {
				t.Errorf("output contains %q: context confusion", tt.forbidden)
			}
		})
	}
}

// TestFragmentModesByteIdentical: FR-031 — the inline wrapper, the
// standalone wrapper's Render, and RenderFragment produce identical
// bytes for identical inputs, hostile ones included, and the inline
// rendering inside a page carries the same bytes.
func TestFragmentModesByteIdentical(t *testing.T) {
	for _, input := range []string{"alpha", `<script>alert("x&y")</script>`} {
		inline := render(t, ConformanceRow(input))

		standalone := ConformanceRowFragment(input)
		full := render(t, standalone)
		var viaFragment strings.Builder
		if err := standalone.RenderFragment(context.Background(), &viaFragment); err != nil {
			t.Fatal(err)
		}

		if inline != full || inline != viaFragment.String() {
			t.Errorf("fragment modes diverged for %q:\ninline     %q\nrender     %q\nstandalone %q",
				input, inline, full, viaFragment.String())
		}
		if page, want := render(t, ItemsPage([]string{input})), `<ul id="items">`+inline+`</ul>`; page != want {
			t.Errorf("page render is not the exact fragment composition:\npage %q\nwant %q", page, want)
		}
	}
}

// TestHXTriggerSerialization: FR-037 — the wire forms of HX-Trigger
// across merges, payload upgrades, and non-ASCII payloads (headers are
// Latin-1, so JSON escapes to \uXXXX with surrogate pairs).
func TestHXTriggerSerialization(t *testing.T) {
	type step struct {
		name    string
		payload any
	}
	tests := []struct {
		name   string
		header string
		steps  []step
		want   string
	}{
		{
			name:   "single event stays a bare name",
			header: "HX-Trigger",
			steps:  []step{{name: "cart-cleared"}},
			want:   "cart-cleared",
		},
		{
			name:   "payload-less events form a comma list",
			header: "HX-Trigger",
			steps:  []step{{name: "cart-cleared"}, {name: "item-saved"}},
			want:   "cart-cleared, item-saved",
		},
		{
			name:   "a payload upgrades the header to JSON",
			header: "HX-Trigger",
			steps:  []step{{name: "cart-cleared"}, {name: "item-saved", payload: map[string]string{"id": "1"}}},
			want:   `{"cart-cleared":null,"item-saved":{"id":"1"}}`,
		},
		{
			name:   "re-emitting an event replaces its payload in place",
			header: "HX-Trigger",
			steps: []step{
				{name: "item-saved", payload: map[string]string{"id": "1"}},
				{name: "item-saved", payload: map[string]string{"id": "2"}},
			},
			want: `{"item-saved":{"id":"2"}}`,
		},
		{
			name:   "non-ascii payloads escape to the latin-1 header plane",
			header: "HX-Trigger-After-Settle",
			steps:  []step{{name: "item-saved", payload: map[string]string{"name": "héllo 🎉"}}},
			want:   `{"item-saved":{"name":"h\u00E9llo \uD83C\uDF89"}}`,
		},
		{
			name:   "after-swap timing uses its own header",
			header: "HX-Trigger-After-Swap",
			steps:  []step{{name: "cart-cleared"}},
			want:   "cart-cleared",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			for _, s := range tt.steps {
				var err error
				switch tt.header {
				case "HX-Trigger":
					err = ghtmxruntime.AppendTrigger(w, s.name, s.payload)
				case "HX-Trigger-After-Settle":
					err = ghtmxruntime.AppendTriggerAfterSettle(w, s.name, s.payload)
				case "HX-Trigger-After-Swap":
					err = ghtmxruntime.AppendTriggerAfterSwap(w, s.name, s.payload)
				}
				if err != nil {
					t.Fatalf("append %s: %v", s.name, err)
				}
			}
			if got := w.Header().Get(tt.header); got != tt.want {
				t.Errorf("%s = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

// TestGeneratedEmitterSerialization: FR-037's user-facing surface is
// the generated ghtmxgen.Emit* symbols — the conformance gate must
// cover the generated layer, not only the runtime plumbing beneath it.
func TestGeneratedEmitterSerialization(t *testing.T) {
	w := httptest.NewRecorder()
	if err := ghtmxgen.EmitCartCleared(w); err != nil {
		t.Fatal(err)
	}
	if err := ghtmxgen.EmitItemSaved(w, ghtmxgen.ItemSavedPayload{Id: "1", Name: "n"}); err != nil {
		t.Fatal(err)
	}
	if got, want := w.Header().Get("HX-Trigger"), `{"cart-cleared":null,"item-saved":{"id":"1","name":"n"}}`; got != want {
		t.Errorf("HX-Trigger = %q, want %q", got, want)
	}

	w = httptest.NewRecorder()
	if err := ghtmxgen.EmitItemSavedAfterSettle(w, ghtmxgen.ItemSavedPayload{Id: "2"}); err != nil {
		t.Fatal(err)
	}
	if got, want := w.Header().Get("HX-Trigger-After-Settle"), `{"item-saved":{"id":"2","name":""}}`; got != want {
		t.Errorf("HX-Trigger-After-Settle = %q, want %q", got, want)
	}

	w = httptest.NewRecorder()
	if err := ghtmxgen.EmitCartClearedAfterSwap(w); err != nil {
		t.Fatal(err)
	}
	if got, want := w.Header().Get("HX-Trigger-After-Swap"), "cart-cleared"; got != want {
		t.Errorf("HX-Trigger-After-Swap = %q, want %q", got, want)
	}
}
