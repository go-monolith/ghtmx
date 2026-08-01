// Package content embeds the documentation and example sources the
// site serves. Everything under docs/ and examples/ is a committed
// copy maintained by `go run ./internal/sync` and drift-checked by
// internal/sync/drift_test.go.
package content

import "embed"

// all: keeps underscore- and dot-prefixed copies embedded too, so a
// file the sync manifest copies can never silently vanish from the
// binary.
//
//go:embed all:docs all:examples
var FS embed.FS
