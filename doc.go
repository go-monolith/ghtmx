// Package ghtmx is the runtime for ghtmx templates: the API that
// generated code calls, and the helpers an application calls around it.
//
// ghtmx is a fork of templ that makes htmx a first-class part of the
// template language. A .ghtmx file declares components as templ does,
// and adds two constructs of its own — fragment blocks, which compile to
// byte-identical inline and standalone entry points for htmx partial
// updates, and event declarations, which generate the only symbols that
// can emit HX-Trigger, with typed payloads. Route bindings such as
// hx-post={ handlers.CreateItem } are checked against the handlers the
// project actually registers.
//
// Templates are compiled ahead of time by the ghtmx command, which also
// provides the language server the editor integrations talk to:
//
//	go install github.com/go-monolith/ghtmx/cmd/ghtmx@latest
//	ghtmx generate
//
// Most of this package is called by generated code rather than by hand.
// What an application uses directly is the [Component] interface, the
// http.Handler wrapper [Handler] and its options, the HX-* response
// helpers ([SetRetarget], [SetPushURL], [SetRedirect], and the rest),
// [IsHTMXRequest] for branching on the request, and [HTMXScriptTag] to
// serve a pinned, integrity-checked htmx build.
//
// # Documentation
//
// The full documentation — the language, every diagnostic, the
// configuration reference, and a getting-started walkthrough — is at
// https://ghtmx.dev. The source lives in the repository and the site
// renders it, so the two cannot drift.
//
// # Stability
//
// Pre-1.0: breaking changes to the language syntax, the shape of
// generated code, and this API are allowed between minor versions, and
// each one is recorded in CHANGELOG.md with a migration note.
package ghtmx
