// Command cmd serves the beego fixture application.
package main

import (
	"log"
	"net/http"

	"github.com/go-monolith/ghtmx/adapters/beego/fixture"
)

func main() {
	log.Println("beego fixture listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", fixture.NewRouter()))
}
