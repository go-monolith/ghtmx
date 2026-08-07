// Command cmd serves the fiber v3 fixture application.
package main

import (
	"log"

	"github.com/go-monolith/ghtmx/adapters/fiberv3/fixture"
)

func main() {
	log.Println("fiber v3 fixture listening on :8080")
	log.Fatal(fixture.NewApp().Listen(":8080"))
}
