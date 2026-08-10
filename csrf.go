package ghtmx

import "encoding/json"

// DefaultCSRFHeaderName is the request header CSRFHeader uses unless the
// application names another one.
const DefaultCSRFHeaderName = "X-CSRF-Token"

// CSRFHeader returns an hx-headers attribute value attaching the
// application's CSRF token to every request the element issues (FR-092):
//
//	<form hx-post={ handlers.CreateUser } hx-headers={ ghtmx.CSRFHeader(token) }>
//
// The same attribute works for all state-changing verbs (hx-post, hx-put,
// hx-patch, hx-delete); placed on a common ancestor it is inherited by
// every element below it. The token is supplied by the application — the
// engine neither generates nor validates tokens — and the value is JSON,
// escaped like any attribute value when rendered, so no hand-written
// header plumbing is involved.
//
// Pass headerName to use a header other than X-CSRF-Token; only the first
// name is used. The name must be a valid HTTP header token and the token
// value Latin-1-safe (real CSRF tokens are ASCII): the browser rejects
// anything else when htmx calls setRequestHeader at request time.
//
// Token generation and request verification live in the auth package
// (secure cookie session middleware): its session middleware derives the
// per-session token handlers read with auth.CSRFTokenFrom, and its CSRF
// middleware verifies this header on unsafe methods.
func CSRFHeader(token string, headerName ...string) string {
	name := DefaultCSRFHeaderName
	if len(headerName) > 0 && headerName[0] != "" {
		name = headerName[0]
	}
	// Two-key marshal would sort; a single-pair object needs no ordering,
	// and json.Marshal escapes both strings safely.
	out, err := json.Marshal(map[string]string{name: token})
	if err != nil {
		// Marshaling a map[string]string cannot fail.
		return "{}"
	}
	return string(out)
}
