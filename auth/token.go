package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
)

// ErrUnauthorized is the sentinel an [Authenticator] wraps when a
// session token is unusable — expired, revoked, or unknown. The
// middleware reacts by clearing the session cookie (so the browser
// stops resending a value that can never work) and redirecting to the
// login page. Any other error is treated as an application failure and
// answered with a plain 500: an auth-store outage must not degrade into
// "please sign in".
var ErrUnauthorized = errors.New("auth: unauthorized")

// Authenticator is the one interface the application implements. It
// resolves a session token to the identity behind it. Everything else —
// session storage, lookup, server-side expiry, revocation, user
// records — is the application's business, behind this method.
type Authenticator[ID any] interface {
	// Authenticate returns the identity behind token, or an error
	// wrapping [ErrUnauthorized] when the session is unusable. This is
	// where real session expiry and revocation are enforced; the
	// cookie's TTL only advises the browser.
	Authenticate(ctx context.Context, token string) (ID, error)
}

// tokenBytes is the entropy of every token this package mints: 256 bits
// of crypto/rand output.
const tokenBytes = 32

// NewSessionToken mints an opaque session token for a fresh sign-in and
// the hash to store server-side. Only storedHash — the SHA-256 hex of
// the token, as [HashSessionToken] computes it — is ever persisted, so
// a database disclosure (backup, dump, read-only SQL injection) cannot
// be replayed as a live session. Plain SHA-256 is correct here: the
// input is 256 bits of uniform randomness, so a slow KDF would buy
// nothing but per-request latency.
func NewSessionToken() (token, storedHash string, err error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	token = base64.RawURLEncoding.EncodeToString(b)
	return token, HashSessionToken(token), nil
}

// HashSessionToken returns the SHA-256 hex digest of token — the value
// [NewSessionToken] returns as storedHash, recomputed at lookup time so
// the application's store never sees the token itself.
func HashSessionToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewCSRFToken mints a random token for the pre-session double-submit
// cookie the login form uses ([SetLoginCSRFCookie]). Per-session CSRF
// tokens are not minted with this — they are derived from the session
// token by the middleware and read with [CSRFTokenFrom].
func NewCSRFToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// EqualTokens compares two tokens in constant time. It rejects empty
// strings explicitly: subtle.ConstantTimeCompare("", "") returns 1, so
// a naive constant-time compare would accept an empty submission
// against a session that carries no token.
func EqualTokens(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// csrfDomainSeparator keeps the derived per-session CSRF token disjoint
// from the stored session hash: sha256(token) is what the application
// persists, sha256(separator+token) is what the browser echoes back, and
// neither is computable from the other without the token itself.
const csrfDomainSeparator = "ghtmx auth csrf v1\x00"

// csrfTokenForSession derives the per-session synchronizer CSRF token
// from the raw session token. Deterministic derivation means the token
// needs no storage and no extra cookie, is bound to the session, and
// rotates with it. This is the single derivation site; glue packages
// reach it through [AuthenticateToken].
func csrfTokenForSession(sessionToken string) string {
	sum := sha256.Sum256([]byte(csrfDomainSeparator + sessionToken))
	return hex.EncodeToString(sum[:])
}
