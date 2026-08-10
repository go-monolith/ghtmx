package auth_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-monolith/ghtmx/auth"
)

// sessionConfig builds a Config whose Authenticator accepts exactly
// "live-token" as the identity "user-1" and answers everything else
// with fail(token's) error.
func sessionConfig(fail error) auth.Config[string] {
	return auth.Config[string]{
		Authenticator: authenticatorFunc[string](func(_ context.Context, token string) (string, error) {
			if token == "live-token" {
				return "user-1", nil
			}
			return "", fail
		}),
		LoginURL: "/login",
	}
}

// TestMiddlewareOutcomes drives the full outcome matrix from the issue:
// missing cookie redirects (htmx-aware), a dead session clears the
// cookie first, a store outage is a 500 and never a login redirect, and
// success installs identity and CSRF token.
func TestMiddlewareOutcomes(t *testing.T) {
	tests := []struct {
		name         string
		cookie       string // session cookie value; "" = no cookie
		htmx         bool
		authErr      error
		wantStatus   int
		wantLocation string // Location header; "" = must be absent
		wantHXRed    string // HX-Redirect header; "" = must be absent
		wantCleared  bool   // response carries the clearing Set-Cookie
		wantHandler  bool   // inner handler ran
	}{
		{
			name:         "no cookie, browser navigation",
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/login",
		},
		{
			name:       "no cookie, htmx request",
			htmx:       true,
			wantStatus: http.StatusNoContent,
			wantHXRed:  "/login",
		},
		{
			name:         "dead session clears the cookie",
			cookie:       "revoked-token",
			authErr:      fmt.Errorf("session gone: %w", auth.ErrUnauthorized),
			wantStatus:   http.StatusSeeOther,
			wantLocation: "/login",
			wantCleared:  true,
		},
		{
			name:        "dead session, htmx request",
			cookie:      "revoked-token",
			htmx:        true,
			authErr:     fmt.Errorf("session gone: %w", auth.ErrUnauthorized),
			wantStatus:  http.StatusNoContent,
			wantHXRed:   "/login",
			wantCleared: true,
		},
		{
			name:       "auth-store outage is a 500, not a login redirect",
			cookie:     "any-token",
			authErr:    errors.New("store down"),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:        "live session runs the handler",
			cookie:      "live-token",
			wantStatus:  http.StatusOK,
			wantHandler: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := sessionConfig(tt.authErr)
			handlerRan := false
			h := auth.Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				handlerRan = true
				id, ok := auth.IdentityFrom[string](r.Context())
				if !ok || id != "user-1" {
					t.Errorf("IdentityFrom = (%q, %v), want (user-1, true)", id, ok)
				}
				if tok, ok := auth.CSRFTokenFrom(r.Context()); !ok || tok == "" {
					t.Errorf("CSRFTokenFrom = (%q, %v), want a token", tok, ok)
				}
				w.WriteHeader(http.StatusOK)
			}))

			r := httptest.NewRequest(http.MethodGet, "/private", nil)
			if tt.cookie != "" {
				r.AddCookie(&http.Cookie{Name: cfg.EffectiveCookieName(), Value: tt.cookie})
			}
			if tt.htmx {
				r.Header.Set("HX-Request", "true")
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			res := rec.Result()
			if res.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", res.StatusCode, tt.wantStatus)
			}
			if got := res.Header.Get("Location"); got != tt.wantLocation {
				t.Errorf("Location = %q, want %q", got, tt.wantLocation)
			}
			if got := res.Header.Get("HX-Redirect"); got != tt.wantHXRed {
				t.Errorf("HX-Redirect = %q, want %q", got, tt.wantHXRed)
			}
			cleared := false
			for _, ck := range res.Cookies() {
				if ck.Name == cfg.EffectiveCookieName() && ck.Value == "" && ck.MaxAge == -1 {
					cleared = true
				}
			}
			if cleared != tt.wantCleared {
				t.Errorf("clearing Set-Cookie present = %v, want %v (cookies: %v)", cleared, tt.wantCleared, res.Cookies())
			}
			if handlerRan != tt.wantHandler {
				t.Errorf("handler ran = %v, want %v", handlerRan, tt.wantHandler)
			}
		})
	}
}

// TestMiddlewarePanicsOnInvalidConfig: a bad Config is a wiring bug and
// must fail at process start, not serve a single request.
func TestMiddlewarePanicsOnInvalidConfig(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Middleware accepted a Config with no Authenticator")
		}
	}()
	auth.Middleware(auth.Config[string]{LoginURL: "/login"})
}

// TestIdentityFromWrongTypeParam: asking for the wrong identity type
// yields false — indistinguishable from missing middleware, which is
// exactly why callers must refuse the request on false.
func TestIdentityFromWrongTypeParam(t *testing.T) {
	ctx := auth.ContextWithIdentity(context.Background(), "user-1")
	if _, ok := auth.IdentityFrom[int](ctx); ok {
		t.Error("IdentityFrom[int] succeeded against a string identity")
	}
	if id, ok := auth.IdentityFrom[string](ctx); !ok || id != "user-1" {
		t.Errorf("IdentityFrom[string] = (%q, %v), want (user-1, true)", id, ok)
	}
}

// TestRedirectToLoginHeaderOrdering pins that HX-Redirect survives the
// 204: net/http drops headers set after WriteHeader, so the helper must
// set the header first.
func TestRedirectToLoginHeaderOrdering(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/private", nil)
	r.Header.Set("HX-Request", "true")
	rec := httptest.NewRecorder()
	auth.RedirectToLogin(rec, r, "/login")
	res := rec.Result()
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", res.StatusCode)
	}
	if got := res.Header.Get("HX-Redirect"); got != "/login" {
		t.Errorf("HX-Redirect = %q, want /login", got)
	}
}
