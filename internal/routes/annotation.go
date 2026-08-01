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
//
// The handler symbol is resolved through the file's import declarations; a
// bare name refers to the file's own package.
const annotationPrefix = "//ghtmx:route"

// collectAnnotations scans a file's comments for //ghtmx:route declarations
// and returns the declared routes. Malformed annotations produce
// GHTMX-E0403 diagnostics.
func collectAnnotations(pkg *Package, file *ast.File, imports importMap, sink *diag.Sink) []Route {
	var out []Route
	for _, group := range file.Comments {
		for _, c := range group.List {
			if !strings.HasPrefix(c.Text, annotationPrefix) {
				continue
			}
			pos := position(pkg.Fset, c.Pos())
			rest := strings.TrimSpace(strings.TrimPrefix(c.Text, annotationPrefix))
			r, errMsg := parseAnnotation(rest, pkg, imports)
			if errMsg != "" {
				sink.Add(diag.MalformedAnnotation, diag.Position{File: pos.File, Line: pos.Line, Col: pos.Col}, errMsg, "expected: //ghtmx:route <VERB> </path> <pkg.Handler>")
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
	if len(fields) != 3 {
		return Route{}, fmt.Sprintf("malformed //ghtmx:route annotation: expected 3 fields (verb, path, handler), got %d", len(fields))
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
	}, ""
}

func position(fset *token.FileSet, p token.Pos) Position {
	pos := fset.Position(p)
	return Position{File: pos.Filename, Line: uint32(pos.Line), Col: uint32(pos.Column)}
}
