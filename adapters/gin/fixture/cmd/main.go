// Command cmd serves the gin fixture application.
package main

import (
	"log"

	ginfw "github.com/gin-gonic/gin"

	"github.com/go-monolith/ghtmx/adapters/gin/fixture"
)

func main() {
	ginfw.SetMode(ginfw.ReleaseMode)
	log.Println("gin fixture listening on :8080")
	log.Fatal(fixture.NewRouter().Run(":8080"))
}
