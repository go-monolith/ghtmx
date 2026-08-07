package routes

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"github.com/go-monolith/ghtmx/internal/diag"
)

// annotationPrefix marks an escape-hatch route declaration (FR-015):
//
//	//ghtmx:route GET /admin/users/{id} handlers.AdminUserShow
//	//ghtmx:route GET /files/{path...} files.Serve
//	//ghtmx:route GET /admin/audit handlers.AuditLog nav
//
// The handler symbol is resolved through the file's import declarations; a
// bare name refers to the file's own package. The optional trailing "nav"
// marker declares the route as navigation-only, exempting it from
// GHTMX-W0104.
const annotationPrefix = "//ghtmx:route"

// prefixDirective declares a package-scoped mount prefix (FR-013):
//
//	//ghtmx:routeprefix /admin/user
//	package adminpages
//
// Every route in the package — discovered or declared — is registered
// under it. A sub-application mounted at a variable prefix from another
// package cannot be recognised syntactically, so the prefix is declared
// rather than inferred: reviewable in a diff, versioned with the code.
const prefixDirective = "//ghtmx:routeprefix"

// annotationHint is the expected-form suggestion attached to every
// GHTMX-E0403 raised by a //ghtmx:route annotation.
const annotationHint = "expected: //ghtmx:route <VERB> </path> <pkg.Handler> [nav]"

// prefixHint is the expected-form suggestion for a malformed
// //ghtmx:routeprefix directive.
const prefixHint = "expected: //ghtmx:routeprefix </static/prefix>"

// collectAnnotations scans a file's comments for //ghtmx:route declarations
// and returns the declared routes. Malformed annotations produce
// GHTMX-E0403 diagnostics.
func collectAnnotations(pkg *Package, file *ast.File, imports importMap, sink *diag.Sink) []Route {
	var out []Route
	for _, group := range file.Comments {
		for _, c := range group.List {
			// The prefix directive shares the //ghtmx:route stem, so it
			// must be taken out of the running before the route check
			// claims it and reports it as malformed.
			if _, isPrefix := directiveArg(c.Text, prefixDirective); isPrefix {
				continue
			}
			arg, isAnnotation := directiveArg(c.Text, annotationPrefix)
			if !isAnnotation {
				continue
			}
			pos := position(pkg.Fset, c.Pos())
			rest := strings.TrimSpace(arg)
			r, errMsg := parseAnnotation(rest, pkg, imports)
			if errMsg != "" {
				sink.Add(diag.MalformedAnnotation, diag.Position{File: pos.File, Line: pos.Line, Col: pos.Col}, errMsg, annotationHint)
				continue
			}
			r.Pos = pos
			out = append(out, r)
		}
	}
	return out
}

func parseAnnotation(s string, pkg *Package, imports importMap) (Route, string) {
	fields := strings.Fields(s)
	if len(fields) < 3 || len(fields) > 4 {
		return Route{}, fmt.Sprintf("malformed //ghtmx:route annotation: expected 3 fields (verb, path, handler) and an optional marker, got %d", len(fields))
	}
	navOnly := false
	if len(fields) == 4 {
		// A closed marker set: an unknown word is far more likely a typo'd
		// handler or a stray token than a new intent.
		switch fields[3] {
		case "nav":
			navOnly = true
		default:
			return Route{}, fmt.Sprintf("malformed //ghtmx:route annotation: unknown marker %q (supported: nav)", fields[3])
		}
	}
	verb := Verb(strings.ToUpper(fields[0]))
	switch verb {
	case GET, POST, PUT, PATCH, DELETE:
	default:
		return Route{}, fmt.Sprintf("malformed //ghtmx:route annotation: unknown verb %q", fields[0])
	}
	path := fields[1]
	if !strings.HasPrefix(path, "/") {
		return Route{}, fmt.Sprintf("malformed //ghtmx:route annotation: path %q must start with /", path)
	}
	normalized, params := NormalizePath(path, BraceStyle)
	for _, param := range params {
		if param.Name == "" || !token.IsIdentifier(param.Name) {
			return Route{}, fmt.Sprintf("malformed //ghtmx:route annotation: path parameter names must be Go identifiers, got %q in %q", param.Name, path)
		}
	}

	sym := fields[2]
	var handler SymbolRef
	if alias, name, qualified := strings.Cut(sym, "."); qualified {
		pkgPath, ok := imports[alias]
		if !ok {
			return Route{}, fmt.Sprintf("malformed //ghtmx:route annotation: package %q is not imported in this file", alias)
		}
		handler = SymbolRef{PkgPath: pkgPath, Name: name}
	} else {
		handler = SymbolRef{PkgPath: pkg.PkgPath, Name: sym}
	}
	if handler.Name == "" || !token.IsIdentifier(handler.Name) {
		return Route{}, fmt.Sprintf("malformed //ghtmx:route annotation: %q is not a valid handler symbol", sym)
	}
	return Route{
		Verb:         verb,
		Path:         normalized,
		OriginalPath: path,
		Params:       params,
		Handler:      handler,
		Origin:       Declared,
		Recognizer:   "annotation",
		NavOnly:      navOnly,
	}, ""
}

// directiveArg reports whether comment text is the named directive, and
// returns everything after it. The name must be followed by whitespace
// or end the comment, so //ghtmx:routeprefixes in prose is not read as a
// misspelt directive.
func directiveArg(text, name string) (string, bool) {
	rest, ok := strings.CutPrefix(text, name)
	if !ok {
		return "", false
	}
	if rest == "" {
		return "", true
	}
	if r := rest[0]; r != ' ' && r != '\t' {
		return "", false
	}
	return rest, true
}

// collectRoutePrefix scans every file in the package for
// //ghtmx:routeprefix and returns the declared prefix, normalized to a
// leading slash and no trailing slash ("" when none is declared, and
// also for a bare "/" which adds nothing). A malformed directive, or two
// files declaring different prefixes, produces GHTMX-E0403 and yields no
// prefix — applying a guess would silently move every route in the
// package.
func collectRoutePrefix(pkg *Package, sink *diag.Sink) string {
	prefix := ""
	declared := false
	declaredAt := Position{}
	rejected := false
	for _, file := range pkg.Files {
		for _, group := range file.Comments {
			for _, c := range group.List {
				arg, isDirective := directiveArg(c.Text, prefixDirective)
				if !isDirective {
					continue
				}
				pos := position(pkg.Fset, c.Pos())
				at := diag.Position{File: pos.File, Line: pos.Line, Col: pos.Col}
				value, errMsg := parseRoutePrefix(arg)
				if errMsg != "" {
					sink.Add(diag.MalformedAnnotation, at, errMsg,
						prefixHint+" — until it is fixed the package's routes are registered unprefixed")
					rejected = true
					continue
				}
				// declared, not prefix != "", distinguishes "no directive
				// yet" from a declared "/" — which normalizes to "".
				if declared && value != prefix {
					sink.Add(diag.MalformedAnnotation, at,
						fmt.Sprintf("conflicting //ghtmx:routeprefix for package %s: %q here, %q at %s", pkg.PkgPath, displayPrefix(value), displayPrefix(prefix), declaredAt),
						"declare one prefix per package — until then its routes are registered unprefixed")
					rejected = true
					continue
				}
				prefix, declared, declaredAt = value, true, pos
			}
		}
	}
	if rejected {
		return ""
	}
	return prefix
}

// displayPrefix renders a normalized prefix the way its author wrote it,
// so a message about a declared "/" does not read as being about "".
func displayPrefix(prefix string) string {
	if prefix == "" {
		return "/"
	}
	return prefix
}

// parseRoutePrefix validates a directive's argument: exactly one field,
// rooted, static, and a plain path.
//
// Parameters are rejected because a prefix applies to every route in the
// package, so a parameter in it would have to be threaded into every
// generated constructor. Query, fragment, and empty segments are
// rejected because the prefix is pasted onto every path in the package:
// one stray "?" would silently turn every generated URL into garbage.
func parseRoutePrefix(s string) (string, string) {
	fields := strings.Fields(s)
	if len(fields) != 1 {
		return "", fmt.Sprintf("malformed //ghtmx:routeprefix directive: expected 1 field (the prefix), got %d", len(fields))
	}
	prefix := fields[0]
	if !strings.HasPrefix(prefix, "/") {
		return "", fmt.Sprintf("malformed //ghtmx:routeprefix directive: prefix %q must start with /", prefix)
	}
	// The parameter syntaxes of every recognized flavour: brace, colon,
	// and the wildcard forms.
	if strings.ContainsAny(prefix, "{}:*+") {
		return "", fmt.Sprintf("malformed //ghtmx:routeprefix directive: prefix %q must be static (no path parameters)", prefix)
	}
	if strings.ContainsAny(prefix, "?# ") {
		return "", fmt.Sprintf("malformed //ghtmx:routeprefix directive: prefix %q must be a path, with no query or fragment", prefix)
	}
	// "/" adds nothing; treat it as absent rather than as a prefix that
	// JoinPaths would have to special-case.
	prefix = strings.TrimRight(prefix, "/")
	for segment := range strings.SplitSeq(strings.TrimPrefix(prefix, "/"), "/") {
		switch segment {
		case "":
			if prefix == "" {
				continue // the whole prefix was "/".
			}
			return "", fmt.Sprintf("malformed //ghtmx:routeprefix directive: prefix %q has an empty path segment", prefix)
		case ".", "..":
			return "", fmt.Sprintf("malformed //ghtmx:routeprefix directive: prefix %q must be resolved, with no . or .. segment", prefix)
		}
	}
	return prefix, ""
}

func position(fset *token.FileSet, p token.Pos) Position {
	pos := fset.Position(p)
	return Position{File: pos.Filename, Line: uint32(pos.Line), Col: uint32(pos.Column)}
}
