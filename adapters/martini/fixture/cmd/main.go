// Command cmd serves the martini fixture application.
package main

import (
	"log"
	"net/http"

	"github.com/go-monolith/ghtmx/adapters/martini/fixture"
)

func main() {
	log.Println("martini fixture listening on :8080")
	if err := http.ListenAndServe(":8080", fixture.NewApp()); err != nil {
		log.Fatal(err)
	}
}
