// Package syntaxcheck enforces NFR-013: SYNTAX.md is the language
// surface's source of truth, and this package keeps it bound to the
// tests in both directions — every golden corpus entry is referenced
// by the specification, every verification anchor it names exists and
// carries tests, and every parser node type is documented.
// Undocumented syntax is unsupported: a new corpus entry or parser
// node fails here until SYNTAX.md describes it.
package syntaxcheck

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the syntaxcheck source file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

func specification(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(moduleRoot(t), "SYNTAX.md"))
	if err != nil {
		t.Fatalf("the syntax specification is missing: %v", err)
	}
	return string(raw)
}

// TestEveryCorpusEntryIsDocumented: a golden corpus entry is a
// language-feature example; the specification must claim it. A new
// construct's corpus entry fails here until SYNTAX.md documents it.
func TestEveryCorpusEntryIsDocumented(t *testing.T) {
	spec := specification(t)
	entries, err := filepath.Glob(filepath.Join(moduleRoot(t), "internal", "generator", "test-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) < 40 {
		t.Fatalf("only %d corpus entries found — the corpus moved?", len(entries))
	}
	for _, dir := range entries {
		name := filepath.Base(dir)
		// The closing backtick keeps prefix names (test-if, test-for)
		// from being satisfied by a longer sibling's anchor.
		if !strings.Contains(spec, "internal/generator/"+name+"`") {
			t.Errorf("corpus entry %s is not referenced by SYNTAX.md — undocumented syntax is unsupported", name)
		}
	}
}

// TestEveryVerificationAnchorExistsAndTests: the paths the
// specification cites must exist, and directory anchors must contain
// tests — a documented example that does not run as a test is not an
// example.
func TestEveryVerificationAnchorExistsAndTests(t *testing.T) {
	root := moduleRoot(t)
	spec := specification(t)
	anchor := regexp.MustCompile("`((?:internal|examples|conformance)[a-zA-Z0-9_./-]*)`")
	seen := map[string]bool{}
	for _, match := range anchor.FindAllStringSubmatch(spec, -1) {
		path := strings.TrimSuffix(match[1], "/")
		if seen[path] {
			continue
		}
		seen[path] = true
		full := filepath.Join(root, filepath.FromSlash(path))
		info, err := os.Stat(full)
		if err != nil {
			t.Errorf("SYNTAX.md cites %s, which does not exist: %v", path, err)
			continue
		}
		if !info.IsDir() {
			continue
		}
		tests, err := filepath.Glob(filepath.Join(full, "*_test.go"))
		if err != nil {
			t.Fatal(err)
		}
		subTests, err := filepath.Glob(filepath.Join(full, "*", "*_test.go"))
		if err != nil {
			t.Fatal(err)
		}
		if len(tests)+len(subTests) == 0 {
			t.Errorf("SYNTAX.md cites %s as verification, but it contains no tests", path)
		}
	}
	if len(seen) < 50 {
		t.Errorf("only %d verification anchors found in SYNTAX.md — the anchor format changed?", len(seen))
	}
}

// TestEveryParserNodeTypeIsDocumented: the parser's exported node
// types are the language surface; each must appear in SYNTAX.md. A new
// node type fails here until the specification describes its syntax.
func TestEveryParserNodeTypeIsDocumented(t *testing.T) {
	spec := specification(t)
	// Pure source-position plumbing, not syntax.
	infrastructure := map[string]bool{
		"Position":   true,
		"Range":      true,
		"Expression": true,
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(moduleRoot(t), "internal", "parser", "types.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, s := range gen.Specs {
			spec2, ok := s.(*ast.TypeSpec)
			if !ok || !spec2.Name.IsExported() || infrastructure[spec2.Name.Name] {
				continue
			}
			switch spec2.Type.(type) {
			case *ast.StructType, *ast.InterfaceType:
			default:
				continue
			}
			count++
			if !regexp.MustCompile("`" + spec2.Name.Name + "`").MatchString(spec) {
				t.Errorf("parser node type %s is not documented in SYNTAX.md — undocumented syntax is unsupported", spec2.Name.Name)
			}
		}
	}
	if count < 40 {
		t.Fatalf("only %d parser node types enumerated — the parser layout moved?", count)
	}
}

// TestCarveOutsAreDocumented: the FR-004 contract — both carve-outs
// with their diagnostics and replacement pointers, and the conformance
// exclusion list — must stay present.
func TestCarveOutsAreDocumented(t *testing.T) {
	spec := specification(t)
	for _, needle := range []string{
		"TEMPL_SYNTAX_BASELINE",
		"04abee5364c6fab2bde8c00d215fdcb630ad6a94",
		"GHTMX-E0601",
		"GHTMX-E0602",
		"GHTMX-E0603",
		"CONFORMANCE.md",
		// The non-carve-out exclusions must stay summarized too.
		"test-fragment",
		"Prettier",
	} {
		if !strings.Contains(spec, needle) {
			t.Errorf("SYNTAX.md must reference %q", needle)
		}
	}
	conformance, err := os.ReadFile(filepath.Join(moduleRoot(t), "CONFORMANCE.md"))
	if err != nil {
		t.Fatalf("the conformance exclusion list is missing: %v", err)
	}
	if !strings.Contains(string(conformance), "## Exclusion list") {
		t.Error("CONFORMANCE.md lost its exclusion list section")
	}
}
