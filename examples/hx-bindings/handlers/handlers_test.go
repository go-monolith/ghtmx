package handlers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestUninstalledHooksFailLoudly pins the wiring contract: this test
// deliberately does NOT import the example package, so no init has
// installed the render hooks, and serving the handlers must be a loud
// 500 naming the missing hook — never a healthy-looking empty 200.
func TestUninstalledHooksFailLoudly(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		hook    string
	}{
		{"ListItems", ListItems, "ListItemsBody"},
		{"GetItem", GetItem, "GetItemBody"},
	}
	for _, tc := range cases {
		rec := httptest.NewRecorder()
		tc.handler(rec, httptest.NewRequest(http.MethodGet, "/items", nil))
		if rec.Code != http.StatusInternalServerError {
			t.Errorf("%s without its hook = %d, want 500", tc.name, rec.Code)
		}
		if !strings.Contains(rec.Body.String(), tc.hook) {
			t.Errorf("%s error must name the uninstalled hook %s, got %q", tc.name, tc.hook, rec.Body.String())
		}
	}
}
