// Command cmd serves the echo fixture application.
package main

import (
	"log"

	"github.com/go-monolith/ghtmx/adapters/echo/fixture"
)

func main() {
	log.Println("echo fixture listening on :8080")
	log.Fatal(fixture.NewRouter().Start(":8080"))
}
