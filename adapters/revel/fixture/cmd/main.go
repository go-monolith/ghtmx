// Command cmd serves the revel fixture application.
package main

import (
	"log"
	"net/http"

	"github.com/go-monolith/ghtmx/adapters/revel/fixture"
)

func main() {
	log.Println("revel fixture listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", fixture.NewHandler()))
}
