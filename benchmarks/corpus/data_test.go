package corpus

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

// The benchmark corpus has to be deterministic: NFR-002 compares
// allocation counts against a recorded baseline, and a workload that
// varied between runs would make every comparison meaningless.

func TestMakeItemsIsDeterministic(t *testing.T) {
	first := MakeItems(50)
	second := MakeItems(50)

	if len(first) != 50 {
		t.Fatalf("MakeItems(50) returned %d items", len(first))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("item %d differs between calls: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func TestMakeItemsPopulatesEveryField(t *testing.T) {
	for i, item := range MakeItems(3) {
		id := strconv.Itoa(i)
		if item.ID != id {
			t.Errorf("item %d has ID %q, want %q", i, item.ID, id)
		}
		// An empty field would shorten the rendered output and quietly
		// change the allocation count the gate measures.
		for name, value := range map[string]string{
			"Name": item.Name, "Description": item.Description,
			"Class": item.Class, "Price": item.Price,
		} {
			if value == "" {
				t.Errorf("item %d has an empty %s", i, name)
			}
			if !strings.Contains(item.Name, id) {
				t.Errorf("item %d's name %q does not carry its id", i, item.Name)
			}
		}
	}
}

func TestMakeItemsWithZero(t *testing.T) {
	if got := MakeItems(0); len(got) != 0 {
		t.Errorf("MakeItems(0) returned %d items", len(got))
	}
}

// TestRoutesAreBound pins the mux the binding workload resolves against.
// The corpus owns these deliberately, so the benchmark does not depend
// on the example applications.
func TestRoutesAreBound(t *testing.T) {
	mux := routes()

	tests := []struct {
		method string
		path   string
	}{
		{"GET", "/bench/items/42"},
		{"POST", "/bench/items"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			handler, pattern := mux.Handler(req)
			if pattern == "" {
				t.Fatalf("%s %s resolves to no route", tt.method, tt.path)
			}
			// The handlers are empty by design; what matters is that the
			// pattern resolves and the handler runs without panicking.
			handler.ServeHTTP(httptest.NewRecorder(), req)
		})
	}
}
