package fiberv3_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	fiberfw "github.com/gofiber/fiber/v3"

	fiberadapter "github.com/go-monolith/ghtmx/adapters/fiberv3"
	"github.com/go-monolith/ghtmx/adapters/fiberv3/fixture"
	"github.com/go-monolith/ghtmx/adapters/nethttp"
)

func itemsRequest(method string, htmx bool) *http.Request {
	req := httptest.NewRequest(method, "/items", nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return req
}

// TestFixtureServes: FR-035 through a real fiber app — mode selection,
// status, and header setting behave exactly like the nethttp adapter.
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
			resp, err := app.Test(itemsRequest(tt.method, tt.htmx))
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

// TestSeededHeadersFlushOnce: headers and cookies set before Render pass
// through the bridge exactly once — fasthttp appends Set-Cookie
// unconditionally, so re-flushing seeded values would duplicate them —
// and multi-value headers survive while the Content-Type default still
// applies.
func TestSeededHeadersFlushOnce(t *testing.T) {
	app := fiberfw.New()
	app.Get("/decorated", func(c fiberfw.Ctx) error {
		c.Cookie(&fiberfw.Cookie{Name: "session", Value: "abc"})
		c.Set("Cache-Control", "no-store")
		c.Response().Header.Add("X-Custom", "one")
		c.Response().Header.Add("X-Custom", "two")
		return fiberadapter.Render(c, fiberadapter.WithPage(fixture.ItemsPage(), fixture.ItemRow()),
			fiberadapter.Status(http.StatusAccepted))
	})

	resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/decorated", nil))
	if err != nil {
		t.Fatal(err)
	}
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
		t.Errorf("Content-Type = %q, want the text/html default despite seeded headers", got)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}
}

// TestOutputIdenticalToNetHTTP: the same inputs through the fiber bridge
// and through the nethttp adapter produce byte-identical output in both
// modes, with matching status and htmx headers.
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

			resp, err := app.Test(itemsRequest(http.MethodPost, htmx))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(reference.Body.Bytes(), body) {
				t.Errorf("fiber output diverged from net/http:\n%q\n%q", reference.Body.Bytes(), body)
			}
			if resp.StatusCode != reference.Code {
				t.Errorf("status diverged: fiber %d, net/http %d", resp.StatusCode, reference.Code)
			}
			for _, header := range []string{"HX-Retarget", "Content-Type"} {
				if got, want := resp.Header.Get(header), reference.Header().Get(header); got != want {
					t.Errorf("%s diverged: fiber %q, net/http %q", header, got, want)
				}
			}
		})
	}
}
