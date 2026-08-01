// Command helloworld serves the hello-world example standalone.
package main

import (
	"fmt"
	"net/http"
	"os"

	example "github.com/go-monolith/ghtmx/examples/hello-world"
)

func main() {
	addr := "127.0.0.1:8080"
	if v := os.Getenv("GHTMX_EXAMPLE_ADDR"); v != "" {
		addr = v
	}
	fmt.Printf("Listening on http://%s/hello\n", addr)
	if err := http.ListenAndServe(addr, example.Routes()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
