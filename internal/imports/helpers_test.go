package imports

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"

	ghtmxparser "github.com/go-monolith/ghtmx/internal/parser"
)

// These decide which imports the formatter adds, keeps and removes. A
// wrong answer either drops an import the generated code needs — so the
// project stops compiling — or leaves one the user did not ask for, and
// `ghtmx fmt` churns the file on every save.

func TestConvertTemplToGoURI(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantIs    bool
		wantGoURI string
	}{
		{
			name:   "template file",
			in:     "file:///project/page.ghtmx",
			wantIs: true, wantGoURI: "file:///project/page_ghtmx.go",
		},
		{
			name:   "nested path",
			in:     "file:///a/b/c/page.ghtmx",
			wantIs: true, wantGoURI: "file:///a/b/c/page_ghtmx.go",
		},
		{
			name:   "bare filename",
			in:     "page.ghtmx",
			wantIs: true, wantGoURI: "page_ghtmx.go",
		},
		// Anything that is not a template must be reported as such, or
		// the formatter would try to organise imports for a .go file the
		// user is editing directly.
		{name: "go file", in: "file:///project/main.go"},
		{name: "generated file", in: "file:///project/page_ghtmx.go"},
		{name: "no extension", in: "file:///project/page"},
		{name: "empty", in: ""},
		// A directory that merely ends in the extension is not a file.
		{name: "similar suffix", in: "file:///project/page.ghtmxx"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			isTempl, goURI := convertTemplToGoURI(tt.in, ".ghtmx")
			if isTempl != tt.wantIs {
				t.Fatalf("isTemplFile = %v, want %v", isTempl, tt.wantIs)
			}
			if goURI != tt.wantGoURI {
				t.Errorf("goURI = %q, want %q", goURI, tt.wantGoURI)
			}
		})
	}
}

// parseImports returns the import specs of a source snippet.
func parseImports(t *testing.T, src string) []*ast.ImportSpec {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parsing: %v", err)
	}
	return f.Imports
}

func TestGetImportDetails(t *testing.T) {
	specs := parseImports(t, `package p

import (
	"strings"
	alias "net/http"
	_ "embed"
)
`)
	if len(specs) != 3 {
		t.Fatalf("parsed %d imports, want 3", len(specs))
	}

	tests := []struct {
		index    int
		wantName string
		wantPath string
	}{
		{0, "", "strings"},
		// The alias has to survive, or the generated code references a
		// package name that is not bound.
		{1, "alias", "net/http"},
		{2, "_", "embed"},
	}
	for _, tt := range tests {
		t.Run(tt.wantPath, func(t *testing.T) {
			name, path, err := getImportDetails(specs[tt.index])
			if err != nil {
				t.Fatalf("getImportDetails: %v", err)
			}
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}
		})
	}
}

// TestGetImportDetailsReportsAnUnquotablePath pins the error branch: an
// import spec whose path is not a valid quoted string cannot be acted on,
// and guessing would produce an import line that does not compile.
func TestGetImportDetailsReportsAnUnquotablePath(t *testing.T) {
	spec := &ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: `not-quoted`}}

	if _, _, err := getImportDetails(spec); err == nil {
		t.Error("getImportDetails accepted an unquotable path")
	}
}

func TestGetPackageIdentifier(t *testing.T) {
	tests := []struct {
		name       string
		alias      string
		importPath string
		want       string
	}{
		{"explicit alias wins", "h", "net/http", "h"},
		{"last path segment", "", "net/http", "http"},
		{"single segment", "", "strings", "strings"},
		// goimports cannot match a hyphenated directory to the
		// identifier it produces, which is why this exists at all.
		{"hyphen removed", "", "example.com/css-classes", "cssclasses"},
		{"several hyphens", "", "example.com/a-b-c", "abc"},
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getPackageIdentifier(tt.alias, tt.importPath); got != tt.want {
				t.Errorf("getPackageIdentifier(%q, %q) = %q, want %q", tt.alias, tt.importPath, got, tt.want)
			}
		})
	}
}

func TestContainsImport(t *testing.T) {
	specs := parseImports(t, "package p\n\nimport (\n\t\"strings\"\n\t\"net/http\"\n)\n")
	other := parseImports(t, "package p\n\nimport \"bytes\"\n")

	if !containsImport(specs, specs[0]) {
		t.Error("containsImport missed a spec that is in the list")
	}
	if containsImport(specs, other[0]) {
		t.Error("containsImport matched a spec that is not in the list")
	}
	if containsImport(nil, specs[0]) {
		t.Error("containsImport matched against an empty list")
	}
}

// TestIsPackageUsedInAST pins the check that keeps a hyphenated import
// from being deleted: goimports cannot see the connection, so this walks
// the generated code looking for the identifier itself.
func TestIsPackageUsedInAST(t *testing.T) {
	src := `package p

func f() {
	cssclasses.Button()
}
`
	f, err := parser.ParseFile(token.NewFileSet(), "x.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	if !isPackageUsedInAST(f, "cssclasses") {
		t.Error("a package referenced in the AST was reported unused; its import would be deleted and the build would break")
	}
	if isPackageUsedInAST(f, "unrelated") {
		t.Error("a package that never appears was reported as used")
	}
}

// TestHasParenthesizedImports pins the check that decides whether the
// single-import collapse runs. Getting it wrong reintroduces the comment
// reordering it was added to prevent.
func TestHasParenthesizedImports(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want bool
	}{
		{"parenthesised block", "package p\n\nimport (\n\t\"strings\"\n)\n", true},
		{"parenthesised block with several", "package p\n\nimport (\n\t\"strings\"\n\t\"bytes\"\n)\n", true},
		{"bare single import", "package p\n\nimport \"strings\"\n", false},
		{"no imports", "package p\n", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := parser.ParseFile(token.NewFileSet(), "x.go", tt.src, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			if got := hasParenthesizedImports(f); got != tt.want {
				t.Errorf("hasParenthesizedImports = %v, want %v for:\n%s", got, tt.want, tt.src)
			}
		})
	}
}

// TestProcessRejectsANonTemplateFilepath pins the guard: this only knows
// how to organise imports for a template's generated Go, so a filepath
// that is not a .ghtmx has to be reported rather than silently producing
// an import block for the wrong file.
func TestProcessRejectsANonTemplateFilepath(t *testing.T) {
	tf, err := ghtmxparser.ParseString("package p\n\ntempl x() {\n\t<div></div>\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	tf.Filepath = "/project/main.go"

	if _, err := Process(tf, ".ghtmx"); err == nil {
		t.Error("Process accepted a filepath that is not a template")
	}
}

// TestProcessWithNoFilepathIsANoOp pins the other guard: a template
// parsed from stdin has no path, and there is nothing to resolve imports
// against.
func TestProcessWithNoFilepathIsANoOp(t *testing.T) {
	tf, err := ghtmxparser.ParseString("package p\n\ntempl x() {\n\t<div></div>\n}\n")
	if err != nil {
		t.Fatal(err)
	}

	got, err := Process(tf, ".ghtmx")
	if err != nil {
		t.Fatalf("Process with no filepath: %v", err)
	}
	if got != tf {
		t.Error("Process replaced the template despite having nothing to do")
	}
}
