package revel_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	revelfw "github.com/revel/revel"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/adapters/nethttp"
	reveladapter "github.com/go-monolith/ghtmx/adapters/revel"
	"github.com/go-monolith/ghtmx/adapters/revel/fixture"
)

func itemsRequest(method string, htmx bool) *http.Request {
	req := httptest.NewRequest(method, "/items", nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return req
}

// newController builds a revel controller around httptest primitives
// through revel's own Go engine types (NewGoContext handles a nil server
// instance for exactly this) — no revel app boots; the revel package's
// import-time logger initialization is all the state Apply needs.
func newController(w http.ResponseWriter, r *http.Request) *revelfw.Controller {
	engine := revelfw.NewGoContext(nil)
	engine.Request.SetRequest(r)
	engine.Response.SetResponse(w)
	return revelfw.NewController(engine)
}

// TestFixtureServes: FR-035 through revel's real controller and engine
// types over HTTP — mode selection, status, and header setting behave
// exactly like the nethttp adapter.
func TestFixtureServes(t *testing.T) {
	srv := httptest.NewServer(fixture.NewHandler())
	defer srv.Close()

	tests := []struct {
		name       string
		method     string
		htmx       bool
		wantStatus int
		wantBody   string
		wantHeader map[string]string
	}{
		{
			name: "plain GET renders the full page", method: http.MethodGet,
			wantStatus: http.StatusOK, wantBody: `<html><body><ul id="items"><li>alpha</li></ul></body></html>`,
		},
		{
			name: "htmx GET renders the standalone fragment", method: http.MethodGet, htmx: true,
			wantStatus: http.StatusOK, wantBody: `<li>alpha</li>`,
		},
		{
			name: "htmx POST sets status and htmx headers", method: http.MethodPost, htmx: true,
			wantStatus: http.StatusCreated, wantBody: `<li>alpha</li>`,
			wantHeader: map[string]string{"HX-Retarget": "#items"},
		},
		{
			name: "plain POST keeps the status but drops htmx headers", method: http.MethodPost,
			wantStatus: http.StatusCreated,
			wantBody:   `<html><body><ul id="items"><li>alpha</li></ul></body></html>`,
			wantHeader: map[string]string{"HX-Retarget": ""},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, srv.URL+"/items", nil)
			if err != nil {
				t.Fatal(err)
			}
			if tt.htmx {
				req.Header.Set("HX-Request", "true")
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if got := string(body); got != tt.wantBody {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}
			if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
				t.Errorf("Content-Type = %q, want text/html", resp.Header.Get("Content-Type"))
			}
			for header, value := range tt.wantHeader {
				if got := resp.Header.Get(header); got != value {
					t.Errorf("%s = %q, want %q", header, got, value)
				}
			}
		})
	}
}

// TestOutputIdenticalToNetHTTP: the same inputs through the revel bridge
// and through the nethttp adapter produce byte-identical output in both
// modes, with matching status and htmx headers.
func TestOutputIdenticalToNetHTTP(t *testing.T) {
	srv := httptest.NewServer(fixture.NewHandler())
	defer srv.Close()

	for _, htmx := range []bool{false, true} {
		name := "plain request"
		if htmx {
			name = "htmx request"
		}
		t.Run(name, func(t *testing.T) {
			reference := httptest.NewRecorder()
			err := nethttp.Render(reference, itemsRequest(http.MethodPost, htmx),
				nethttp.WithPage(fixture.ItemsPage(), fixture.ItemRow()),
				nethttp.Status(http.StatusCreated), nethttp.Retarget("#items"))
			if err != nil {
				t.Fatal(err)
			}

			req, err := http.NewRequest(http.MethodPost, srv.URL+"/items", nil)
			if err != nil {
				t.Fatal(err)
			}
			if htmx {
				req.Header.Set("HX-Request", "true")
			}
			resp, err := srv.Client().Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(reference.Body.Bytes(), body) {
				t.Errorf("revel output diverged from net/http:\n%q\n%q", reference.Body.Bytes(), body)
			}
			if resp.StatusCode != reference.Code {
				t.Errorf("status diverged: revel %d, net/http %d", resp.StatusCode, reference.Code)
			}
			for _, header := range []string{"HX-Retarget", "Content-Type"} {
				if got, want := resp.Header.Get(header), reference.Header().Get(header); got != want {
					t.Errorf("%s diverged: revel %q, net/http %q", header, got, want)
				}
			}
		})
	}
}

// TestSeededHeadersFlushOnce: headers and cookies set through revel's
// response header before the Result applies pass through the staging
// bridge exactly once — revel's ServerHeader appends Set-Cookie
// unconditionally, so re-flushing seeded values would duplicate them —
// and multi-value headers survive while the Content-Type default still
// applies. The recorder snapshots headers at WriteHeader, so anything
// arriving after the status would be missing here.
func TestSeededHeadersFlushOnce(t *testing.T) {
	recorder := httptest.NewRecorder()
	c := newController(recorder, itemsRequest(http.MethodGet, false))
	out := c.Response.Out.Header()
	out.SetCookie((&http.Cookie{Name: "session", Value: "abc"}).String())
	out.Set("Cache-Control", "no-store")
	out.Add("X-Custom", "one")
	out.Add("X-Custom", "two")

	result := reveladapter.Result(c, reveladapter.WithPage(fixture.ItemsPage(), fixture.ItemRow()),
		reveladapter.Status(http.StatusAccepted))
	result.Apply(c.Request, c.Response)

	resp := recorder.Result()
	if got := resp.Header.Values("Set-Cookie"); len(got) != 1 {
		t.Errorf("Set-Cookie emitted %d times, want exactly once: %v", len(got), got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("pre-set Cache-Control = %q, want no-store", got)
	}
	if got := resp.Header.Values("X-Custom"); !slices.Equal(got, []string{"one", "two"}) {
		t.Errorf("multi-value header = %v, want [one two]", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want the text/html default despite seeded headers", got)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}
	if c.Response.Status != http.StatusAccepted {
		t.Errorf("c.Response.Status = %d, want %d written back for revel's request log",
			c.Response.Status, http.StatusAccepted)
	}
}

// TestControllerConventionsHonored: revel's documented conventions — a
// controller-assigned c.Response.Status and c.Response.ContentType —
// behave exactly as they do with revel's built-in results: they are the
// defaults, and the Status option wins over the assigned status.
func TestControllerConventionsHonored(t *testing.T) {
	t.Run("assigned status and content type are the defaults", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c := newController(recorder, itemsRequest(http.MethodGet, false))
		c.Response.Status = http.StatusTeapot
		c.Response.ContentType = "text/plain; charset=utf-8"

		result := reveladapter.Result(c, reveladapter.WithPage(fixture.ItemsPage(), fixture.ItemRow()))
		result.Apply(c.Request, c.Response)

		resp := recorder.Result()
		if resp.StatusCode != http.StatusTeapot {
			t.Errorf("status = %d, want the assigned %d", resp.StatusCode, http.StatusTeapot)
		}
		if got := resp.Header.Get("Content-Type"); got != "text/plain; charset=utf-8" {
			t.Errorf("Content-Type = %q, want the assigned text/plain to block the default", got)
		}
	})

	t.Run("the Status option wins over the assigned status", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		c := newController(recorder, itemsRequest(http.MethodGet, false))
		c.Response.Status = http.StatusTeapot

		result := reveladapter.Result(c, reveladapter.WithPage(fixture.ItemsPage(), fixture.ItemRow()),
			reveladapter.Status(http.StatusCreated))
		result.Apply(c.Request, c.Response)

		if got := recorder.Result().StatusCode; got != http.StatusCreated {
			t.Errorf("status = %d, want the option's %d over the assigned status", got, http.StatusCreated)
		}
	})
}

// failingFragment returns its error from both render paths without
// writing a byte.
type failingFragment struct{ err error }

func (f failingFragment) Render(ctx context.Context, w io.Writer) error         { return f.err }
func (f failingFragment) RenderFragment(ctx context.Context, w io.Writer) error { return f.err }

// TestRenderErrorIdentity: Render returns the runtime's error unchanged
// — callers assert identity against their sentinel, so wrapping would
// break them.
func TestRenderErrorIdentity(t *testing.T) {
	sentinel := errors.New("render exploded")
	recorder := httptest.NewRecorder()
	c := newController(recorder, itemsRequest(http.MethodGet, false))

	if err := reveladapter.Render(c.Request, c.Response, failingFragment{err: sentinel}); err != sentinel {
		t.Errorf("err = %v, want the sentinel returned unchanged", err)
	}
}

// The fakes below implement revel's ServerRequest and ServerResponse
// interfaces around httptest values, recording the order of header,
// status, and body operations. They prove the adapter works against the
// abstract engine surface — not just the Go engine — and that it stages
// every header before committing the status and the status before the
// first body byte: revel's Go engine freezes headers at SetStatus, so
// the order is observable behavior, not style.

type fakeContext struct {
	request  *http.Request
	response *fakeResponse
}

func (c fakeContext) GetRequest() revelfw.ServerRequest   { return fakeRequest{original: c.request} }
func (c fakeContext) GetResponse() revelfw.ServerResponse { return c.response }

type fakeRequest struct{ original *http.Request }

func (r fakeRequest) GetRaw() any       { return r.original }
func (r fakeRequest) Set(int, any) bool { return false }
func (r fakeRequest) Get(key int) (any, error) {
	switch key {
	case revelfw.HTTP_SERVER_HEADER:
		return fakeRequestHeader{original: r.original}, nil
	case revelfw.HTTP_METHOD:
		return r.original.Method, nil
	case revelfw.HTTP_URL:
		return r.original.URL, nil
	case revelfw.HTTP_HOST:
		return r.original.Host, nil
	case revelfw.HTTP_REQUEST_CONTEXT:
		return r.original.Context(), nil
	case revelfw.ENGINE_PATH:
		return r.original.URL.Path, nil
	case revelfw.HTTP_REQUEST_URI:
		return r.original.URL.String(), nil
	case revelfw.HTTP_REMOTE_ADDR:
		return r.original.RemoteAddr, nil
	}
	return nil, revelfw.ENGINE_UNKNOWN_GET
}

type fakeRequestHeader struct{ original *http.Request }

func (h fakeRequestHeader) SetCookie(string)   {}
func (h fakeRequestHeader) Set(string, string) {}
func (h fakeRequestHeader) Add(string, string) {}
func (h fakeRequestHeader) Del(string)         {}
func (h fakeRequestHeader) SetStatus(int)      {}
func (h fakeRequestHeader) GetCookie(string) (revelfw.ServerCookie, error) {
	return nil, http.ErrNoCookie
}
func (h fakeRequestHeader) Get(key string) []string { return h.original.Header.Values(key) }
func (h fakeRequestHeader) GetKeys() []string {
	keys := make([]string, 0, len(h.original.Header))
	for key := range h.original.Header {
		keys = append(keys, key)
	}
	return keys
}

type fakeResponse struct {
	recorder *httptest.ResponseRecorder
	events   []string
}

func (r *fakeResponse) record(event string) { r.events = append(r.events, event) }

func (r *fakeResponse) GetRaw() any { return r.recorder }
func (r *fakeResponse) Set(key int, value any) bool {
	if key == revelfw.ENGINE_RESPONSE_STATUS {
		r.record("status")
		r.recorder.WriteHeader(value.(int))
		return true
	}
	return false
}
func (r *fakeResponse) Get(key int) (any, error) {
	switch key {
	case revelfw.HTTP_SERVER_HEADER:
		return fakeResponseHeader{response: r}, nil
	case revelfw.ENGINE_WRITER:
		return recordingWriter{response: r}, nil
	}
	return nil, revelfw.ENGINE_UNKNOWN_GET
}

type fakeResponseHeader struct{ response *fakeResponse }

func (h fakeResponseHeader) SetCookie(cookie string) {
	h.response.record("header")
	h.response.recorder.Header().Add("Set-Cookie", cookie)
}
func (h fakeResponseHeader) GetCookie(string) (revelfw.ServerCookie, error) {
	return nil, http.ErrNoCookie
}
func (h fakeResponseHeader) Set(key, value string) {
	h.response.record("header")
	h.response.recorder.Header().Set(key, value)
}
func (h fakeResponseHeader) Add(key, value string) {
	h.response.record("header")
	h.response.recorder.Header().Add(key, value)
}
func (h fakeResponseHeader) Del(key string) { h.response.recorder.Header().Del(key) }
func (h fakeResponseHeader) Get(key string) []string {
	return h.response.recorder.Header().Values(key)
}
func (h fakeResponseHeader) GetKeys() []string {
	keys := make([]string, 0, len(h.response.recorder.Header()))
	for key := range h.response.recorder.Header() {
		keys = append(keys, key)
	}
	return keys
}
func (h fakeResponseHeader) SetStatus(code int) {
	h.response.record("status")
	h.response.recorder.WriteHeader(code)
}

type recordingWriter struct{ response *fakeResponse }

func (w recordingWriter) Write(p []byte) (int, error) {
	w.response.record("body")
	return w.response.recorder.Write(p)
}

// TestHeadersLandBeforeStatusAndBody: through the fake engine, every
// header operation happens before the single status commit, and the
// status commits before the first body byte.
func TestHeadersLandBeforeStatusAndBody(t *testing.T) {
	response := &fakeResponse{recorder: httptest.NewRecorder()}
	c := revelfw.NewController(fakeContext{request: itemsRequest(http.MethodPost, true), response: response})

	result := reveladapter.Result(c, reveladapter.WithPage(fixture.ItemsPage(), fixture.ItemRow()),
		reveladapter.Status(http.StatusCreated), reveladapter.Retarget("#items"))
	result.Apply(c.Request, c.Response)

	statusAt := slices.Index(response.events, "status")
	bodyAt := slices.Index(response.events, "body")
	if statusAt == -1 || bodyAt == -1 {
		t.Fatalf("events = %v, want at least one status and one body operation", response.events)
	}
	if got := len(slices.DeleteFunc(slices.Clone(response.events), func(e string) bool { return e != "status" })); got != 1 {
		t.Errorf("status committed %d times, want exactly once: %v", got, response.events)
	}
	if statusAt > bodyAt {
		t.Errorf("status committed after the first body byte: %v", response.events)
	}
	for i, event := range response.events {
		if event == "header" && i > statusAt {
			t.Errorf("header staged after the status commit: %v", response.events)
		}
	}

	resp := response.recorder.Result()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusCreated)
	}
	if got := resp.Header.Get("HX-Retarget"); got != "#items" {
		t.Errorf("HX-Retarget = %q, want #items", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want the text/html default", got)
	}
	if got := response.recorder.Body.String(); got != `<li>alpha</li>` {
		t.Errorf("body = %q, want the standalone fragment", got)
	}
}

// The fixture types must keep satisfying the runtime interfaces the
// adapter is generic over.
var (
	_ ghtmx.Component = fixture.ItemsPage()
	_ ghtmx.Fragment  = fixture.ItemRow()
)
