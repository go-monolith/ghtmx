// Package corpus is the DATA-007 benchmark corpus: named page,
// fragment, and route-binding rendering workloads. It is the sole input
// to the NFR-002 render-throughput gate; the recorded figures live in
// baseline.json and the policy in BASELINE.md.
package corpus

import (
	"net/http"
	"strconv"
)

// Item is the row model every workload renders.
type Item struct {
	ID          string
	Name        string
	Description string
	Class       string
	Price       string
}

// MakeItems builds a deterministic item list of the given size.
func MakeItems(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		id := strconv.Itoa(i)
		items[i] = Item{
			ID:          id,
			Name:        "Item " + id,
			Description: "A perfectly ordinary catalogue entry numbered " + id,
			Class:       "price",
			Price:       "$" + id + ".99",
		}
	}
	return items
}

// The corpus owns its bound routes so the binding workload does not
// depend on example applications.
func routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /bench/items/{id}", BenchItem)
	mux.HandleFunc("POST /bench/items", BenchCreate)
	return mux
}

func BenchItem(w http.ResponseWriter, r *http.Request) {}

func BenchCreate(w http.ResponseWriter, r *http.Request) {}
