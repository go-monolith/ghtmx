package runtime

import (
	"net/http"
	"strings"
	"testing"
)

func TestAppendTrigger(t *testing.T) {
	type emit struct {
		name    string
		payload any
	}
	tests := []struct {
		name     string
		existing string // pre-set header value, "" for none
		emits    []emit
		want     string
	}{
		{
			name:  "single payload-less event stays simple",
			emits: []emit{{"cart-cleared", nil}},
			want:  "cart-cleared",
		},
		{
			name:  "two payload-less events stay a comma list",
			emits: []emit{{"cart-cleared", nil}, {"item-saved", nil}},
			want:  "cart-cleared, item-saved",
		},
		{
			name:  "payload forces the JSON form",
			emits: []emit{{"item-saved", map[string]string{"id": "1"}}},
			want:  `{"item-saved":{"id":"1"}}`,
		},
		{
			name:  "mixed emissions merge into one JSON object in order",
			emits: []emit{{"cart-cleared", nil}, {"item-saved", map[string]string{"id": "1"}}},
			want:  `{"cart-cleared":null,"item-saved":{"id":"1"}}`,
		},
		{
			name:  "payload first then payload-less keeps JSON form",
			emits: []emit{{"item-saved", map[string]string{"id": "1"}}, {"cart-cleared", nil}},
			want:  `{"item-saved":{"id":"1"},"cart-cleared":null}`,
		},
		{
			name:  "re-emitting replaces the payload in place",
			emits: []emit{{"a-b", map[string]int{"n": 1}}, {"c-d", nil}, {"a-b", map[string]int{"n": 2}}},
			want:  `{"a-b":{"n":2},"c-d":null}`,
		},
		{
			name:     "merges an existing simple list",
			existing: "cart-cleared, item-saved",
			emits:    []emit{{"user-created", map[string]string{"id": "9"}}},
			want:     `{"cart-cleared":null,"item-saved":null,"user-created":{"id":"9"}}`,
		},
		{
			name:     "merges an existing JSON header preserving order",
			existing: `{"b-b":{"x":1},"a-a":null}`,
			emits:    []emit{{"c-c", nil}},
			want:     `{"b-b":{"x":1},"a-a":null,"c-c":null}`,
		},
		{
			name:  "hostile name forces JSON",
			emits: []emit{{`x,y`, nil}},
			want:  `{"x,y":null}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			if tt.existing != "" {
				h.Set("HX-Trigger", tt.existing)
			}
			w := headerOnlyResponseWriter{header: h}
			for _, e := range tt.emits {
				if err := AppendTrigger(w, e.name, e.payload); err != nil {
					t.Fatal(err)
				}
			}
			if got := h.Get("HX-Trigger"); got != tt.want {
				t.Errorf("HX-Trigger = %q, want %q", got, tt.want)
			}
			if len(h.Values("HX-Trigger")) != 1 {
				t.Errorf("emissions must collapse to a single header, got %v", h.Values("HX-Trigger"))
			}
		})
	}
}

func TestAppendTriggerCollapsesMultiValueHeaders(t *testing.T) {
	h := http.Header{}
	h.Add("HX-Trigger", "a-a")
	h.Add("HX-Trigger", "b-b")
	if err := AppendTrigger(headerOnlyResponseWriter{header: h}, "c-c", nil); err != nil {
		t.Fatal(err)
	}
	if got := h.Values("HX-Trigger"); len(got) != 1 || got[0] != "a-a, b-b, c-c" {
		t.Errorf("multi-value headers must collapse into one, got %v", got)
	}
}

func TestAppendTriggerErrors(t *testing.T) {
	h := http.Header{}
	w := headerOnlyResponseWriter{header: h}
	if err := AppendTrigger(w, "", nil); err == nil {
		t.Error("an empty event name must error")
	}
	if err := AppendTrigger(w, "bad-payload", func() {}); err == nil || !strings.Contains(err.Error(), "serialize") {
		t.Errorf("an unserializable payload must error, got %v", err)
	}
	h.Set("HX-Trigger", "{not json")
	if err := AppendTrigger(w, "x-y", nil); err == nil {
		t.Error("a corrupt existing JSON header must error")
	}
}

type headerOnlyResponseWriter struct{ header http.Header }

func (w headerOnlyResponseWriter) Header() http.Header         { return w.header }
func (w headerOnlyResponseWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w headerOnlyResponseWriter) WriteHeader(int)             {}

func TestAppendTriggerEscapesNonASCII(t *testing.T) {
	h := http.Header{}
	w := headerOnlyResponseWriter{header: h}
	if err := AppendTrigger(w, "item-saved", map[string]string{"name": "héllo 🎉"}); err != nil {
		t.Fatal(err)
	}
	got := h.Get("HX-Trigger")
	for i := 0; i < len(got); i++ {
		if got[i] >= 0x80 {
			t.Fatalf("header must be pure ASCII, got %q", got)
		}
	}
	want := `{"item-saved":{"name":"h\u00E9llo \uD83C\uDF89"}}`
	if got != want {
		t.Errorf("HX-Trigger = %q, want %q", got, want)
	}
}

func TestAppendTriggerRejectsTrailingGarbage(t *testing.T) {
	h := http.Header{}
	h.Set("HX-Trigger", `{"a-a":1}{"b-b":2}`)
	if err := AppendTrigger(headerOnlyResponseWriter{header: h}, "c-c", nil); err == nil {
		t.Error("trailing data after the JSON object must error, not be dropped")
	}
}

func TestTimingVariantsUseIndependentHeaders(t *testing.T) {
	h := http.Header{}
	w := headerOnlyResponseWriter{header: h}
	if err := AppendTrigger(w, "a-a", nil); err != nil {
		t.Fatal(err)
	}
	if err := AppendTriggerAfterSettle(w, "b-b", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := AppendTriggerAfterSwap(w, "c-c", nil); err != nil {
		t.Fatal(err)
	}
	if err := AppendTriggerAfterSettle(w, "d-d", nil); err != nil {
		t.Fatal(err)
	}
	if got := h.Get("HX-Trigger"); got != "a-a" {
		t.Errorf("HX-Trigger = %q", got)
	}
	if got := h.Get("HX-Trigger-After-Settle"); got != `{"b-b":{"n":1},"d-d":null}` {
		t.Errorf("HX-Trigger-After-Settle = %q", got)
	}
	if got := h.Get("HX-Trigger-After-Swap"); got != "c-c" {
		t.Errorf("HX-Trigger-After-Swap = %q", got)
	}
}
