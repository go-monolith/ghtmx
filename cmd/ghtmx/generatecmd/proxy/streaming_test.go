package proxy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The dev proxy injects a reload script into HTML responses. It has to
// tell a buffered response from a streaming one, because rewriting a
// stream means holding it until the end — which turns a live SSE feed or
// a chunked page into a page that never arrives.

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// responseWith builds a response carrying the given headers, with a
// request attached because isStreaming reads its context.
func responseWith(t *testing.T, contentLength int64, headers map[string]string) *http.Response {
	t.Helper()
	r := &http.Response{
		Header:        make(http.Header),
		ContentLength: contentLength,
		Request:       httptest.NewRequest(http.MethodGet, "/", nil),
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestIsStreaming(t *testing.T) {
	tests := []struct {
		name          string
		contentLength int64
		headers       map[string]string
		want          bool
	}{
		{
			name:          "chunked transfer encoding",
			contentLength: -1,
			headers:       map[string]string{"Transfer-Encoding": "chunked"},
			want:          true,
		},
		{
			// Case must not matter: header values arrive however the
			// upstream server wrote them.
			name:          "chunked in mixed case",
			contentLength: -1,
			headers:       map[string]string{"Transfer-Encoding": "Chunked"},
			want:          true,
		},
		{
			// Neither header set: Go reports -1, and the response has to
			// be treated as a stream or an SSE feed would be buffered
			// until it ended, which is never.
			name:          "no content length and no transfer encoding",
			contentLength: -1,
			want:          true,
		},
		{
			name:          "known content length",
			contentLength: 1234,
			headers:       map[string]string{"Content-Length": "1234"},
			want:          false,
		},
		{
			name:          "empty body with a declared length",
			contentLength: 0,
			headers:       map[string]string{"Content-Length": "0"},
			want:          false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStreaming(responseWith(t, tt.contentLength, tt.headers), quietLogger()); got != tt.want {
				t.Errorf("isStreaming = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInsertScriptTagIntoBody(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		nonce    string
		wantErr  bool
		contains []string
	}{
		{
			name:     "ordinary document",
			body:     "<html><head></head><body><h1>hi</h1></body></html>",
			contains: []string{"<h1>hi</h1>", "script"},
		},
		{
			// The nonce has to reach the tag, or a page with a strict
			// CSP silently refuses to run the reload script and the
			// developer is left wondering why nothing reloads.
			name:     "with a nonce",
			body:     "<html><body></body></html>",
			nonce:    "abc123",
			contains: []string{"abc123"},
		},
		{
			// html.Parse synthesises a body for a fragment, so this
			// still succeeds — pinned so the behaviour is deliberate
			// rather than incidental.
			name:     "fragment without an explicit body",
			body:     "<div>fragment</div>",
			contains: []string{"fragment", "script"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := insertScriptTagIntoBody(tt.nonce, tt.body)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("insertScriptTagIntoBody: %v", err)
			}
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("output is missing %q:\n%s", want, got)
				}
			}
			// The original content must survive the rewrite.
			if strings.Contains(tt.body, "<h1>hi</h1>") && !strings.Contains(got, "<h1>hi</h1>") {
				t.Errorf("the original body content was lost:\n%s", got)
			}
		})
	}
}

// TestInsertScriptTagIsIdempotentlyParseable pins that the rewritten
// document is still valid HTML — the browser has to be able to parse
// what the proxy hands back.
func TestInsertScriptTagIsIdempotentlyParseable(t *testing.T) {
	once, err := insertScriptTagIntoBody("", "<html><body><p>x</p></body></html>")
	if err != nil {
		t.Fatal(err)
	}
	twice, err := insertScriptTagIntoBody("", once)
	if err != nil {
		t.Fatalf("the rewritten document could not be rewritten again: %v", err)
	}
	if !strings.Contains(twice, "<p>x</p>") {
		t.Errorf("content was lost on the second pass:\n%s", twice)
	}
}
