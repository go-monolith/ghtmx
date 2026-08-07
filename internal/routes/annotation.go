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

// annotationHint is the expected-form suggestion attached to every
// GHTMX-E0403.
const annotationHint = "expected: //ghtmx:route <VERB> </path> <Handler|pkg.Handler|Type.Method|pkg.Type.Method> [nav]"

// collectAnnotations scans a file's comments for //ghtmx:route declarations
// and returns the declared routes. Malformed annotations produce
// GHTMX-E0403 diagnostics.
func collectAnnotations(pkg *Package, file *ast.File, imports importMap, types map[string]bool, sink *diag.Sink) []Route {
	var out []Route
	for _, group := range file.Comments {
		for _, c := range group.List {
			if !strings.HasPrefix(c.Text, annotationPrefix) {
				continue
			}
			pos := position(pkg.Fset, c.Pos())
			rest := strings.TrimSpace(strings.TrimPrefix(c.Text, annotationPrefix))
			r, errMsg := parseAnnotation(rest, pkg, imports, types)
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

func parseAnnotation(s string, pkg *Package, imports importMap, types map[string]bool) (Route, string) {
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

	handler, errMsg := parseHandlerSymbol(fields[2], pkg, imports, types)
	if errMsg != "" {
		return Route{}, errMsg
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

// parseHandlerSymbol resolves an annotation's handler field. Four forms,
// matching what discovery itself can resolve:
//
//	Handler             a package-level func in this package
//	alias.Handler       a package-level func in an imported package
//	Type.Method         a method on a type declared in this package
//	alias.Type.Method   a method on a type in an imported package
//
// Imports win over local types on the first segment, mirroring
// resolveHandler, so an alias shadowed by a same-named type keeps
// resolving as the import.
func parseHandlerSymbol(sym string, pkg *Package, imports importMap, types map[string]bool) (SymbolRef, string) {
	segments := strings.Split(sym, ".")
	for _, s := range segments {
		if s == "" || !token.IsIdentifier(s) {
			return SymbolRef{}, fmt.Sprintf("malformed //ghtmx:route annotation: %q is not a valid handler symbol", sym)
		}
	}
	switch len(segments) {
	case 1:
		return SymbolRef{PkgPath: pkg.PkgPath, Name: segments[0]}, ""
	case 2:
		if path, ok := imports[segments[0]]; ok {
			return SymbolRef{PkgPath: path, Name: segments[1]}, ""
		}
		if types[segments[0]] {
			return SymbolRef{PkgPath: pkg.PkgPath, Name: sym}, ""
		}
		return SymbolRef{}, fmt.Sprintf("malformed //ghtmx:route annotation: %q is neither a package imported in this file nor a type declared in this package", segments[0])
	case 3:
		path, ok := imports[segments[0]]
		if !ok {
			return SymbolRef{}, fmt.Sprintf("malformed //ghtmx:route annotation: package %q is not imported in this file", segments[0])
		}
		return SymbolRef{PkgPath: path, Name: segments[1] + "." + segments[2]}, ""
	}
	return SymbolRef{}, fmt.Sprintf("malformed //ghtmx:route annotation: %q has too many qualifiers", sym)
}

// packageTypeNames collects the type names a package declares, so an
// annotation naming a method (Handlers.ListUsers) can be told apart from
// one naming a symbol in a package that was never imported.
func packageTypeNames(pkg *Package) map[string]bool {
	out := map[string]bool{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					out[ts.Name.Name] = true
				}
			}
		}
	}
	return out
}

func position(fset *token.FileSet, p token.Pos) Position {
	pos := fset.Position(p)
	return Position{File: pos.Filename, Line: uint32(pos.Line), Col: uint32(pos.Column)}
}
