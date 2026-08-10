package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-monolith/ghtmx/auth"
)

// TestSessionCookieAttributes: every row asserts on the serialized
// Set-Cookie string, because that is what the browser sees — struct
// fields can look right while the wire format is wrong.
func TestSessionCookieAttributes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*auth.Config[string])
		want   []string // substrings of the serialized cookie
		forbid []string
	}{
		{
			name:   "defaults",
			mutate: func(c *auth.Config[string]) {},
			want:   []string{"__Host-ghtmx_session=tok", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"},
			forbid: []string{"Domain=", "Max-Age", "Expires"},
		},
		{
			name: "lax and TTL",
			mutate: func(c *auth.Config[string]) {
				c.SameSite = auth.SameSiteLax
				c.TTL = time.Hour
			},
			want:   []string{"SameSite=Lax", "Max-Age=3600"},
			forbid: []string{"Domain="},
		},
		{
			name: "sub-second TTL rounds up, never to a session cookie",
			mutate: func(c *auth.Config[string]) {
				c.TTL = 1500 * time.Millisecond
			},
			want:   []string{"Max-Age=2"},
			forbid: []string{"Domain="},
		},
		{
			name: "insecure dev mode",
			mutate: func(c *auth.Config[string]) {
				c.Insecure = true
			},
			want:   []string{"ghtmx_session=tok", "HttpOnly", "SameSite=Strict"},
			forbid: []string{"Secure", "__Host-", "Domain="},
		},
		{
			name: "sub-app path",
			mutate: func(c *auth.Config[string]) {
				c.CookiePath = "/admin"
			},
			want:   []string{"ghtmx_session=tok", "Path=/admin", "Secure"},
			forbid: []string{"__Host-", "Domain="},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			s := cfg.SessionCookie("tok").String()
			for _, w := range tt.want {
				if !strings.Contains(s, w) {
					t.Errorf("cookie %q lacks %q", s, w)
				}
			}
			for _, f := range tt.forbid {
				if strings.Contains(s, f) {
					t.Errorf("cookie %q must not contain %q", s, f)
				}
			}
		})
	}
}

// TestSessionClearingCookie: deletion must serialize as Max-Age=0 (RFC
// 6265 "expire immediately") under the exact same name and path as the
// live cookie, or the browser treats them as different cookies and
// keeps the session.
func TestSessionClearingCookie(t *testing.T) {
	cfg := validConfig()
	live := cfg.SessionCookie("tok")
	clear := cfg.SessionClearingCookie()
	if live.Name != clear.Name || live.Path != clear.Path {
		t.Errorf("clearing cookie name/path (%q, %q) differ from live cookie (%q, %q)",
			clear.Name, clear.Path, live.Name, live.Path)
	}
	s := clear.String()
	if !strings.Contains(s, "Max-Age=0") {
		t.Errorf("clearing cookie %q lacks Max-Age=0", s)
	}
	if clear.Value != "" {
		t.Errorf("clearing cookie value = %q, want empty", clear.Value)
	}
}

// TestSetAndClearSessionCookie exercises the ResponseWriter-facing
// wrappers end to end.
func TestSetAndClearSessionCookie(t *testing.T) {
	cfg := validConfig()

	rec := httptest.NewRecorder()
	auth.SetSessionCookie(rec, cfg, "tok")
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "__Host-ghtmx_session" || cookies[0].Value != "tok" {
		t.Fatalf("SetSessionCookie wrote %+v, want one __Host-ghtmx_session=tok", cookies)
	}

	rec = httptest.NewRecorder()
	auth.ClearSessionCookie(rec, cfg)
	cookies = rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value != "" || cookies[0].MaxAge != -1 {
		t.Fatalf("ClearSessionCookie wrote %+v, want one empty-value MaxAge=-1 cookie", cookies)
	}
}

// TestLoginCSRFRoundTrip: the pre-session double-submit flow — the
// value returned for the form must be the value set in the cookie, and
// validation must fail closed on every partial state.
func TestLoginCSRFRoundTrip(t *testing.T) {
	cfg := validConfig()

	rec := httptest.NewRecorder()
	formValue, err := auth.SetLoginCSRFCookie(rec, cfg)
	if err != nil {
		t.Fatalf("SetLoginCSRFCookie: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	ck := cookies[0]
	if ck.Name != cfg.LoginCSRFCookieName() {
		t.Errorf("cookie name = %q, want %q", ck.Name, cfg.LoginCSRFCookieName())
	}
	if ck.Value != formValue {
		t.Errorf("cookie value %q differs from returned form value %q", ck.Value, formValue)
	}
	if !ck.HttpOnly {
		t.Error("login CSRF cookie is not HttpOnly")
	}
	if s := ck.String(); strings.Contains(s, "Max-Age") || strings.Contains(s, "Expires") {
		t.Errorf("login CSRF cookie %q should be session-lifetime", s)
	}

	withCookie := func(value string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/login", nil)
		if value != "" {
			r.AddCookie(&http.Cookie{Name: cfg.LoginCSRFCookieName(), Value: value})
		}
		return r
	}
	if !auth.ValidLoginCSRF(withCookie(formValue), cfg, formValue) {
		t.Error("matching cookie and submission rejected")
	}
	if auth.ValidLoginCSRF(withCookie(formValue), cfg, "wrong") {
		t.Error("mismatched submission accepted")
	}
	if auth.ValidLoginCSRF(withCookie(formValue), cfg, "") {
		t.Error("empty submission accepted")
	}
	if auth.ValidLoginCSRF(withCookie(""), cfg, formValue) {
		t.Error("missing cookie accepted")
	}
}
