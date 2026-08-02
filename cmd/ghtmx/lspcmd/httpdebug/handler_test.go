package httpdebug

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/lspcmd/proxy"
	"github.com/go-monolith/ghtmx/internal/format"
	"github.com/go-monolith/ghtmx/internal/parser"
)

// This is the LSP debug UI: when a source map looks wrong, this is where
// someone goes to see the .ghtmx and its generated Go side by side. The
// contract worth pinning is that each route serves the right artefact
// for a known uri and answers 404 rather than an empty 200 for an
// unknown one — an empty 200 reads as "the mapping is empty", which is a
// very different bug from "that file was never opened".

const (
	knownURI    = "file:///project/greeting.ghtmx"
	unknownURI  = "file:///project/absent.ghtmx"
	templSource = "package main\n"
	goSource    = "// generated\npackage main\n"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// serverWithDocument builds a proxy.Server holding one document in all
// three caches, which is the state the debug UI reads.
func serverWithDocument(t *testing.T) *proxy.Server {
	t.Helper()
	log := discardLog()
	s := proxy.NewServer(log, nil, proxy.NewSourceMapCache(), proxy.NewDiagnosticCache(), true, format.Config{})
	s.TemplSource.Set(knownURI, proxy.NewDocument(log, templSource))
	s.GoSource[knownURI] = goSource

	sm := parser.NewSourceMap()
	sm.Add(
		parser.Expression{
			Value: "main",
			Range: parser.Range{
				From: parser.Position{Line: 0, Col: 8},
				To:   parser.Position{Line: 0, Col: 12},
			},
		},
		parser.Range{
			From: parser.Position{Line: 1, Col: 8},
			To:   parser.Position{Line: 1, Col: 12},
		},
	)
	s.SourceMapCache.Set(knownURI, sm)
	return s
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestRoutesServeTheKnownDocument(t *testing.T) {
	h := NewHandler(discardLog(), serverWithDocument(t))

	tests := []struct {
		name   string
		path   string
		want   string
		isJSON bool
	}{
		{name: "templ source", path: "/templ", want: templSource},
		{name: "generated go", path: "/go", want: goSource},
		{name: "source map", path: "/sourcemap", isJSON: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := get(t, h, tt.path+"?uri="+knownURI)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			if tt.isJSON {
				if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
					t.Errorf("Content-Type = %q, want application/json", ct)
				}
				var decoded any
				if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
					t.Errorf("body is not valid JSON: %v\n%s", err, rec.Body.String())
				}
				return
			}
			if got := rec.Body.String(); got != tt.want {
				t.Errorf("body = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestRoutesReport404ForAnUnknownURI is the assertion that matters most:
// a 200 with an empty body would look like a document that mapped to
// nothing, sending someone hunting a source-map bug that does not exist.
func TestRoutesReport404ForAnUnknownURI(t *testing.T) {
	h := NewHandler(discardLog(), serverWithDocument(t))

	for _, path := range []string{"/templ", "/go", "/sourcemap", "/"} {
		t.Run(path, func(t *testing.T) {
			rec := get(t, h, path+"?uri="+unknownURI)

			if rec.Code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", rec.Code)
			}
			if rec.Body.Len() == 0 {
				t.Error("the 404 body is empty; it should say what was not found")
			}
		})
	}
}

// TestIndexListsKnownURIs covers the no-uri branch: the landing page is
// how someone finds the document they want in the first place.
func TestIndexListsKnownURIs(t *testing.T) {
	rec := get(t, NewHandler(discardLog(), serverWithDocument(t)), "/")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), knownURI) {
		t.Errorf("the listing does not mention the known uri:\n%s", rec.Body.String())
	}
}

// TestIndexVisualizesAKnownURI covers the side-by-side view, the reason
// the whole package exists.
func TestIndexVisualizesAKnownURI(t *testing.T) {
	rec := get(t, NewHandler(discardLog(), serverWithDocument(t)), "/?uri="+knownURI)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// The visualisation names the file and links mapped characters in
	// both directions.
	for _, want := range []string{knownURI, "src_", "tgt_"} {
		if !strings.Contains(body, want) {
			t.Errorf("the visualisation is missing %q", want)
		}
	}
}

func TestURLHelpers(t *testing.T) {
	const uri = "file:///a b.ghtmx"
	tests := []struct {
		name string
		got  ghtmx.SafeURL
		want ghtmx.SafeURL
	}{
		{"map", getMapURL(uri), "/?uri=file%3A%2F%2F%2Fa+b.ghtmx"},
		{"sourcemap", getSourceMapURL(uri), "/sourcemap?uri=file%3A%2F%2F%2Fa+b.ghtmx"},
		{"templ", getTemplURL(uri), "/templ?uri=file%3A%2F%2F%2Fa+b.ghtmx"},
		{"go", getGoURL(uri), "/go?uri=file%3A%2F%2F%2Fa+b.ghtmx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("= %q, want %q", tt.got, tt.want)
			}
		})
	}
}

// failingWriter is an http.ResponseWriter whose body writes always fail,
// which is how the response helpers' error branches are reached.
type failingWriter struct {
	header http.Header
	status int
}

func (w *failingWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}

func (w *failingWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func (w *failingWriter) WriteHeader(status int) { w.status = status }

// TestResponseHelpersSurviveAFailedWrite pins that a dropped connection
// logs rather than panics. The debug UI is opened from an editor, and a
// user closing the tab mid-response must not take the language server
// down with it.
func TestResponseHelpersSurviveAFailedWrite(t *testing.T) {
	var logged strings.Builder
	log = slog.New(slog.NewTextHandler(&logged, nil))

	tests := []struct {
		name string
		call func(http.ResponseWriter)
		want string
	}{
		{"String", func(w http.ResponseWriter) { String(w, "body") }, "failed to write string response"},
		{"JSON", func(w http.ResponseWriter) { JSON(w, map[string]int{"a": 1}) }, "failed to write JSON response"},
		{"Error", func(w http.ResponseWriter) { Error(w, "nope", http.StatusNotFound) }, "failed to write error response"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logged.Reset()
			tt.call(&failingWriter{})
			if !strings.Contains(logged.String(), tt.want) {
				t.Errorf("a failed write was not logged; want %q in:\n%s", tt.want, logged.String())
			}
		})
	}
}

// TestRenderFailuresAreReported covers the two render-error branches in
// the index handler. Both are reached the same way a real one would be:
// the client goes away mid-render.
func TestRenderFailuresAreReported(t *testing.T) {
	var logged strings.Builder
	log = slog.New(slog.NewTextHandler(&logged, nil))
	h := NewHandler(log, serverWithDocument(t))

	tests := []struct {
		name   string
		target string
	}{
		{"listing", "/"},
		{"visualisation", "/?uri=" + knownURI},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logged.Reset()
			// Must not panic: a disconnected client is routine.
			h.ServeHTTP(&failingWriter{}, httptest.NewRequest(http.MethodGet, tt.target, nil))
			if !strings.Contains(logged.String(), "failed to write") {
				t.Errorf("a failed render was not reported:\n%s", logged.String())
			}
		})
	}
}

func TestErrorSetsTheStatusAndBody(t *testing.T) {
	rec := httptest.NewRecorder()
	Error(rec, "uri not found", http.StatusNotFound)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	if got := rec.Body.String(); got != "uri not found" {
		t.Errorf("body = %q, want %q", got, "uri not found")
	}
}

func TestJSONEncodesTheValue(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, map[string]string{"key": "value"})

	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body is not valid JSON: %v", err)
	}
	if got["key"] != "value" {
		t.Errorf("decoded %v, want key=value", got)
	}
}
