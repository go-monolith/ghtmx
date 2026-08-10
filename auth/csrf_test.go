package auth_test

import (
	"bytes"
	"context"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/auth"
)

// csrfChain builds session middleware + CSRF middleware around a
// 200-answering handler, and returns the chain plus the CSRF token a
// live session yields — obtained the way an application would, from the
// request context via a probe request.
func csrfChain(t *testing.T) (http.Handler, string) {
	t.Helper()
	cfg := sessionConfig(auth.ErrUnauthorized)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	chain := auth.Middleware(cfg)(auth.CSRF()(ok))

	var token string
	probe := auth.Middleware(cfg)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, _ = auth.CSRFTokenFrom(r.Context())
	}))
	r := httptest.NewRequest(http.MethodGet, "/probe", nil)
	r.AddCookie(&http.Cookie{Name: cfg.EffectiveCookieName(), Value: "live-token"})
	probe.ServeHTTP(httptest.NewRecorder(), r)
	if token == "" {
		t.Fatal("probe request yielded no CSRF token")
	}
	return chain, token
}

func sessionRequest(method, target string, body *strings.Reader) *http.Request {
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, body)
	}
	r.AddCookie(&http.Cookie{Name: "__Host-ghtmx_session", Value: "live-token"})
	return r
}

// TestCSRFMiddleware: safe methods pass untouched; unsafe methods need
// the session's token in the header or a form field; the query string
// is never an accepted channel (tokens must not leak into logs and
// Referer headers).
func TestCSRFMiddleware(t *testing.T) {
	chain, token := csrfChain(t)

	tests := []struct {
		name string
		req  func() *http.Request
		want int
	}{
		{
			name: "GET passes without a token",
			req:  func() *http.Request { return sessionRequest(http.MethodGet, "/x", nil) },
			want: http.StatusOK,
		},
		{
			name: "HEAD passes without a token",
			req:  func() *http.Request { return sessionRequest(http.MethodHead, "/x", nil) },
			want: http.StatusOK,
		},
		{
			name: "OPTIONS passes without a token",
			req:  func() *http.Request { return sessionRequest(http.MethodOptions, "/x", nil) },
			want: http.StatusOK,
		},
		{
			name: "POST without a token is rejected",
			req:  func() *http.Request { return sessionRequest(http.MethodPost, "/x", nil) },
			want: http.StatusForbidden,
		},
		{
			name: "POST with the header token passes",
			req: func() *http.Request {
				r := sessionRequest(http.MethodPost, "/x", nil)
				r.Header.Set(ghtmx.DefaultCSRFHeaderName, token)
				return r
			},
			want: http.StatusOK,
		},
		{
			name: "POST with a wrong header token is rejected",
			req: func() *http.Request {
				r := sessionRequest(http.MethodPost, "/x", nil)
				r.Header.Set(ghtmx.DefaultCSRFHeaderName, "wrong")
				return r
			},
			want: http.StatusForbidden,
		},
		{
			name: "POST with the urlencoded form token passes",
			req: func() *http.Request {
				form := url.Values{auth.DefaultCSRFFormField: {token}}
				r := sessionRequest(http.MethodPost, "/x", strings.NewReader(form.Encode()))
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				return r
			},
			want: http.StatusOK,
		},
		{
			name: "DELETE with the header token passes",
			req: func() *http.Request {
				r := sessionRequest(http.MethodDelete, "/x", nil)
				r.Header.Set(ghtmx.DefaultCSRFHeaderName, token)
				return r
			},
			want: http.StatusOK,
		},
		{
			name: "query-string token is rejected",
			req: func() *http.Request {
				return sessionRequest(http.MethodPost, "/x?"+auth.DefaultCSRFFormField+"="+url.QueryEscape(token), nil)
			},
			want: http.StatusForbidden,
		},
		{
			name: "form token without a form content type is rejected",
			req: func() *http.Request {
				form := url.Values{auth.DefaultCSRFFormField: {token}}
				return sessionRequest(http.MethodPost, "/x", strings.NewReader(form.Encode()))
			},
			want: http.StatusForbidden,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, tt.req())
			if got := rec.Result().StatusCode; got != tt.want {
				t.Errorf("status = %d, want %d", got, tt.want)
			}
		})
	}
}

// TestCSRFMultipartForm: the token must also be found in multipart
// bodies — file-upload forms are still state-changing forms.
func TestCSRFMultipartForm(t *testing.T) {
	chain, token := csrfChain(t)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	if err := mw.WriteField(auth.DefaultCSRFFormField, token); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/x", &body)
	r.Header.Set("Content-Type", mw.FormDataContentType())
	r.AddCookie(&http.Cookie{Name: "__Host-ghtmx_session", Value: "live-token"})

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, r)
	if got := rec.Result().StatusCode; got != http.StatusOK {
		t.Errorf("status = %d, want 200", got)
	}
}

// TestCSRFFailsClosedWithoutSessionMiddleware: with no session
// middleware ahead of it there is no expected token, and the layer must
// reject rather than wave requests through — a wiring mistake must not
// silently disable CSRF protection.
func TestCSRFFailsClosedWithoutSessionMiddleware(t *testing.T) {
	h := auth.CSRF()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	r.Header.Set(ghtmx.DefaultCSRFHeaderName, "any-token")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if got := rec.Result().StatusCode; got != http.StatusForbidden {
		t.Errorf("status = %d, want 403", got)
	}
}

// TestVerifyCSRFToken covers the comparison core the middleware and the
// fiber glue share.
func TestVerifyCSRFToken(t *testing.T) {
	withToken := auth.ContextWithCSRFToken(context.Background(), "expected")
	tests := []struct {
		name      string
		ctx       context.Context
		submitted string
		wantErr   bool
	}{
		{name: "match", ctx: withToken, submitted: "expected", wantErr: false},
		{name: "mismatch", ctx: withToken, submitted: "other", wantErr: true},
		{name: "empty submission", ctx: withToken, submitted: "", wantErr: true},
		{name: "no session token in context", ctx: context.Background(), submitted: "expected", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := auth.VerifyCSRFToken(tt.ctx, tt.submitted)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("VerifyCSRFToken = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, auth.ErrCSRF) {
				t.Errorf("error %v does not wrap ErrCSRF", err)
			}
		})
	}
}

// TestSafeMethod pins the safe-list the glue packages share.
func TestSafeMethod(t *testing.T) {
	for method, want := range map[string]bool{
		http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true,
		http.MethodPost: false, http.MethodPut: false, http.MethodPatch: false, http.MethodDelete: false,
	} {
		if got := auth.SafeMethod(method); got != want {
			t.Errorf("SafeMethod(%s) = %v, want %v", method, got, want)
		}
	}
}
