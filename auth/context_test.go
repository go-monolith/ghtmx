package auth_test

import (
	"context"
	"errors"
	"testing"

	"github.com/go-monolith/ghtmx/auth"
)

// TestContextRoundTrips: the exported injectors exist so handler tests
// can build authenticated contexts without the middleware; they must
// round-trip exactly, and absence must read as false.
func TestContextRoundTrips(t *testing.T) {
	base := context.Background()

	if _, ok := auth.IdentityFrom[string](base); ok {
		t.Error("IdentityFrom on an empty context reported ok")
	}
	if _, ok := auth.CSRFTokenFrom(base); ok {
		t.Error("CSRFTokenFrom on an empty context reported ok")
	}

	ctx := auth.ContextWithIdentity(base, "user-1")
	if id, ok := auth.IdentityFrom[string](ctx); !ok || id != "user-1" {
		t.Errorf("IdentityFrom = (%q, %v), want (user-1, true)", id, ok)
	}

	ctx = auth.ContextWithCSRFToken(base, "tok")
	if tok, ok := auth.CSRFTokenFrom(ctx); !ok || tok != "tok" {
		t.Errorf("CSRFTokenFrom = (%q, %v), want (tok, true)", tok, ok)
	}
}

// TestAuthenticateToken: the shared engine must install both context
// values on success, pass the Authenticator's error through unwrapped
// (callers branch on ErrUnauthorized), and refuse a nil Authenticator.
func TestAuthenticateToken(t *testing.T) {
	t.Run("success installs identity and csrf token", func(t *testing.T) {
		cfg := auth.Config[int]{
			Authenticator: authenticatorFunc[int](func(_ context.Context, token string) (int, error) {
				if token != "tok" {
					t.Errorf("Authenticate received %q, want tok", token)
				}
				return 42, nil
			}),
			LoginURL: "/login",
		}
		ctx, id, err := auth.AuthenticateToken(context.Background(), cfg, "tok")
		if err != nil {
			t.Fatalf("AuthenticateToken: %v", err)
		}
		if id != 42 {
			t.Errorf("id = %d, want 42", id)
		}
		if got, ok := auth.IdentityFrom[int](ctx); !ok || got != 42 {
			t.Errorf("IdentityFrom = (%d, %v), want (42, true)", got, ok)
		}
		if tok, ok := auth.CSRFTokenFrom(ctx); !ok || tok == "" {
			t.Errorf("CSRFTokenFrom = (%q, %v), want a derived token", tok, ok)
		}
	})

	t.Run("error passes through unwrapped", func(t *testing.T) {
		wrapped := errors.Join(auth.ErrUnauthorized)
		cfg := auth.Config[int]{
			Authenticator: authenticatorFunc[int](func(_ context.Context, _ string) (int, error) {
				return 0, wrapped
			}),
			LoginURL: "/login",
		}
		ctx, _, err := auth.AuthenticateToken(context.Background(), cfg, "tok")
		if !errors.Is(err, auth.ErrUnauthorized) {
			t.Fatalf("err = %v, want it to wrap ErrUnauthorized", err)
		}
		if _, ok := auth.IdentityFrom[int](ctx); ok {
			t.Error("identity installed despite an authentication error")
		}
	})

	t.Run("nil authenticator errors", func(t *testing.T) {
		_, _, err := auth.AuthenticateToken(context.Background(), auth.Config[int]{LoginURL: "/login"}, "tok")
		if err == nil {
			t.Fatal("AuthenticateToken accepted a nil Authenticator")
		}
	})
}
