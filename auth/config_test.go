package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-monolith/ghtmx/auth"
)

// okAuthenticator is a minimal valid Authenticator for Config tests that
// never authenticate anything.
var okAuthenticator = authenticatorFunc[string](func(_ context.Context, _ string) (string, error) {
	return "", nil
})

func validConfig() auth.Config[string] {
	return auth.Config[string]{Authenticator: okAuthenticator, LoginURL: "/login"}
}

// TestValidate: the middleware constructors panic on whatever this
// rejects, so each row documents a wiring bug that must be caught at
// process start rather than mid-request.
func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*auth.Config[string])
		wantErr string // substring; "" means valid
	}{
		{name: "zero-value optionals are valid", mutate: func(c *auth.Config[string]) {}},
		{name: "explicit fields are valid", mutate: func(c *auth.Config[string]) {
			c.CookieName = "sess"
			c.CookiePath = "/admin"
			c.SameSite = auth.SameSiteLax
			c.TTL = time.Hour
		}},
		{name: "nil authenticator", mutate: func(c *auth.Config[string]) {
			c.Authenticator = nil
		}, wantErr: "Authenticator"},
		{name: "empty login URL", mutate: func(c *auth.Config[string]) {
			c.LoginURL = ""
		}, wantErr: "LoginURL"},
		{name: "negative TTL", mutate: func(c *auth.Config[string]) {
			c.TTL = -time.Second
		}, wantErr: "TTL"},
		{name: "out-of-range SameSite", mutate: func(c *auth.Config[string]) {
			c.SameSite = auth.SameSite(99)
		}, wantErr: "SameSite"},
		{name: "relative cookie path", mutate: func(c *auth.Config[string]) {
			c.CookiePath = "admin"
		}, wantErr: "CookiePath"},
		{name: "hand-written __Host- prefix", mutate: func(c *auth.Config[string]) {
			c.CookieName = "__Host-sess"
		}, wantErr: "prefix"},
		{name: "hand-written __Secure- prefix", mutate: func(c *auth.Config[string]) {
			c.CookieName = "__Secure-sess"
		}, wantErr: "prefix"},
		{name: "invalid cookie name", mutate: func(c *auth.Config[string]) {
			c.CookieName = "bad;name"
		}, wantErr: "valid cookie name"},
		{name: "invalid cookie path bytes", mutate: func(c *auth.Config[string]) {
			// net/http would silently strip the ';' at serialization
			// time, sending the cookie under a different path than
			// configured — Validate must catch it at wiring time.
			c.CookiePath = "/admin;evil"
		}, wantErr: "valid cookie path"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want an error mentioning %q", err, tt.wantErr)
			}
		})
	}
}

// TestEffectiveCookieName pins the __Host- rule: applied exactly when
// the cookie is site-wide and Secure, and shared with the login-CSRF
// cookie name so set, clear, and read can never disagree.
func TestEffectiveCookieName(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*auth.Config[string])
		want      string
		wantLogin string
	}{
		{
			name:      "defaults get the prefix",
			mutate:    func(c *auth.Config[string]) {},
			want:      "__Host-ghtmx_session",
			wantLogin: "__Host-ghtmx_session_login_csrf",
		},
		{
			name: "insecure drops the prefix",
			mutate: func(c *auth.Config[string]) {
				c.Insecure = true
			},
			want:      "ghtmx_session",
			wantLogin: "ghtmx_session_login_csrf",
		},
		{
			name: "sub-app path drops the prefix",
			mutate: func(c *auth.Config[string]) {
				c.CookiePath = "/admin"
			},
			want:      "ghtmx_session",
			wantLogin: "ghtmx_session_login_csrf",
		},
		{
			name: "custom name keeps the prefix rule",
			mutate: func(c *auth.Config[string]) {
				c.CookieName = "portal"
			},
			want:      "__Host-portal",
			wantLogin: "__Host-portal_login_csrf",
		},
		{
			name: "explicit root path is site-wide",
			mutate: func(c *auth.Config[string]) {
				c.CookiePath = "/"
			},
			want:      "__Host-ghtmx_session",
			wantLogin: "__Host-ghtmx_session_login_csrf",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.mutate(&cfg)
			if got := cfg.EffectiveCookieName(); got != tt.want {
				t.Errorf("EffectiveCookieName() = %q, want %q", got, tt.want)
			}
			if got := cfg.LoginCSRFCookieName(); got != tt.wantLogin {
				t.Errorf("LoginCSRFCookieName() = %q, want %q", got, tt.wantLogin)
			}
		})
	}
}
