package auth_test

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/go-monolith/ghtmx/auth"
)

// authenticatorFunc adapts a function to the Authenticator interface for
// tests.
type authenticatorFunc[ID any] func(ctx context.Context, token string) (ID, error)

func (f authenticatorFunc[ID]) Authenticate(ctx context.Context, token string) (ID, error) {
	return f(ctx, token)
}

// TestNewSessionTokenShape: tokens must carry the full 256 bits of
// entropy the store-the-hash design assumes, and the returned hash must
// be exactly what HashSessionToken recomputes at lookup time.
func TestNewSessionTokenShape(t *testing.T) {
	token, storedHash, err := auth.NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token %q is not base64.RawURLEncoding: %v", token, err)
	}
	if len(raw) != 32 {
		t.Errorf("token decodes to %d bytes, want 32", len(raw))
	}
	if got := auth.HashSessionToken(token); got != storedHash {
		t.Errorf("storedHash = %q, HashSessionToken(token) = %q — they must agree", storedHash, got)
	}
	second, _, err := auth.NewSessionToken()
	if err != nil {
		t.Fatalf("NewSessionToken (second): %v", err)
	}
	if second == token {
		t.Error("two session tokens were identical")
	}
}

// TestHashSessionTokenKnownVector pins the hash construction (plain
// SHA-256 hex) so the stored-hash format can never drift silently and
// invalidate every persisted session.
func TestHashSessionTokenKnownVector(t *testing.T) {
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" // sha256("abc")
	if got := auth.HashSessionToken("abc"); got != want {
		t.Errorf("HashSessionToken(\"abc\") = %q, want %q", got, want)
	}
}

func TestNewCSRFToken(t *testing.T) {
	tok, err := auth.NewCSRFToken()
	if err != nil {
		t.Fatalf("NewCSRFToken: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil {
		t.Fatalf("token %q is not base64.RawURLEncoding: %v", tok, err)
	}
	if len(raw) != 32 {
		t.Errorf("token decodes to %d bytes, want 32", len(raw))
	}
	second, err := auth.NewCSRFToken()
	if err != nil {
		t.Fatalf("NewCSRFToken (second): %v", err)
	}
	if second == tok {
		t.Error("two CSRF tokens were identical")
	}
}

// TestEqualTokens: the empty-string cases exist because
// subtle.ConstantTimeCompare("", "") returns 1 — a naive constant-time
// compare would accept an empty submission against a session carrying
// no token.
func TestEqualTokens(t *testing.T) {
	tests := []struct {
		name string
		a, b string
		want bool
	}{
		{name: "both empty", a: "", b: "", want: false},
		{name: "first empty", a: "", b: "x", want: false},
		{name: "second empty", a: "x", b: "", want: false},
		{name: "equal", a: "token-value", b: "token-value", want: true},
		{name: "unequal same length", a: "aaaa", b: "aaab", want: false},
		{name: "different lengths", a: "aaaa", b: "aaaaa", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := auth.EqualTokens(tt.a, tt.b); got != tt.want {
				t.Errorf("EqualTokens(%q, %q) = %v, want %v", tt.a, tt.b, got, tt.want)
			}
		})
	}
}

// TestDerivedCSRFTokenIsDomainSeparated: the per-session CSRF token the
// middleware derives must differ from both the raw session token and
// the stored hash — otherwise a database disclosure (which holds the
// hash) would also hand out live CSRF tokens.
func TestDerivedCSRFTokenIsDomainSeparated(t *testing.T) {
	cfg := auth.Config[string]{
		Authenticator: authenticatorFunc[string](func(_ context.Context, _ string) (string, error) {
			return "user-1", nil
		}),
		LoginURL: "/login",
	}
	const sessionToken = "some-session-token"
	ctx, _, err := auth.AuthenticateToken(context.Background(), cfg, sessionToken)
	if err != nil {
		t.Fatalf("AuthenticateToken: %v", err)
	}
	derived, ok := auth.CSRFTokenFrom(ctx)
	if !ok || derived == "" {
		t.Fatalf("CSRFTokenFrom: ok=%v token=%q, want a derived token", ok, derived)
	}
	if derived == sessionToken {
		t.Error("derived CSRF token equals the session token")
	}
	if derived == auth.HashSessionToken(sessionToken) {
		t.Error("derived CSRF token equals the stored session hash — no domain separation")
	}
}
