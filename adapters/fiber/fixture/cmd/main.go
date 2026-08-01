// Command cmd serves the fiber fixture application.
package main

import (
	"log"

	"github.com/go-monolith/ghtmx/adapters/fiber/fixture"
)

func main() {
	log.Println("fiber fixture listening on :8080")
	log.Fatal(fixture.NewApp().Listen(":8080"))
}
