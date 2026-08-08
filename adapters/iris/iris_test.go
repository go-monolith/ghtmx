package iris_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	irisfw "github.com/kataras/iris/v12"

	irisadapter "github.com/go-monolith/ghtmx/adapters/iris"
	"github.com/go-monolith/ghtmx/adapters/iris/fixture"
	"github.com/go-monolith/ghtmx/adapters/nethttp"
)

func itemsRequest(method string, htmx bool) *http.Request {
	req := httptest.NewRequest(method, "/items", nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return req
}

// newHandler builds the iris application and returns it as the plain
// http.Handler the tests drive — no server socket, no iris test client.
func newHandler(t *testing.T, app *irisfw.Application) http.Handler {
	t.Helper()
	if err := app.Build(); err != nil {
		t.Fatal(err)
	}
	return app
}

// TestFixtureServes: FR-035 through a real iris application — mode
// selection, status, and header setting behave exactly like the nethttp
// adapter.
func TestFixtureServes(t *testing.T) {
	handler := newHandler(t, fixture.NewApp())

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
			handler.ServeHTTP(rec, itemsRequest(tt.method, tt.htmx))
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

// TestSeededHeadersReachTheWire: headers and cookies set through iris's
// context before Render pass through the recording writer untouched —
// its Header() is the underlying response's header map — while the
// Content-Type default still applies and the Status option's deferred
// WriteHeader lands before the body.
func TestSeededHeadersReachTheWire(t *testing.T) {
	app := irisfw.New()
	app.Get("/decorated", func(ctx irisfw.Context) {
		ctx.SetCookie(&http.Cookie{Name: "session", Value: "abc"})
		ctx.Header("Cache-Control", "no-store")
		ctx.Header("X-Custom", "one")
		ctx.Header("X-Custom", "two")
		err := irisadapter.Render(ctx, irisadapter.WithPage(fixture.ItemsPage(), fixture.ItemRow()),
			irisadapter.Status(http.StatusAccepted))
		if err != nil {
			t.Error(err)
		}
	})
	handler := newHandler(t, app)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/decorated", nil))
	resp := rec.Result()
	defer resp.Body.Close()

	if got := resp.Header.Values("Set-Cookie"); len(got) != 1 || !strings.HasPrefix(got[0], "session=abc") {
		t.Errorf("Set-Cookie = %v, want exactly one session=abc cookie", got)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("pre-set Cache-Control = %q, want no-store", got)
	}
	if got := resp.Header.Values("X-Custom"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("multi-value header = %v, want [one two]", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want the text/html default despite seeded headers", got)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}
}

// TestRecordedStatusLastWriteWins pins the one documented divergence of
// iris's status recording: a status the handler set before Render is
// replaced by the Status option, because nothing reaches the wire until
// the first body byte. net/http would send the first status immediately
// and drop the second as superfluous.
func TestRecordedStatusLastWriteWins(t *testing.T) {
	app := irisfw.New()
	app.Get("/preset", func(ctx irisfw.Context) {
		ctx.StatusCode(http.StatusTeapot)
		err := irisadapter.Render(ctx, irisadapter.WithPage(fixture.ItemsPage(), fixture.ItemRow()),
			irisadapter.Status(http.StatusAccepted))
		if err != nil {
			t.Error(err)
		}
	})
	handler := newHandler(t, app)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/preset", nil))

	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d, want %d: the last WriteHeader before the body should win", rec.Code, http.StatusAccepted)
	}
}

// TestOutputIdenticalToNetHTTP: the same inputs through the iris
// application and through the nethttp adapter produce byte-identical
// output in both modes, with matching status and htmx headers.
func TestOutputIdenticalToNetHTTP(t *testing.T) {
	handler := newHandler(t, fixture.NewApp())
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
			handler.ServeHTTP(rec, itemsRequest(http.MethodPost, htmx))
			resp := rec.Result()
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(reference.Body.Bytes(), body) {
				t.Errorf("iris output diverged from net/http:\n%q\n%q", reference.Body.Bytes(), body)
			}
			if resp.StatusCode != reference.Code {
				t.Errorf("status diverged: iris %d, net/http %d", resp.StatusCode, reference.Code)
			}
			for _, header := range []string{"HX-Retarget", "Content-Type"} {
				if got, want := resp.Header.Get(header), reference.Header().Get(header); got != want {
					t.Errorf("%s diverged: iris %q, net/http %q", header, got, want)
				}
			}
		})
	}
}
