package auth

import "net/http"

// SetSessionCookie sets the session cookie after a successful sign-in.
// token is the raw value from [NewSessionToken] — the application
// stores only the hash, the browser only the token.
func SetSessionCookie[ID any](w http.ResponseWriter, cfg Config[ID], token string) {
	http.SetCookie(w, cfg.SessionCookie(token))
}

// ClearSessionCookie deletes the session cookie — on sign-out, or when
// the middleware learns the session is dead. The middleware calls it
// itself on [ErrUnauthorized]; applications call it from their sign-out
// handler.
func ClearSessionCookie[ID any](w http.ResponseWriter, cfg Config[ID]) {
	http.SetCookie(w, cfg.SessionClearingCookie())
}

// SetLoginCSRFCookie mints the login form's pre-session double-submit
// token, sets it as a cookie, and returns the value the form embeds in
// a hidden field. SameSite does not protect the login form — it governs
// whether an existing cookie is sent, not whether an unauthenticated
// POST is accepted — so without this token an attacker can submit their
// own credentials from the victim's browser, after which the victim
// unknowingly works inside the attacker's account. Call it from the
// GET handler that renders the login page, and check the submission
// with [ValidLoginCSRF] before verifying credentials.
func SetLoginCSRFCookie[ID any](w http.ResponseWriter, cfg Config[ID]) (formValue string, err error) {
	tok, err := NewCSRFToken()
	if err != nil {
		return "", err
	}
	http.SetCookie(w, cfg.LoginCSRFCookie(tok))
	return tok, nil
}

// ValidLoginCSRF reports whether the login form submission carries the
// token [SetLoginCSRFCookie] set, compared in constant time. False —
// missing cookie, missing submission, or mismatch — means the login
// POST must be rejected before credentials are even looked at.
func ValidLoginCSRF[ID any](r *http.Request, cfg Config[ID], submitted string) bool {
	ck, err := r.Cookie(cfg.LoginCSRFCookieName())
	if err != nil {
		return false
	}
	return EqualTokens(ck.Value, submitted)
}
