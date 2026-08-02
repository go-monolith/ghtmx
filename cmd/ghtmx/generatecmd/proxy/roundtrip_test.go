package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The dev proxy sits in front of a server the user is actively
// restarting, so a request arriving mid-restart is normal rather than
// exceptional. The round tripper retries through that window; without it
// every save would show the browser a connection error before the new
// binary finished binding its port.

func newRoundTripper(maxRetries int) *roundTripper {
	return &roundTripper{
		maxRetries:      maxRetries,
		initialDelay:    time.Millisecond,
		backoffExponent: 1.0,
	}
}

func TestRoundTripForwardsASuccessfulRequest(t *testing.T) {
	var gotBody string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, "<html><body>ok</body></html>")
	}))
	defer backend.Close()

	req, err := http.NewRequest(http.MethodPost, backend.URL, strings.NewReader("payload"))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := newRoundTripper(3).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	// The body is buffered so it can be replayed on a retry; losing it
	// would turn every retried POST into an empty one.
	if gotBody != "payload" {
		t.Errorf("the backend received %q, want %q", gotBody, "payload")
	}
}

// TestRoundTripRetriesABadGateway is the restart window: the proxy in
// front of a server that has not finished starting answers 502, and the
// tripper has to keep trying rather than showing the user an error.
func TestRoundTripRetriesABadGateway(t *testing.T) {
	var attempts int
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, "ready")
	}))
	defer backend.Close()

	req, err := http.NewRequest(http.MethodGet, backend.URL, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := newRoundTripper(10).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 after the backend came up", resp.StatusCode)
	}
	if attempts < 3 {
		t.Errorf("the backend saw %d attempts; the retry did not happen", attempts)
	}
}

// TestRoundTripGivesUpEventually pins the bound: a backend that never
// comes back has to produce an error rather than retrying forever and
// hanging the browser tab.
func TestRoundTripGivesUpEventually(t *testing.T) {
	// A port with nothing listening: every attempt fails to connect.
	target, err := url.Parse("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodGet, target.String(), nil)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := newRoundTripper(2).RoundTrip(req); err == nil {
		t.Error("RoundTrip succeeded against a port with nothing listening")
	}
}

// TestRoundTripReplaysTheBodyOnRetry pins the reason the body is
// buffered at all: a retried POST must carry the same payload, not an
// already-consumed reader.
func TestRoundTripReplaysTheBodyOnRetry(t *testing.T) {
	var bodies []string
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	req, err := http.NewRequest(http.MethodPost, backend.URL, strings.NewReader("form=data"))
	if err != nil {
		t.Fatal(err)
	}

	resp, err := newRoundTripper(5).RoundTrip(req)
	if err != nil {
		t.Fatalf("RoundTrip: %v", err)
	}
	defer resp.Body.Close()

	if len(bodies) < 2 {
		t.Fatalf("the backend saw %d requests, want at least 2", len(bodies))
	}
	for i, b := range bodies {
		if b != "form=data" {
			t.Errorf("attempt %d carried %q, want the original payload", i+1, b)
		}
	}
}
