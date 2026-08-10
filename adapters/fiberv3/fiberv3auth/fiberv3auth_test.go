package fiberv3auth_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	fiberfw "github.com/gofiber/fiber/v3"

	"github.com/go-monolith/ghtmx/adapters/fiberv3/fiberv3auth"
	"github.com/go-monolith/ghtmx/auth"
)

// store is the application side of the contract: an in-memory session
// store keyed by the token hash, exactly the store-the-hash shape the
// auth package prescribes.
type store struct{ sessions map[string]string }

func (s *store) Authenticate(_ context.Context, token string) (string, error) {
	id, ok := s.sessions[auth.HashSessionToken(token)]
	if !ok {
		return "", fmt.Errorf("unknown session: %w", auth.ErrUnauthorized)
	}
	return id, nil
}

// newApp wires a minimal sign-in/protected/sign-out app through the
// glue package, the way an application would.
func newApp() (*fiberfw.App, *store, auth.Config[string]) {
	st := &store{sessions: map[string]string{}}
	cfg := auth.Config[string]{Authenticator: st, LoginURL: "/login"}

	app := fiberfw.New()
	app.Get("/login", func(c fiberfw.Ctx) error {
		v, err := fiberv3auth.SetLoginCSRFCookie(c, cfg)
		if err != nil {
			return err
		}
		return c.SendString("login-csrf:" + v)
	})
	app.Post("/login", func(c fiberfw.Ctx) error {
		if !fiberv3auth.ValidLoginCSRF(c, cfg, c.FormValue("login_csrf")) {
			return c.Status(fiberfw.StatusForbidden).SendString("bad login csrf")
		}
		if c.FormValue("user") != "alice" || c.FormValue("pass") != "secret" {
			return c.Status(fiberfw.StatusUnauthorized).SendString("bad credentials")
		}
		token, hash, err := auth.NewSessionToken()
		if err != nil {
			return err
		}
		st.sessions[hash] = "alice"
		fiberv3auth.SetSessionCookie(c, cfg, token)
		return c.Redirect().Status(fiberfw.StatusSeeOther).To("/private")
	})

	priv := app.Group("/", fiberv3auth.New(cfg), fiberv3auth.CSRF())
	priv.Get("/private", func(c fiberfw.Ctx) error {
		id, ok := fiberv3auth.IdentityFrom[string](c)
		if !ok {
			return c.SendStatus(fiberfw.StatusInternalServerError)
		}
		tok, _ := auth.CSRFTokenFrom(c.Context())
		c.Set("X-Test-CSRF", tok)
		return c.SendString("hello " + id)
	})
	priv.Post("/private", func(c fiberfw.Ctx) error {
		return c.SendString("posted")
	})
	priv.Post("/logout", func(c fiberfw.Ctx) error {
		fiberv3auth.ClearSessionCookie(c, cfg)
		return c.Redirect().Status(fiberfw.StatusSeeOther).To("/login")
	})
	return app, st, cfg
}

func do(t *testing.T, app *fiberfw.App, req *http.Request) *http.Response {
	t.Helper()
	res, err := app.Test(req)
	if err != nil {
		t.Fatalf("app.Test: %v", err)
	}
	return res
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// clearedSession reports whether the response deletes the session
// cookie the way fiber v3 must: v3 serializes via net/http, so MaxAge
// -1 goes out as Max-Age=0 and parses back as MaxAge -1.
func clearedSession(res *http.Response, name string) bool {
	for _, ck := range res.Cookies() {
		if ck.Name == name && ck.Value == "" && ck.MaxAge == -1 {
			return true
		}
	}
	return false
}

// TestAuthFlow drives the full life of a session through the fiber v3
// glue: unauthenticated redirects (htmx-aware), login CSRF, sign-in,
// CSRF on unsafe methods, sign-out, and revocation.
func TestAuthFlow(t *testing.T) {
	app, st, cfg := newApp()

	t.Run("unauthenticated browser navigation gets a 303", func(t *testing.T) {
		res := do(t, app, httptest.NewRequest(http.MethodGet, "/private", nil))
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
			t.Fatalf("got %d Location=%q, want 303 /login", res.StatusCode, res.Header.Get("Location"))
		}
	})

	t.Run("unauthenticated htmx request gets HX-Redirect and 204", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/private", nil)
		req.Header.Set("HX-Request", "true")
		res := do(t, app, req)
		if res.StatusCode != http.StatusNoContent || res.Header.Get("HX-Redirect") != "/login" {
			t.Fatalf("got %d HX-Redirect=%q, want 204 /login", res.StatusCode, res.Header.Get("HX-Redirect"))
		}
		if res.Header.Get("Location") != "" {
			t.Errorf("Location must be absent, got %q", res.Header.Get("Location"))
		}
	})

	// Sign in: fetch the login form, then post credentials with the
	// double-submit token.
	res := do(t, app, httptest.NewRequest(http.MethodGet, "/login", nil))
	loginCookies := res.Cookies()
	loginBody := body(t, res)
	formValue := strings.TrimPrefix(loginBody, "login-csrf:")
	if formValue == "" || formValue == loginBody {
		t.Fatalf("login page body %q carries no token", loginBody)
	}

	t.Run("login POST without the double-submit token is rejected", func(t *testing.T) {
		form := url.Values{"user": {"alice"}, "pass": {"secret"}}
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := do(t, app, req)
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("got %d, want 403", res.StatusCode)
		}
	})

	form := url.Values{"user": {"alice"}, "pass": {"secret"}, "login_csrf": {formValue}}
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for _, ck := range loginCookies {
		req.AddCookie(ck)
	}
	res = do(t, app, req)
	if res.StatusCode != http.StatusSeeOther {
		t.Fatalf("login POST got %d, want 303", res.StatusCode)
	}
	var session *http.Cookie
	for _, ck := range res.Cookies() {
		if ck.Name == cfg.EffectiveCookieName() {
			session = ck
		}
	}
	if session == nil {
		t.Fatalf("login response set no %q cookie; cookies: %v", cfg.EffectiveCookieName(), res.Cookies())
	}

	withSession := func(method, target string, bodyReader io.Reader) *http.Request {
		r := httptest.NewRequest(method, target, bodyReader)
		r.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
		return r
	}

	var csrfToken string
	t.Run("authenticated GET reaches the handler with the identity", func(t *testing.T) {
		res := do(t, app, withSession(http.MethodGet, "/private", nil))
		if res.StatusCode != http.StatusOK {
			t.Fatalf("got %d, want 200", res.StatusCode)
		}
		if got := body(t, res); got != "hello alice" {
			t.Errorf("body = %q, want %q", got, "hello alice")
		}
		csrfToken = res.Header.Get("X-Test-CSRF")
		if csrfToken == "" {
			t.Fatal("handler saw no CSRF token")
		}
	})

	t.Run("authenticated POST without a CSRF token is rejected", func(t *testing.T) {
		res := do(t, app, withSession(http.MethodPost, "/private", nil))
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("got %d, want 403", res.StatusCode)
		}
	})

	t.Run("authenticated POST with the CSRF header passes", func(t *testing.T) {
		req := withSession(http.MethodPost, "/private", nil)
		req.Header.Set("X-CSRF-Token", csrfToken)
		res := do(t, app, req)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("got %d, want 200", res.StatusCode)
		}
	})

	t.Run("authenticated POST with the urlencoded form token passes", func(t *testing.T) {
		form := url.Values{auth.DefaultCSRFFormField: {csrfToken}}
		req := withSession(http.MethodPost, "/private", strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		res := do(t, app, req)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("got %d, want 200", res.StatusCode)
		}
	})

	t.Run("authenticated POST with the multipart form token passes", func(t *testing.T) {
		// The multipart branch is glue-native extraction (fasthttp's
		// MultipartForm, not the core's PostFormValue) — the core's
		// multipart test does not cover it.
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		if err := mw.WriteField(auth.DefaultCSRFFormField, csrfToken); err != nil {
			t.Fatal(err)
		}
		if err := mw.Close(); err != nil {
			t.Fatal(err)
		}
		req := withSession(http.MethodPost, "/private", &buf)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		res := do(t, app, req)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("got %d, want 200", res.StatusCode)
		}
	})

	t.Run("query-string token is rejected", func(t *testing.T) {
		req := withSession(http.MethodPost, "/private?"+auth.DefaultCSRFFormField+"="+url.QueryEscape(csrfToken), nil)
		res := do(t, app, req)
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("got %d, want 403 — the query string must never be a token channel", res.StatusCode)
		}
	})

	t.Run("sign-out clears the session cookie via Max-Age=0", func(t *testing.T) {
		req := withSession(http.MethodPost, "/logout", nil)
		req.Header.Set("X-CSRF-Token", csrfToken)
		res := do(t, app, req)
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("got %d, want 303", res.StatusCode)
		}
		if !clearedSession(res, cfg.EffectiveCookieName()) {
			t.Errorf("no clearing Set-Cookie in %v", res.Header.Values("Set-Cookie"))
		}
		// The v3 serialization pin: MaxAge -1 must go out as Max-Age=0
		// (RFC 6265 "expire immediately") with no Expires — the issue's
		// documented fiber trivia, owned here so users never write it.
		for _, raw := range res.Header.Values("Set-Cookie") {
			if !strings.HasPrefix(raw, cfg.EffectiveCookieName()+"=") {
				continue
			}
			lower := strings.ToLower(raw)
			if !strings.Contains(lower, "max-age=0") {
				t.Errorf("clearing Set-Cookie %q lacks max-age=0", raw)
			}
			if strings.Contains(lower, "expires=") {
				t.Errorf("clearing Set-Cookie %q unexpectedly carries expires", raw)
			}
		}
	})

	t.Run("revoked session is cleared and redirected", func(t *testing.T) {
		st.sessions = map[string]string{}
		res := do(t, app, withSession(http.MethodGet, "/private", nil))
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
			t.Fatalf("got %d Location=%q, want 303 /login", res.StatusCode, res.Header.Get("Location"))
		}
		if !clearedSession(res, cfg.EffectiveCookieName()) {
			t.Errorf("no clearing Set-Cookie in %v", res.Header.Values("Set-Cookie"))
		}
	})
}

// TestCSRFFailsClosedWithoutSessionMiddleware: the fiber CSRF handler
// has its own middleware body, so the core's fail-closed test does not
// transfer — pin that a context New never touched yields a rejection,
// not a pass-through.
func TestCSRFFailsClosedWithoutSessionMiddleware(t *testing.T) {
	app := fiberfw.New()
	app.Post("/x", fiberv3auth.CSRF(), func(c fiberfw.Ctx) error {
		return c.SendString("reached")
	})
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	req.Header.Set("X-CSRF-Token", "any-token")
	res := do(t, app, req)
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("got %d, want 403", res.StatusCode)
	}
}

// TestNewPanicsOnInvalidConfig: a bad Config must fail at wiring time,
// identically to the core middleware.
func TestNewPanicsOnInvalidConfig(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("New accepted a Config with no Authenticator")
		}
	}()
	fiberv3auth.New(auth.Config[string]{LoginURL: "/login"})
}
