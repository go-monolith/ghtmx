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
			// The exemption htmx 4's hx-query relies on, and the one
			// issue #45 asks to be able to decline; the default keeps it.
			name: "QUERY passes without a token",
			req:  func() *http.Request { return sessionRequest(auth.MethodQuery, "/x", nil) },
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

// TestHasFormContentType pins the gate both the net/http extractor and
// the fiber glue apply before consulting a request body. It is exported
// precisely so the two cannot drift, so its rule is tested directly
// rather than only through a middleware.
func TestHasFormContentType(t *testing.T) {
	for ct, want := range map[string]bool{
		"application/x-www-form-urlencoded":                true,
		"application/x-www-form-urlencoded; charset=utf-8": true,
		"Application/X-WWW-Form-URLEncoded":                true,
		"multipart/form-data":                              true,
		"multipart/form-data; boundary=abc":                true,
		"application/json":                                 false,
		"text/plain":                                       false,
		"":                                                 false,
		// fasthttp prefix-matches the urlencoded type, so these are
		// exactly the shapes where the two paths could have diverged.
		// The shared gate resolves them the strict way, for both.
		"application/x-www-form-urlencoded-not-really": false,
		"application/x-www-form-urlencoded; ;":         false,
	} {
		if got := auth.HasFormContentType(ct); got != want {
			t.Errorf("HasFormContentType(%q) = %v, want %v", ct, got, want)
		}
	}
}

// TestCSRFOnRejectHook: the hook observes rejections without changing
// them. It must see the method, the request path, and an error wrapping
// ErrCSRF — and must not fire for a request that passes.
func TestCSRFOnRejectHook(t *testing.T) {
	cfg := sessionConfig(auth.ErrUnauthorized)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	var got []auth.CSRFRejection
	chain := auth.Middleware(cfg)(auth.CSRF(auth.WithOnReject(
		func(ctx context.Context, rej auth.CSRFRejection) {
			// The identity is not on the rejection: it is read from the
			// same context, which is what makes one hook shape work for
			// every adapter.
			if id, okID := auth.IdentityFrom[string](ctx); !okID || id != "user-1" {
				t.Errorf("IdentityFrom = %q, %v; want the session's identity", id, okID)
			}
			got = append(got, rej)
		},
	))(ok))

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, sessionRequest(http.MethodPost, "/admin/delete?x=1", nil))
	if code := rec.Result().StatusCode; code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — the hook must not change the outcome", code)
	}
	if len(got) != 1 {
		t.Fatalf("hook fired %d times, want exactly 1", len(got))
	}
	if got[0].Method != http.MethodPost {
		t.Errorf("Method = %q, want POST", got[0].Method)
	}
	if got[0].Path != "/admin/delete" {
		t.Errorf("Path = %q, want /admin/delete — the path, never the query string", got[0].Path)
	}
	if !errors.Is(got[0].Err, auth.ErrCSRF) {
		t.Errorf("Err %v does not wrap ErrCSRF", got[0].Err)
	}

	// A request that passes must leave the hook untouched.
	_, token := csrfChain(t)
	pass := sessionRequest(http.MethodPost, "/admin/delete", nil)
	pass.Header.Set(ghtmx.DefaultCSRFHeaderName, token)
	chain.ServeHTTP(httptest.NewRecorder(), pass)
	if len(got) != 1 {
		t.Errorf("hook fired %d times, want 1 — an accepted request is not a rejection", len(got))
	}
}

// TestCSRFWithoutHook: the zero-option call is the documented default
// and must still answer 403 with nothing installed to observe it.
func TestCSRFWithoutHook(t *testing.T) {
	chain, _ := csrfChain(t)
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, sessionRequest(http.MethodPost, "/x", nil))
	if got := rec.Result().StatusCode; got != http.StatusForbidden {
		t.Errorf("status = %d, want 403", got)
	}
}

// TestSafeMethod pins the safe-list the glue packages share.
func TestSafeMethod(t *testing.T) {
	for method, want := range map[string]bool{
		http.MethodGet: true, http.MethodHead: true, http.MethodOptions: true, auth.MethodQuery: true,
		http.MethodPost: false, http.MethodPut: false, http.MethodPatch: false, http.MethodDelete: false,
	} {
		if got := auth.SafeMethod(method); got != want {
			t.Errorf("SafeMethod(%s) = %v, want %v", method, got, want)
		}
	}
}

// TestDefaultSafeMethods pins the documented default and its
// independence: the slice is fresh, so a caller that sorts or truncates
// it cannot reach into the package's own safe-list.
func TestDefaultSafeMethods(t *testing.T) {
	want := []string{http.MethodGet, http.MethodHead, http.MethodOptions, auth.MethodQuery}
	got := auth.DefaultSafeMethods()
	if len(got) != len(want) {
		t.Fatalf("DefaultSafeMethods() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("DefaultSafeMethods() = %v, want %v", got, want)
		}
	}
	got[0] = "MUTATED"
	if again := auth.DefaultSafeMethods(); again[0] != http.MethodGet {
		t.Errorf("mutating the result changed the package default: %v", again)
	}
}

// TestCSRFOptionsSafeMethodDefault: options built without
// WithSafeMethods must decide exactly as the package-level SafeMethod
// does — the nil map is "unconfigured", never "nothing is safe".
func TestCSRFOptionsSafeMethodDefault(t *testing.T) {
	o := auth.NewCSRFOptions(auth.WithOnReject(func(context.Context, auth.CSRFRejection) {}))
	for _, m := range []string{
		http.MethodGet, http.MethodHead, http.MethodOptions, auth.MethodQuery,
		http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
	} {
		if got, want := o.SafeMethod(m), auth.SafeMethod(m); got != want {
			t.Errorf("CSRFOptions.SafeMethod(%s) = %v, want the package default %v", m, got, want)
		}
	}
}

// TestCSRFWithSafeMethods: the opt-out issue #45 asks for. Each row is
// a middleware built with its own safe-list, so the QUERY exemption
// that arrived with htmx 4 can be declined by an application that never
// pinned htmx 4.
func TestCSRFWithSafeMethods(t *testing.T) {
	cfg := sessionConfig(auth.ErrUnauthorized)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// The htmx 2 safe-list: everything the library exempted before
	// v0.2.0, and nothing more.
	htmx2 := []string{http.MethodGet, http.MethodHead, http.MethodOptions}

	tests := []struct {
		name    string
		methods []string
		request string
		want    int
	}{
		{
			name:    "QUERY is rejected once it leaves the safe-list",
			methods: htmx2,
			request: auth.MethodQuery,
			want:    http.StatusForbidden,
		},
		{
			name:    "GET still passes under the narrowed list",
			methods: htmx2,
			request: http.MethodGet,
			want:    http.StatusOK,
		},
		{
			name:    "POST is rejected under the narrowed list too",
			methods: htmx2,
			request: http.MethodPost,
			want:    http.StatusForbidden,
		},
		{
			// The list replaces the default rather than adding to it,
			// so a caller can exempt a method the library never names.
			name:    "a method the default never exempts can be added",
			methods: []string{"REPORT"},
			request: "REPORT",
			want:    http.StatusOK,
		},
		{
			name:    "replacing the list drops the defaults it omits",
			methods: []string{"REPORT"},
			request: http.MethodGet,
			want:    http.StatusForbidden,
		},
		{
			// The nil-versus-empty distinction: no arguments means
			// nothing is safe, not "fall back to the default".
			name:    "no arguments makes every method unsafe",
			methods: []string{},
			request: http.MethodGet,
			want:    http.StatusForbidden,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chain := auth.Middleware(cfg)(auth.CSRF(auth.WithSafeMethods(tt.methods...))(ok))
			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, sessionRequest(tt.request, "/x", nil))
			if got := rec.Result().StatusCode; got != tt.want {
				t.Errorf("%s %s = %d, want %d", tt.request, "/x", got, tt.want)
			}
		})
	}
}

// TestCSRFWithSafeMethodsReportsRejections: a request refused because
// the safe-list was narrowed is an ordinary CSRF rejection, so the
// WithOnReject hook observes it like any other.
func TestCSRFWithSafeMethodsReportsRejections(t *testing.T) {
	cfg := sessionConfig(auth.ErrUnauthorized)
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	var got []auth.CSRFRejection
	chain := auth.Middleware(cfg)(auth.CSRF(
		auth.WithSafeMethods(http.MethodGet, http.MethodHead, http.MethodOptions),
		auth.WithOnReject(func(_ context.Context, rej auth.CSRFRejection) { got = append(got, rej) }),
	)(ok))

	chain.ServeHTTP(httptest.NewRecorder(), sessionRequest(auth.MethodQuery, "/rows", nil))

	if len(got) != 1 {
		t.Fatalf("hook fired %d times, want 1", len(got))
	}
	if got[0].Method != auth.MethodQuery || got[0].Path != "/rows" {
		t.Errorf("rejection = %+v, want QUERY /rows", got[0])
	}
	if !errors.Is(got[0].Err, auth.ErrCSRF) {
		t.Errorf("rejection error = %v, want it to satisfy errors.Is(err, auth.ErrCSRF)", got[0].Err)
	}
}
