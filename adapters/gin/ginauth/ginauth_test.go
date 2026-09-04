package ginauth_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	ginfw "github.com/gin-gonic/gin"

	"github.com/go-monolith/ghtmx/adapters/gin/ginauth"
	"github.com/go-monolith/ghtmx/auth"
)

func TestMain(m *testing.M) {
	// SetMode is process-global; the test binary owns the process.
	ginfw.SetMode(ginfw.ReleaseMode)
	os.Exit(m.Run())
}

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
func newApp() (*ginfw.Engine, *store, auth.Config[string]) {
	st := &store{sessions: map[string]string{}}
	cfg := auth.Config[string]{Authenticator: st, LoginURL: "/login"}

	r := ginfw.New()
	r.GET("/login", func(c *ginfw.Context) {
		v, err := ginauth.SetLoginCSRFCookie(c, cfg)
		if err != nil {
			c.String(http.StatusInternalServerError, "login csrf: %v", err)
			return
		}
		c.String(http.StatusOK, "login-csrf:%s", v)
	})
	r.POST("/login", func(c *ginfw.Context) {
		if !ginauth.ValidLoginCSRF(c, cfg, c.PostForm("login_csrf")) {
			c.String(http.StatusForbidden, "bad login csrf")
			return
		}
		if c.PostForm("user") != "alice" || c.PostForm("pass") != "secret" {
			c.String(http.StatusUnauthorized, "bad credentials")
			return
		}
		token, hash, err := auth.NewSessionToken()
		if err != nil {
			c.String(http.StatusInternalServerError, "token: %v", err)
			return
		}
		st.sessions[hash] = "alice"
		ginauth.SetSessionCookie(c, cfg, token)
		c.Redirect(http.StatusSeeOther, "/private")
	})

	priv := r.Group("/", ginauth.New(cfg), ginauth.CSRF())
	priv.GET("/private", func(c *ginfw.Context) {
		id, ok := ginauth.IdentityFrom[string](c)
		if !ok {
			c.String(http.StatusInternalServerError, "identity missing")
			return
		}
		tok, _ := auth.CSRFTokenFrom(c.Request.Context())
		c.Header("X-Test-CSRF", tok)
		c.String(http.StatusOK, "hello %s", id)
	})
	priv.POST("/private", func(c *ginfw.Context) {
		c.String(http.StatusOK, "posted")
	})
	priv.POST("/logout", func(c *ginfw.Context) {
		ginauth.ClearSessionCookie(c, cfg)
		c.Redirect(http.StatusSeeOther, "/login")
	})
	return r, st, cfg
}

func do(t *testing.T, h http.Handler, req *http.Request) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

// TestAuthFlow drives the full life of a session through the gin glue:
// unauthenticated redirects (htmx-aware), login CSRF, sign-in, CSRF on
// unsafe methods, sign-out, and revocation.
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

	withSession := func(method, target string) *http.Request {
		r := httptest.NewRequest(method, target, nil)
		r.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
		return r
	}

	var csrfToken string
	t.Run("authenticated GET reaches the handler with the identity", func(t *testing.T) {
		res := do(t, app, withSession(http.MethodGet, "/private"))
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
		res := do(t, app, withSession(http.MethodPost, "/private"))
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("got %d, want 403", res.StatusCode)
		}
	})

	t.Run("authenticated POST with the CSRF header passes", func(t *testing.T) {
		req := withSession(http.MethodPost, "/private")
		req.Header.Set("X-CSRF-Token", csrfToken)
		res := do(t, app, req)
		if res.StatusCode != http.StatusOK {
			t.Fatalf("got %d, want 200", res.StatusCode)
		}
	})

	t.Run("sign-out clears the session cookie", func(t *testing.T) {
		req := withSession(http.MethodPost, "/logout")
		req.Header.Set("X-CSRF-Token", csrfToken)
		res := do(t, app, req)
		if res.StatusCode != http.StatusSeeOther {
			t.Fatalf("got %d, want 303", res.StatusCode)
		}
		cleared := false
		for _, ck := range res.Cookies() {
			if ck.Name == cfg.EffectiveCookieName() && ck.Value == "" && ck.MaxAge == -1 {
				cleared = true
			}
		}
		if !cleared {
			t.Errorf("no clearing Set-Cookie in %v", res.Cookies())
		}
	})

	t.Run("revoked session is cleared and redirected", func(t *testing.T) {
		st.sessions = map[string]string{}
		res := do(t, app, withSession(http.MethodGet, "/private"))
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
			t.Fatalf("got %d Location=%q, want 303 /login", res.StatusCode, res.Header.Get("Location"))
		}
		cleared := false
		for _, ck := range res.Cookies() {
			if ck.Name == cfg.EffectiveCookieName() && ck.Value == "" && ck.MaxAge == -1 {
				cleared = true
			}
		}
		if !cleared {
			t.Errorf("no clearing Set-Cookie in %v", res.Cookies())
		}
	})
}

// TestCSRFOnRejectHook: the core's option type must work through this
// adapter unchanged, reporting the same request path every other
// adapter does — not gin's route pattern.
func TestCSRFOnRejectHook(t *testing.T) {
	var got []auth.CSRFRejection
	r := ginfw.New()
	r.POST("/items/:id", ginauth.CSRF(auth.WithOnReject(
		func(_ context.Context, rej auth.CSRFRejection) { got = append(got, rej) },
	)), func(c *ginfw.Context) { c.String(http.StatusOK, "reached") })

	req := httptest.NewRequest(http.MethodPost, "/items/42?"+auth.DefaultCSRFFormField+"=x", nil)
	res := do(t, r, req)
	if res.StatusCode != http.StatusForbidden {
		t.Fatalf("got %d, want 403 — the hook must not change the outcome", res.StatusCode)
	}
	if len(got) != 1 {
		t.Fatalf("hook fired %d times, want exactly 1", len(got))
	}
	if got[0].Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", got[0].Method)
	}
	if got[0].Path != "/items/42" {
		t.Errorf("Path = %q, want /items/42 — the request path, not the route pattern or the query string", got[0].Path)
	}
	if !errors.Is(got[0].Err, auth.ErrCSRF) {
		t.Errorf("Err %v does not wrap ErrCSRF", got[0].Err)
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
	ginauth.New(auth.Config[string]{LoginURL: "/login"})
}

// TestCSRFSafeMethods: the safe-list an application chooses reaches the
// glue, so the QUERY exemption htmx 4 brought with it can be declined
// here exactly as it can behind the core middleware (issue #45).
func TestCSRFSafeMethods(t *testing.T) {
	app := func(opts ...auth.CSRFOption) *ginfw.Engine {
		r := ginfw.New()
		r.Handle(auth.MethodQuery, "/rows", ginauth.CSRF(opts...),
			func(c *ginfw.Context) { c.String(http.StatusOK, "reached") })
		return r
	}

	t.Run("QUERY passes with no options", func(t *testing.T) {
		res := do(t, app(), httptest.NewRequest(auth.MethodQuery, "/rows", nil))
		if res.StatusCode != http.StatusOK {
			t.Fatalf("got %d, want 200 — QUERY is exempt by default", res.StatusCode)
		}
	})

	t.Run("QUERY is rejected once the safe-list drops it", func(t *testing.T) {
		htmx2 := auth.WithSafeMethods(http.MethodGet, http.MethodHead, http.MethodOptions)
		res := do(t, app(htmx2), httptest.NewRequest(auth.MethodQuery, "/rows", nil))
		if res.StatusCode != http.StatusForbidden {
			t.Fatalf("got %d, want 403 — the narrowed list must reach the glue", res.StatusCode)
		}
	})
}
