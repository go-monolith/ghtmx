// Command cmd builds the release artifacts (used by the release
// workflow; tests exercise the same builder on every CI run).
package main

import (
	"flag"
	"log"

	"github.com/go-monolith/ghtmx/internal/release"
)

func main() {
	version := flag.String("version", "", "release version (tag form, e.g. v1.2.3)")
	out := flag.String("out", "dist/release", "output directory")
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if *version == "" {
		log.Fatal("-version is required")
	}
	if err := release.Build(*root, *version, *out); err != nil {
		log.Fatal(err)
	}
	log.Printf("release artifacts written to %s", *out)
}
