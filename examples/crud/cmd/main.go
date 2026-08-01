// Command crud serves the crud example standalone.
package main

import (
	"fmt"
	"net/http"
	"os"

	example "github.com/go-monolith/ghtmx/examples/crud"
)

func main() {
	addr := "127.0.0.1:8080"
	if v := os.Getenv("GHTMX_EXAMPLE_ADDR"); v != "" {
		addr = v
	}
	fmt.Printf("Listening on http://%s/todos\n", addr)
	if err := http.ListenAndServe(addr, example.Routes()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
