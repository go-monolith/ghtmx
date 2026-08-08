package beego_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/beego/beego/v2/server/web"
	beegocontext "github.com/beego/beego/v2/server/web/context"

	beegoadapter "github.com/go-monolith/ghtmx/adapters/beego"
	"github.com/go-monolith/ghtmx/adapters/beego/fixture"
	"github.com/go-monolith/ghtmx/adapters/nethttp"
)

func itemsRequest(method string, htmx bool) *http.Request {
	req := httptest.NewRequest(method, "/items", nil)
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	return req
}

// TestFixtureServes: FR-035 through a real beego router over HTTP —
// mode selection, status, and header setting behave exactly like the
// nethttp adapter.
func TestFixtureServes(t *testing.T) {
	srv := httptest.NewServer(fixture.NewRouter())
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

// TestWrappedWriterPassThrough: beego's *context.Response wraps the
// connection's writer to track Started and Status — it must stay a
// pass-through. Headers and cookies set before Render survive it, the
// Content-Type default still applies through it, the Status option
// lands after those headers, and a status the handler already
// committed wins over the option — beego's wrapper drops the second
// WriteHeader silently. A pending ctx.Output.SetStatus is the reverse
// case: the router flushes it only after the handler returns, too late
// to beat Render's commit, so it is lost — the package doc tells
// handlers to use the Status option instead.
func TestWrappedWriterPassThrough(t *testing.T) {
	router := web.NewControllerRegister()
	router.Get("/decorated", func(ctx *beegocontext.Context) {
		http.SetCookie(ctx.ResponseWriter, &http.Cookie{Name: "session", Value: "abc"})
		ctx.ResponseWriter.Header().Set("Cache-Control", "no-store")
		ctx.ResponseWriter.Header().Add("X-Custom", "one")
		ctx.ResponseWriter.Header().Add("X-Custom", "two")
		err := beegoadapter.Render(ctx, beegoadapter.WithPage(fixture.ItemsPage(), fixture.ItemRow()),
			beegoadapter.Status(http.StatusAccepted))
		if err != nil {
			t.Errorf("render /decorated: %v", err)
		}
	})
	router.Get("/committed", func(ctx *beegocontext.Context) {
		ctx.ResponseWriter.WriteHeader(http.StatusTeapot)
		err := beegoadapter.Render(ctx, beegoadapter.WithPage(fixture.ItemsPage(), fixture.ItemRow()),
			beegoadapter.Status(http.StatusCreated))
		if err != nil {
			t.Errorf("render /committed: %v", err)
		}
	})
	router.Get("/late-output-status", func(ctx *beegocontext.Context) {
		ctx.Output.SetStatus(http.StatusTeapot)
		err := beegoadapter.Render(ctx, beegoadapter.WithPage(fixture.ItemsPage(), fixture.ItemRow()))
		if err != nil {
			t.Errorf("render /late-output-status: %v", err)
		}
	})
	srv := httptest.NewServer(router)
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL + "/decorated")
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
		t.Errorf("Content-Type = %q, want the text/html default despite pre-set headers", got)
	}
	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusAccepted)
	}

	resp, err = srv.Client().Get(srv.URL + "/committed")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d, want the handler's committed %d over the option", resp.StatusCode, http.StatusTeapot)
	}
	if want := `<html><body><ul id="items"><li>alpha</li></ul></body></html>`; string(body) != want {
		t.Errorf("body after committed status = %q, want %q", body, want)
	}

	resp, err = srv.Client().Get(srv.URL + "/late-output-status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200: the router flushes a pending ctx.Output.SetStatus only after the handler returns, too late to beat Render's commit", resp.StatusCode)
	}
}

// TestOutputIdenticalToNetHTTP: the same inputs through the beego
// bridge and through the nethttp adapter produce byte-identical output
// in both modes, with matching status and htmx headers.
func TestOutputIdenticalToNetHTTP(t *testing.T) {
	srv := httptest.NewServer(fixture.NewRouter())
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
				t.Errorf("beego output diverged from net/http:\n%q\n%q", reference.Body.Bytes(), body)
			}
			if resp.StatusCode != reference.Code {
				t.Errorf("status diverged: beego %d, net/http %d", resp.StatusCode, reference.Code)
			}
			for _, header := range []string{"HX-Retarget", "Content-Type"} {
				if got, want := resp.Header.Get(header), reference.Header().Get(header); got != want {
					t.Errorf("%s diverged: beego %q, net/http %q", header, got, want)
				}
			}
		})
	}
}
