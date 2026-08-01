package routes

import (
	"fmt"
	"go/ast"
	"go/token"

	"github.com/go-monolith/ghtmx/internal/diag"
	"golang.org/x/tools/go/packages"
)

// Package is one syntactically loaded Go package: ASTs and positions only,
// never type information.
type Package struct {
	PkgPath string
	Name    string
	Fset    *token.FileSet
	Files   []*ast.File
}

// Load loads the packages matching patterns rooted at dir in syntax-only
// mode (constitution A3.1): NeedName | NeedFiles | NeedSyntax. Packages
// that fail to parse produce a diagnostic and are skipped; the remainder
// still loads (FR-055). Type errors are invisible at this load level and
// cannot fail the pass.
func Load(dir string, patterns []string, sink *diag.Sink) ([]*Package, error) {
	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedSyntax,
		Dir:   dir,
		Tests: false,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("failed to load packages %v: %w", patterns, err)
	}
	var out []*Package
	for _, p := range pkgs {
		// Report parse errors but keep whatever files did parse: graceful
		// degradation, never a first-error abort.
		for _, e := range p.Errors {
			if e.Kind != packages.ParseError && e.Kind != packages.ListError {
				continue
			}
			pos := diag.Position{File: "go.mod", Line: 1, Col: 1}
			if f, l, c, ok := splitErrorPos(e.Pos); ok {
				pos = diag.Position{File: f, Line: l, Col: c}
			}
			sink.Add(diag.UnresolvableRoute, pos, fmt.Sprintf("package %s cannot be fully analyzed: %s", p.PkgPath, e.Msg), "fix the parse error, or declare affected routes with //ghtmx:route annotations")
		}
		if len(p.Syntax) == 0 && len(p.Errors) > 0 {
			continue
		}
		out = append(out, &Package{
			PkgPath: p.PkgPath,
			Name:    p.Name,
			Fset:    p.Fset,
			Files:   p.Syntax,
		})
	}
	return out, nil
}

// splitErrorPos parses a "file:line:col" position string from
// packages.Error.
func splitErrorPos(pos string) (file string, line, col uint32, ok bool) {
	if pos == "" || pos == "-" {
		return "", 0, 0, false
	}
	var l, c int
	// Try file:line:col, then file:line.
	n := lastIndexByte(pos, ':')
	if n < 0 {
		return "", 0, 0, false
	}
	if _, err := fmt.Sscanf(pos[n+1:], "%d", &c); err != nil {
		return "", 0, 0, false
	}
	rest := pos[:n]
	m := lastIndexByte(rest, ':')
	if m < 0 {
		// file:line only.
		return rest, uint32(c), 1, true
	}
	if _, err := fmt.Sscanf(rest[m+1:], "%d", &l); err != nil {
		return "", 0, 0, false
	}
	return rest[:m], uint32(l), uint32(c), true
}

func lastIndexByte(s string, b byte) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == b {
			return i
		}
	}
	return -1
}
