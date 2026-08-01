// Package content embeds the documentation and example sources the
// site serves. Everything under docs/ and examples/ is a committed
// copy maintained by `go run ./internal/sync` and drift-checked by
// internal/sync/drift_test.go.
package content

import "embed"

// Plain patterns deliberately exclude dot- and underscore-prefixed
// files so a future sync change can never sweep them into the binary;
// the sync manifest's copyName mirrors the exclusion, so nothing that
// is copied can silently miss the embed either.
//
//go:embed docs examples
var FS embed.FS
