// Command cmd builds the documentation site (used by the release
// publish workflow; tests build the same site on every CI run).
package main

import (
	"flag"
	"log"

	"github.com/go-monolith/ghtmx/internal/docsite"
)

func main() {
	out := flag.String("out", "dist/docs", "output directory")
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if err := docsite.Build(*root, *out); err != nil {
		log.Fatal(err)
	}
	log.Printf("documentation site written to %s", *out)
}
