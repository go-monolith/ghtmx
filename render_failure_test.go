package ghtmx

import (
	"context"
	"errors"
	"html/template"
	"strings"
	"testing"
)

// Every component in this package writes to an io.Writer supplied by the
// caller, and in a real server that writer is an http.ResponseWriter
// backed by a network connection the client can drop at any moment. The
// error branches that handle that are unreachable against the
// strings.Builder every other test uses, which leaves the behaviour that
// matters most under load completely unexercised.
//
// Failing the k-th write for every k reaches all of them, and asserts a
// property worth having: a dropped connection surfaces as an error, not
// as a silently truncated page or a swallowed write.

var errRenderWriteFailed = errors.New("connection dropped")

// failAtNthWrite fails the nth Write call (1-based) and every one after.
type failAtNthWrite struct {
	n     int
	calls int
}

func (w *failAtNthWrite) Write(p []byte) (int, error) {
	w.calls++
	if w.calls >= w.n {
		return 0, errRenderWriteFailed
	}
	return len(p), nil
}

type countingRenderWriter struct{ n int }

func (w *countingRenderWriter) Write(p []byte) (int, error) { w.n++; return len(p), nil }

// renderCases are the components this package hands to a server. Each is
// built fresh per render, since some carry once-only state.
var renderCases = []struct {
	name string
	make func() Component
}{
	{"raw", func() Component { return Raw("<b>bold</b>") }},
	{"join", func() Component {
		return Join(Raw("<p>one</p>"), Raw("<p>two</p>"), Raw("<p>three</p>"))
	}},
	{"join of joins", func() Component {
		return Join(Join(Raw("a"), Raw("b")), Join(Raw("c")))
	}},
	{"once with a component", func() Component {
		h := NewOnceHandle(WithComponent(Raw("<script>x</script>")))
		return h.Once()
	}},
	{"json script", func() Component {
		return JSONScript("data", map[string]any{"a": 1, "b": "two"})
	}},
	{"json script with a nonce", func() Component {
		return JSONScript("data", []int{1, 2, 3}).WithNonceFromString("abc123")
	}},
	{"json script with a type", func() Component {
		return JSONScript("data", map[string]int{"n": 1}).WithType("application/ld+json")
	}},
	{"htmx script tag", func() Component { return HTMXScriptTag("2.0.10") }},
	{"htmx script tag with a src override", func() Component {
		return HTMXScriptTag("2.0.10", WithScriptSrc("/static/htmx.js"))
	}},
	{"go html template", func() Component {
		return FromGoHTML(template.Must(template.New("t").Parse(`<div>{{.Name}}</div>`)),
			struct{ Name string }{Name: "world"})
	}},
}

// TestEveryComponentReportsAWriteFailure sweeps each component's write
// sites. A swallowed error here means the server logs a successful
// response after sending half a page.
func TestEveryComponentReportsAWriteFailure(t *testing.T) {
	// The render context has to be initialised: OnceHandle scopes its
	// "already rendered" state to it, and a server always renders
	// through one.
	ctx := InitializeContext(context.Background())

	for _, tc := range renderCases {
		t.Run(tc.name, func(t *testing.T) {
			var counter countingRenderWriter
			if err := tc.make().Render(ctx, &counter); err != nil {
				t.Fatalf("baseline render failed: %v", err)
			}
			if counter.n == 0 {
				t.Fatal("the component made no writes; the sweep would assert nothing")
			}

			for k := 1; k <= counter.n; k++ {
				err := tc.make().Render(ctx, &failAtNthWrite{n: k})
				if err == nil {
					t.Fatalf("write %d of %d failed but Render returned nil: "+
						"a dropped connection would be logged as a successful response",
						k, counter.n)
				}
				if !errors.Is(err, errRenderWriteFailed) {
					t.Fatalf("write %d of %d: Render returned %v, want it to wrap the write failure",
						k, counter.n, err)
				}
			}
		})
	}
}

// TestEveryComponentRendersSomething guards the sweep from over-reach: a
// component that wrote nothing at all would satisfy every assertion
// above by never reaching a failing write.
func TestEveryComponentRendersSomething(t *testing.T) {
	ctx := InitializeContext(context.Background())

	for _, tc := range renderCases {
		t.Run(tc.name, func(t *testing.T) {
			var sb strings.Builder
			if err := tc.make().Render(ctx, &sb); err != nil {
				t.Fatalf("render: %v", err)
			}
			if sb.Len() == 0 {
				t.Error("the component rendered nothing")
			}
		})
	}
}

// TestRawPropagatesItsErrors pins Raw's error passthrough: it exists so
// a caller that already knows a value is bad can hand the failure to the
// renderer rather than emitting broken markup.
func TestRawPropagatesItsErrors(t *testing.T) {
	sentinel := errors.New("precomputed failure")

	var sb strings.Builder
	err := Raw("<b>ignored</b>", sentinel).Render(context.Background(), &sb)
	if !errors.Is(err, sentinel) {
		t.Errorf("Render returned %v, want it to wrap %v", err, sentinel)
	}
}

// TestFromGoHTMLReportsTemplateFailures pins that a failing html/template
// execution surfaces, rather than rendering a partial document.
func TestFromGoHTMLReportsTemplateFailures(t *testing.T) {
	// Calling a method that does not exist fails at execution time.
	tmpl := template.Must(template.New("t").Parse(`{{.Missing.Field}}`))

	var sb strings.Builder
	if err := FromGoHTML(tmpl, struct{}{}).Render(context.Background(), &sb); err == nil {
		t.Error("Render succeeded on a template that cannot execute")
	}
}

// TestOnceRendersOnlyOnce is the whole point of OnceHandle: a script
// included by twenty components must reach the page once, or the browser
// runs it twenty times.
func TestOnceRendersOnlyOnce(t *testing.T) {
	h := NewOnceHandle(WithComponent(Raw("<script>init()</script>")))

	// Dedup is scoped to the render context, so all three have to share
	// one — which is exactly what a server does for a single response.
	ctx := InitializeContext(context.Background())
	var sb strings.Builder
	if err := Join(h.Once(), h.Once(), h.Once()).Render(ctx, &sb); err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(sb.String(), "init()"); got != 1 {
		t.Errorf("the once-only component rendered %d times, want 1", got)
	}
}

// TestOnceIsPerHandle pins the other half: two handles are two different
// things even in one context, and sharing state between them would
// suppress a script the page needs.
func TestOnceIsPerHandle(t *testing.T) {
	first := NewOnceHandle(WithComponent(Raw("<script>a()</script>")))
	second := NewOnceHandle(WithComponent(Raw("<script>b()</script>")))

	ctx := InitializeContext(context.Background())
	var sb strings.Builder
	if err := Join(first.Once(), second.Once()).Render(ctx, &sb); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a()", "b()"} {
		if !strings.Contains(sb.String(), want) {
			t.Errorf("output is missing %q:\n%s", want, sb.String())
		}
	}
}

// TestOnceIsPerContext pins the scope: a second response gets the script
// again. Sharing the flag across contexts would mean the second visitor
// to a page never receives it.
func TestOnceIsPerContext(t *testing.T) {
	h := NewOnceHandle(WithComponent(Raw("<script>init()</script>")))

	for i := range 2 {
		var sb strings.Builder
		ctx := InitializeContext(context.Background())
		if err := h.Once().Render(ctx, &sb); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(sb.String(), "init()") {
			t.Errorf("render %d did not emit the component: %q", i+1, sb.String())
		}
	}
}
