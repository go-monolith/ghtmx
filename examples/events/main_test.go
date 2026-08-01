package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/ghtmxgen"
)

// TestTwoEmissionsMergeIntoOneHeader: FR-037 — two events emitted in one
// response merge into a single, correctly-serialized HX-Trigger header in
// htmx 2.x's JSON object form, preserving emission order.
func TestTwoEmissionsMergeIntoOneHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/events/items/1", nil)
	eventsMux().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("save failed with status %d: %s", rec.Code, rec.Body.String())
	}
	values := rec.Result().Header.Values("HX-Trigger")
	if len(values) != 1 {
		t.Fatalf("both emissions must share one header, got %v", values)
	}
	want := `{"item-saved":{"id":"1","name":"Alpha"},"cart-cleared":null}`
	if values[0] != want {
		t.Errorf("HX-Trigger = %q, want %q", values[0], want)
	}
}

// TestEmitterIsTyped: the payload struct fields mirror the declaration;
// this call compiling is the FR-037 type-check working (a mismatch is a Go
// compile error, proven adversarially in the generatecmd e2e tests).
func TestEmitterIsTyped(t *testing.T) {
	rec := httptest.NewRecorder()
	if err := ghtmxgen.EmitItemSaved(rec, ghtmxgen.ItemSavedPayload{Id: "9", Name: "Iota"}); err != nil {
		t.Fatal(err)
	}
	if got := rec.Header().Get("HX-Trigger"); !strings.Contains(got, `"id":"9"`) {
		t.Errorf("payload fields must serialize under their declared names, got %q", got)
	}
}

// TestPageBindsEventListeners: the page references both wire names, so
// the declarations are used (no GHTMX-W0102) and listeners line up with
// what the emitters send.
func TestPageBindsEventListeners(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	eventsMux().ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{`hx-on:item-saved=`, `hx-trigger="cart-cleared from:body"`, `hx-post="/events/items/1"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s in page:\n%s", want, body)
		}
	}
}

// TestCSRFBindingNeedsNoHandWrittenPlumbing: FR-092 — one helper call in
// the template attaches the application-supplied token via hx-headers on
// the state-changing hx-post binding; htmx then sends it as a request
// header. The engine neither generates nor validates the token.
func TestCSRFBindingNeedsNoHandWrittenPlumbing(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	req.AddCookie(&http.Cookie{Name: "csrf", Value: "tok123"})
	eventsMux().ServeHTTP(rec, req)

	body := rec.Body.String()
	want := `hx-headers="{&#34;X-CSRF-Token&#34;:&#34;tok123&#34;}"`
	if !strings.Contains(body, want) {
		t.Errorf("expected the attached token attribute %s, got:\n%s", want, body)
	}
	if !strings.Contains(body, `hx-post="/events/items/1"`) {
		t.Errorf("the state-changing binding must remain intact:\n%s", body)
	}
}
