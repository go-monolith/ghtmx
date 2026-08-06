// Package docsite holds the gates on the repository's documentation
// (NFR-013): that the getting-started guide's code still compiles and
// renders what it promises, and that the prose which restates a value
// defined in Go — the pinned htmx version above all — still matches it.
//
// There is no site builder here. The published documentation site is
// docs/official, a ghtmx application deployed to https://ghtmx.dev by
// deploy-docs.yml; it renders its own markdown from docs/official/content,
// which docs/official/internal/sync keeps in step with these sources.
// This package once carried a second renderer whose HTML nothing
// published, so the checks stayed and the renderer went.
//
// The package has no non-test API. It exists for the tests in it.
package docsite
