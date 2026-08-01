// Command cmd serves the chi fixture application.
package main

import (
	"log"
	"net/http"

	"github.com/go-monolith/ghtmx/adapters/chi/fixture"
)

func main() {
	log.Println("chi fixture listening on :8080")
	if err := http.ListenAndServe(":8080", fixture.NewRouter()); err != nil {
		log.Fatal(err)
	}
}
