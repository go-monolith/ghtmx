package analyzer

import (
	"fmt"
	goparser "go/parser"
	"go/token"
	"strconv"
	"strings"

	"github.com/go-monolith/ghtmx/internal/diag"
	"github.com/go-monolith/ghtmx/internal/generator"
	"github.com/go-monolith/ghtmx/internal/parser"
)

// syntheticImportHeader turns a template's top-level Go expression into a
// parseable file; positions are mapped back through its known length.
const syntheticImportHeader = "package p\n"

// ValidateImports reports GHTMX-E0308 for template imports that collide
// with the imports every generated file declares (generator.RuntimeImports):
// the unaliased root-package import, and any import aliased to one of the
// names the generated import block claims. Without the check, the collision
// surfaces as a Go redeclaration error inside a *_ghtmx.go file the author
// did not write, at a line they cannot edit.
//
// Header nodes are not scanned: they hold only pre-package comments and
// whitespace, where an import cannot legally appear.
func ValidateImports(file *parser.TemplateFile, sink *diag.Sink) {
	for _, n := range file.Nodes {
		g, ok := n.(*parser.TemplateFileGoExpression)
		if !ok {
			continue
		}
		checkExpressionImports(file, g, sink)
	}
}

// generatedImportNames returns the file-scope names the generated import
// block declares: the root package's own name and the runtime's forced
// alias.
func generatedImportNames() (rootName, runtimeAlias string) {
	root := generator.RuntimeImports[0]
	return root[strings.LastIndex(root, "/")+1:], generator.RuntimeImportAlias
}

func checkExpressionImports(file *parser.TemplateFile, g *parser.TemplateFileGoExpression, sink *diag.Sink) {
	if !strings.Contains(g.Expression.Value, "import") {
		return
	}
	fset := token.NewFileSet()
	f, err := goparser.ParseFile(fset, "imports.go", syntheticImportHeader+g.Expression.Value, goparser.ImportsOnly)
	if err != nil || f == nil {
		// Not parseable Go at this granularity; the generator or compiler
		// reports the real problem.
		return
	}
	rootName, runtimeAlias := generatedImportNames()
	for _, spec := range f.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		alias := ""
		if spec.Name != nil {
			alias = spec.Name.Name
		}
		var message, suggest string
		switch {
		case alias == "" && path == generator.RuntimeImports[0]:
			// Deliberate false negative: an unaliased THIRD-PARTY path
			// whose package happens to be named ghtmx would also collide,
			// but its package name is not knowable syntax-only, and E0308
			// is an unsilenceable error — a wrong guess would hard-block a
			// legal build. Only the exact root path is flagged unaliased.
			message = "template file imports the ghtmx root package; generated code already imports it"
			suggest = fmt.Sprintf("remove this import, or alias it: import ghtmxlib %q", path)
		case alias == rootName && path == generator.RuntimeImports[0]:
			message = fmt.Sprintf("import alias %q collides with the root-package import every generated file declares", alias)
			suggest = "remove this import (generated code imports it) or pick a different alias"
		case alias == rootName:
			message = fmt.Sprintf("import alias %q collides with the root-package import every generated file declares", alias)
			suggest = "rename the alias"
		case alias == runtimeAlias:
			message = fmt.Sprintf("import alias %q collides with the runtime import every generated file declares", alias)
			suggest = "rename the alias"
		default:
			continue
		}
		sink.Add(diag.ReservedImport, specPosition(file, g, fset, spec.Pos()), message, suggest)
	}
}

// specPosition maps a position inside the synthetic parse buffer back to
// the template file. Parser ranges are 0-indexed; diagnostics are
// 1-indexed (Validate rejects zero lines and columns).
func specPosition(file *parser.TemplateFile, g *parser.TemplateFileGoExpression, fset *token.FileSet, pos token.Pos) diag.Position {
	p := fset.Position(pos)
	from := g.Expression.Range.From
	// Line 2 of the buffer is the first line of the expression's value.
	line := from.Line + uint32(p.Line) - 1
	col := uint32(p.Column)
	if p.Line == 2 {
		// Same line as the expression's start: columns compose.
		col = from.Col + uint32(p.Column)
	}
	return diag.Position{
		File:  file.Filepath,
		Line:  line,
		Col:   col,
		Index: from.Index + int64(p.Offset-len(syntheticImportHeader)),
	}
}
