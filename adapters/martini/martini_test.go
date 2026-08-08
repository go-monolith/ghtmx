package martini_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	martinifw "github.com/go-martini/martini"

	martiniadapter "github.com/go-monolith/ghtmx/adapters/martini"
	"github.com/go-monolith/ghtmx/adapters/martini/fixture"
	"github.com/go-monolith/ghtmx/adapters/nethttp"
)

func itemsRequest(method string, htmx bool) *http.Request {
	req := httptest.NewRequest(method, "/items", nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return req
}

// TestFixtureServes: FR-035 through a real martini instance — mode
// selection, status, and header setting behave exactly like the nethttp
// adapter.
func TestFixtureServes(t *testing.T) {
	app := fixture.NewApp()

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
			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, itemsRequest(tt.method, tt.htmx))
			resp := rec.Result()
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

// TestWrappedWriterPassthrough: martini hands handlers its own
// ResponseWriter wrapper. Headers and cookies set on it before Render
// pass through exactly once, multi-value headers survive, the
// Content-Type default still applies, and the option status lands ahead
// of the body despite the wrapper's own WriteHeader bookkeeping.
func TestWrappedWriterPassthrough(t *testing.T) {
	m := martinifw.New()
	r := martinifw.NewRouter()
	m.MapTo(r, (*martinifw.Routes)(nil))
	m.Action(r.Handle)
	r.Get("/decorated", func(w http.ResponseWriter, req *http.Request) {
		if _, ok := w.(martinifw.ResponseWriter); !ok {
			t.Errorf("handler writer = %T, want martini's ResponseWriter wrapper", w)
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc"})
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Add("X-Custom", "one")
		w.Header().Add("X-Custom", "two")
		err := martiniadapter.Render(w, req, martiniadapter.WithPage(fixture.ItemsPage(), fixture.ItemRow()),
			martiniadapter.Status(http.StatusAccepted))
		if err != nil {
			t.Errorf("render /decorated: %v", err)
		}
	})

	rec := httptest.NewRecorder()
	m.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/decorated", nil))
	resp := rec.Result()
	defer resp.Body.Close()

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
		t.Errorf("Content-Type = %q, want the text/html default despite pre-set headers", got)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}
}

// TestOutputIdenticalToNetHTTP: the same inputs through a real martini
// instance and through the nethttp adapter produce byte-identical output
// in both modes, with matching status and htmx headers — martini's
// ResponseWriter wrapper is transparent.
func TestOutputIdenticalToNetHTTP(t *testing.T) {
	app := fixture.NewApp()
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

			rec := httptest.NewRecorder()
			app.ServeHTTP(rec, itemsRequest(http.MethodPost, htmx))
			resp := rec.Result()
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(reference.Body.Bytes(), body) {
				t.Errorf("martini output diverged from net/http:\n%q\n%q", reference.Body.Bytes(), body)
			}
			if resp.StatusCode != reference.Code {
				t.Errorf("status diverged: martini %d, net/http %d", resp.StatusCode, reference.Code)
			}
			for _, header := range []string{"HX-Retarget", "Content-Type"} {
				if got, want := resp.Header.Get(header), reference.Header().Get(header); got != want {
					t.Errorf("%s diverged: martini %q, net/http %q", header, got, want)
				}
			}
		})
	}
}
