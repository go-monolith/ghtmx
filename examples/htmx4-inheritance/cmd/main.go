// Command htmx4inheritance serves the htmx4-inheritance example standalone.
package main

import (
	"fmt"
	"net/http"
	"os"

	example "github.com/go-monolith/ghtmx/examples/htmx4-inheritance"
)

func main() {
	addr := "127.0.0.1:8086"
	if v := os.Getenv("GHTMX_EXAMPLE_ADDR"); v != "" {
		addr = v
	}
	fmt.Printf("Listening on http://%s/inherit\n", addr)
	if err := http.ListenAndServe(addr, example.Routes()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
