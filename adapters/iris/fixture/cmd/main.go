// Command cmd serves the iris fixture application.
package main

import (
	"log"

	"github.com/go-monolith/ghtmx/adapters/iris/fixture"
)

func main() {
	log.Println("iris fixture listening on :8080")
	log.Fatal(fixture.NewApp().Listen(":8080"))
}
