package generator

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	"html"
	"io"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode"

	_ "embed"

	"github.com/go-monolith/ghtmx/internal/parser"
)

type GenerateOpt func(g *generator) error

// WithVersion enables the version to be included in the generated code.
func WithVersion(v string) GenerateOpt {
	return func(g *generator) error {
		g.options.Version = v
		return nil
	}
}

// WithTimestamp enables the generated date to be included in the generated code.
func WithTimestamp(d time.Time) GenerateOpt {
	return func(g *generator) error {
		g.options.GeneratedDate = d.Format(time.RFC3339)
		return nil
	}
}

// WithFileName sets the filename of the templ file in template rendering error messages.
func WithFileName(name string) GenerateOpt {
	return func(g *generator) error {
		if filepath.IsAbs(name) {
			_, g.options.FileName = filepath.Split(name)
			return nil
		}
		g.options.FileName = name
		return nil
	}
}

// WithSkipCodeGeneratedComment skips the code generated comment at the top of the file.
// gopls disables edit related functionality for generated files, so the templ LSP may
// wish to skip generation of this comment so that gopls provides expected results.
func WithSkipCodeGeneratedComment() GenerateOpt {
	return func(g *generator) error {
		g.options.SkipCodeGeneratedComment = true
		return nil
	}
}

// WithEditorBindings drops the ghtmx.SafeURL type expectation from the
// hx-* verb attribute expressions the analyzer would have lowered, for
// the language server only. generatedPkg names the central generated
// package, which distinguishes the two cases.
//
// When `ghtmx generate` resolves a symbol binding against the route
// table, the analyzer folds it into the registered path and the
// generator never sees it. The language server generates straight from
// the parsed template with no table, so the binding does reach the
// generator, and the normal emission produces
// `var v ghtmx.SafeURL = CreateTodo` — a type error against a template
// that builds perfectly, which is what surfaces in editors as a false
// IncompatibleAssign.
//
// Only the shapes the analyzer lowers are relaxed: a bare handler
// identifier, and a handler qualified by a package other than the
// generated one. A route-constructor call or a generated-package
// reference is left alone, because those reach the generator in a real
// build too — their result really must be a ghtmx.SafeURL, and the
// editor should keep saying so.
//
// Lowering inside the server instead would delete the expression, and
// with it the position gopls completes, defines, and hovers at inside
// `{ … }` (FR-081, FR-083). Dropping only the assignment keeps the
// symbol live for the editor, and the escaping contract stays enforced
// everywhere the code is actually compiled.
func WithEditorBindings(generatedPkg string) GenerateOpt {
	return func(g *generator) error {
		g.options.EditorBindings = true
		g.options.EditorGeneratedPkg = generatedPkg
		return nil
	}
}

// relaxHxURLType reports whether the hx-* verb expression is one the
// analyzer would have lowered before generation, and so is not required
// to produce a ghtmx.SafeURL in the editor's copy of the Go.
//
// An unparseable expression is relaxed as well: it is broken Go either
// way, and a half-typed binding should not gain a type error on top of
// the syntax error it already has.
func relaxHxURLType(expr, generatedPkg string) bool {
	parsed, err := goparser.ParseExpr(strings.TrimSpace(expr))
	if err != nil {
		return true
	}
	switch e := parsed.(type) {
	case *goast.Ident:
		// A bare handler, resolved against the template's own package.
		return true
	case *goast.SelectorExpr:
		// pkg.Handler, unless pkg is the generated package — those are
		// route constructors and keep their type.
		qualifier, ok := e.X.(*goast.Ident)
		return ok && qualifier.Name != generatedPkg
	default:
		return false
	}
}

type GeneratorOutput struct {
	Options   GeneratorOptions  `json:"meta"`
	SourceMap *parser.SourceMap `json:"sourceMap"`
	Literals  []string          `json:"literals"`
}

type GeneratorOptions struct {
	// Version of ghtmx.
	Version string
	// FileName to include in error messages if string expressions return an error.
	FileName string
	// SkipCodeGeneratedComment skips the code generated comment at the top of the file.
	SkipCodeGeneratedComment bool
	// GeneratedDate to include as a comment.
	GeneratedDate string
	// EditorBindings relaxes the hx-* verb attribute emission for the
	// language server. See WithEditorBindings.
	EditorBindings bool
	// EditorGeneratedPkg is the central generated package's name, which
	// tells a route constructor apart from a handler reference.
	EditorGeneratedPkg string
}

// HasGoChanged returns true if the Go code has changed between the previous and updated GeneratorOutput.
func HasGoChanged(previous, updated GeneratorOutput) bool {
	// If generator options have changed, we need to recompile.
	if previous.Options.Version != updated.Options.Version {
		return true
	}
	if previous.Options.FileName != updated.Options.FileName {
		return true
	}
	if previous.Options.SkipCodeGeneratedComment != updated.Options.SkipCodeGeneratedComment {
		return true
	}
	// We don't check the generated date as it's not used for determining if the file has changed.
	// If the number of literals has changed, we need to recompile.
	if len(previous.Literals) != len(updated.Literals) {
		return true
	}
	// If the Go code has changed, we need to recompile.
	if len(previous.SourceMap.Expressions) != len(updated.SourceMap.Expressions) {
		return true
	}
	for i, prev := range previous.SourceMap.Expressions {
		if prev != updated.SourceMap.Expressions[i] {
			return true
		}
	}
	return false
}

// HasTextChanged returns true if the text literals have changed between the previous and updated GeneratorOutput.
func HasTextChanged(previous, updated GeneratorOutput) bool {
	if len(previous.Literals) != len(updated.Literals) {
		return true
	}
	for i, prev := range previous.Literals {
		if prev != updated.Literals[i] {
			return true
		}
	}
	return false
}

// Generate generates Go code from the input template file to w, and returns a map of the location of Go expressions in the template
// to the location of the generated Go code in the output.
func Generate(template *parser.TemplateFile, w io.Writer, opts ...GenerateOpt) (op GeneratorOutput, err error) {
	g := &generator{
		tf:        template,
		w:         NewRangeWriter(w),
		sourceMap: parser.NewSourceMap(),
	}
	for _, opt := range opts {
		if err = opt(g); err != nil {
			return
		}
	}
	err = g.generate()
	if err != nil {
		return op, err
	}
	op.Options = g.options
	op.SourceMap = g.sourceMap
	op.Literals = g.w.Literals
	return op, nil
}

type generator struct {
	tf          *parser.TemplateFile
	w           *RangeWriter
	sourceMap   *parser.SourceMap
	variableID  int
	childrenVar string
	// pendingFragments are fragments declared inside the template being
	// written; their shared bodies and wrappers are emitted after it.
	pendingFragments []*parser.FragmentDeclaration

	options GeneratorOptions
}

func (g *generator) generate() (err error) {
	if err = g.writeCodeGeneratedComment(); err != nil {
		return
	}
	if err = g.writeVersionComment(); err != nil {
		return
	}
	if err = g.writeGeneratedDateComment(); err != nil {
		return
	}
	if err = g.writeHeader(); err != nil {
		return
	}
	if err = g.writePackage(); err != nil {
		return
	}
	if err = g.writeImports(); err != nil {
		return
	}
	if err = g.writeTemplateNodes(); err != nil {
		return
	}
	if err = g.writeBlankAssignmentForRuntimeImport(); err != nil {
		return
	}
	return err
}

// See https://pkg.go.dev/cmd/go#hdr-Generate_Go_files_by_processing_source
// Automatically generated files have a comment in the header that instructs the LSP
// to stop operating.
func (g *generator) writeCodeGeneratedComment() (err error) {
	if g.options.SkipCodeGeneratedComment {
		// Write an empty comment so that the file is the same shape.
		_, err = g.w.Write("//\n\n")
		return err
	}
	_, err = g.w.Write("// Code generated by ghtmx - DO NOT EDIT.\n\n")
	return err
}

func (g *generator) writeVersionComment() (err error) {
	if g.options.Version != "" {
		_, err = g.w.Write("// ghtmx: version: " + g.options.Version + "\n")
	}
	return err
}

func (g *generator) writeGeneratedDateComment() (err error) {
	if g.options.GeneratedDate != "" {
		_, err = g.w.Write("// ghtmx: generated: " + g.options.GeneratedDate + "\n")
	}
	return err
}

func (g *generator) writeHeader() (err error) {
	if len(g.tf.Header) == 0 {
		return nil
	}
	for _, n := range g.tf.Header {
		if err := g.writeGoExpression(n); err != nil {
			return err
		}
	}
	return err
}

func (g *generator) writePackage() error {
	var r parser.Range
	var err error
	// package ...
	if r, err = g.w.Write(g.tf.Package.Expression.Value + "\n\n"); err != nil {
		return err
	}
	g.sourceMap.Add(g.tf.Package.Expression, r)
	if _, err = g.w.Write("//lint:file-ignore SA4006 This context is only used if a nested component is present.\n\n"); err != nil {
		return err
	}
	return nil
}

// RuntimeImports are the packages every generated file imports, in
// emission order: the root interface package, then the generated-code
// runtime. The import-isolation gate (internal/importcheck) verifies
// their combined transitive closure stays standard-library only
// (NFR-012) — extend both together.
var RuntimeImports = []string{
	"github.com/go-monolith/ghtmx",
	"github.com/go-monolith/ghtmx/runtime",
}

// RuntimeImportAlias is the alias writeImports gives RuntimeImports[1].
// The analyzer's reserved-import check (GHTMX-E0308) flags template
// imports that would collide with it — keep the two in sync through this
// constant.
const RuntimeImportAlias = "ghtmxruntime"

func (g *generator) writeImports() error {
	var err error
	// Always import templ because it's the interface type of all templates.
	if _, err = g.w.Write("import \"" + RuntimeImports[0] + "\"\n"); err != nil {
		return err
	}
	if _, err = g.w.Write("import " + RuntimeImportAlias + " \"" + RuntimeImports[1] + "\"\n"); err != nil {
		return err
	}
	if _, err = g.w.Write("\n"); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeTemplateNodes() error {
	for i, n := range g.tf.Nodes {
		switch n := n.(type) {
		case *parser.TemplateFileGoExpression:
			if err := g.writeGoExpression(n); err != nil {
				return err
			}
		case *parser.HTMLTemplate:
			if err := g.writeTemplate(i, n); err != nil {
				return err
			}
			// Hoist fragments declared inside this template to file level.
			if err := g.flushPendingFragments(); err != nil {
				return err
			}
		case *parser.FragmentDeclaration:
			if err := g.writeFragmentDeclaration(n); err != nil {
				return err
			}
			// A fragment body can itself declare fragments; hoist them too.
			if err := g.flushPendingFragments(); err != nil {
				return err
			}
		case *parser.EventDeclaration:
			// Events generate no code in the template's file: the emission
			// symbols live in the central generated package (FR-037).
		case *parser.CSSTemplate:
			if err := g.writeCSS(n); err != nil {
				return err
			}
		case *parser.ScriptTemplate:
			if err := g.writeScript(n); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown node type: %v", reflect.TypeOf(n))
		}
	}
	return nil
}

func (g *generator) writeCSS(n *parser.CSSTemplate) error {
	if n == nil {
		return errors.New("CSS template is nil")
	}
	var r parser.Range
	var tgtSymbolRange parser.Range
	var err error
	var indentLevel int

	// func
	if r, err = g.w.Write("func "); err != nil {
		return err
	}
	tgtSymbolRange.From = r.From
	if r, err = g.w.Write(n.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(n.Expression, r)
	// ghtmx.CSSClass {
	if _, err = g.w.Write(" ghtmx.CSSClass {\n"); err != nil {
		return err
	}
	{
		indentLevel++
		// ghtmx_7f3b9d1a_CSSBuilder := templruntim.GetBuilder()
		if _, err = g.w.WriteIndent(indentLevel, "ghtmx_7f3b9d1a_CSSBuilder := ghtmxruntime.GetBuilder()\n"); err != nil {
			return err
		}
		for _, p := range n.Properties {
			switch p := p.(type) {
			case *parser.ConstantCSSProperty:
				// Constant CSS property values are not sanitized.
				if _, err = g.w.WriteIndent(indentLevel, "ghtmx_7f3b9d1a_CSSBuilder.WriteString("+createGoString(p.String(true))+")\n"); err != nil {
					return err
				}
			case *parser.ExpressionCSSProperty:
				// ghtmx_7f3b9d1a_CSSBuilder.WriteString(ghtmx.SanitizeCSS('name', p.Expression()))
				if _, err = g.w.WriteIndent(indentLevel, fmt.Sprintf("ghtmx_7f3b9d1a_CSSBuilder.WriteString(string(ghtmx.SanitizeCSS(`%s`, ", p.Name)); err != nil {
					return err
				}
				if r, err = g.w.Write(p.Value.Expression.Value); err != nil {
					return err
				}
				g.sourceMap.Add(p.Value.Expression, r)
				if _, err = g.w.Write(")))\n"); err != nil {
					return err
				}
			default:
				return fmt.Errorf("unknown CSS property type: %v", reflect.TypeOf(p))
			}
		}
		if _, err = g.w.WriteIndent(indentLevel, fmt.Sprintf("ghtmx_7f3b9d1a_CSSID := ghtmx.CSSID(`%s`, ghtmx_7f3b9d1a_CSSBuilder.String())\n", n.Name)); err != nil {
			return err
		}
		// return ghtmx.CSS {
		if _, err = g.w.WriteIndent(indentLevel, "return ghtmx.ComponentCSSClass{\n"); err != nil {
			return err
		}
		{
			indentLevel++
			// ID: ghtmx_7f3b9d1a_CSSID,
			if _, err = g.w.WriteIndent(indentLevel, "ID: ghtmx_7f3b9d1a_CSSID,\n"); err != nil {
				return err
			}
			// Class: ghtmx.SafeCSS(".cssID{" + ghtmx.CSSBuilder.String() + "}"),
			if _, err = g.w.WriteIndent(indentLevel, "Class: ghtmx.SafeCSS(`.` + ghtmx_7f3b9d1a_CSSID + `{` + ghtmx_7f3b9d1a_CSSBuilder.String() + `}`),\n"); err != nil {
				return err
			}
			indentLevel--
		}
		if _, err = g.w.WriteIndent(indentLevel, "}\n"); err != nil {
			return err
		}
		indentLevel--
	}
	// }
	if r, err = g.w.WriteIndent(indentLevel, "}\n\n"); err != nil {
		return err
	}

	// Keep a track of symbol ranges for the LSP.
	tgtSymbolRange.To = r.To
	g.sourceMap.AddSymbolRange(n.Range, tgtSymbolRange)

	return nil
}

func (g *generator) writeGoExpression(n *parser.TemplateFileGoExpression) (err error) {
	if n == nil {
		return errors.New("go expression is nil")
	}
	var tgtSymbolRange parser.Range

	r, err := g.w.Write(n.Expression.Value)
	if err != nil {
		return err
	}
	tgtSymbolRange.From = r.From
	g.sourceMap.Add(n.Expression, r)
	v := n.Expression.Value
	lineSlice := strings.Split(v, "\n")
	lastLine := lineSlice[len(lineSlice)-1]
	if strings.HasPrefix(lastLine, "//") {
		if _, err = g.w.WriteIndent(0, "\n"); err != nil {
			return err
		}
		return err
	}
	if r, err = g.w.WriteIndent(0, "\n\n"); err != nil {
		return err
	}

	// Keep a track of symbol ranges for the LSP.
	tgtSymbolRange.To = r.To
	g.sourceMap.AddSymbolRange(n.Expression.Range, tgtSymbolRange)

	return err
}

func (g *generator) writeTemplBuffer(indentLevel int) (err error) {
	// ghtmx_7f3b9d1a_Buffer, ghtmx_7f3b9d1a_IsBuffer := ghtmxruntime.GetBuffer(ghtmx_7f3b9d1a_W)
	if _, err = g.w.WriteIndent(indentLevel, "ghtmx_7f3b9d1a_Buffer, ghtmx_7f3b9d1a_IsBuffer := ghtmxruntime.GetBuffer(ghtmx_7f3b9d1a_W)\n"); err != nil {
		return err
	}
	// if !ghtmx_7f3b9d1a_IsBuffer {
	//	defer func() {
	//		ghtmx_7f3b9d1a_BufErr := ghtmxruntime.ReleaseBuffer(ghtmx_7f3b9d1a_Buffer)
	//		if ghtmx_7f3b9d1a_Err == nil {
	//			ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_BufErr
	//		}
	//	}()
	// }
	if _, err = g.w.WriteIndent(indentLevel, "if !ghtmx_7f3b9d1a_IsBuffer {\n"); err != nil {
		return err
	}
	{
		indentLevel++
		if _, err = g.w.WriteIndent(indentLevel, "defer func() {\n"); err != nil {
			return err
		}
		{
			indentLevel++
			if _, err = g.w.WriteIndent(indentLevel, "ghtmx_7f3b9d1a_BufErr := ghtmxruntime.ReleaseBuffer(ghtmx_7f3b9d1a_Buffer)\n"); err != nil {
				return err
			}
			if _, err = g.w.WriteIndent(indentLevel, "if ghtmx_7f3b9d1a_Err == nil {\n"); err != nil {
				return err
			}
			{
				indentLevel++
				if _, err = g.w.WriteIndent(indentLevel, "ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_BufErr\n"); err != nil {
					return err
				}
				indentLevel--
			}
			if _, err = g.w.WriteIndent(indentLevel, "}\n"); err != nil {
				return err
			}
			indentLevel--
		}
		if _, err = g.w.WriteIndent(indentLevel, "}()\n"); err != nil {
			return err
		}
		indentLevel--
	}
	if _, err = g.w.WriteIndent(indentLevel, "}\n"); err != nil {
		return err
	}
	return
}

func (g *generator) writeTemplate(nodeIdx int, t *parser.HTMLTemplate) error {
	if t == nil {
		return errors.New("template is nil")
	}
	var r parser.Range
	var tgtSymbolRange parser.Range
	var err error
	var indentLevel int

	// func
	if r, err = g.w.Write("func "); err != nil {
		return err
	}
	tgtSymbolRange.From = r.From
	// (r *Receiver) Name(params []string)
	if r, err = g.w.Write(t.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(t.Expression, r)
	// ghtmx.Component {
	if _, err = g.w.Write(" ghtmx.Component {\n"); err != nil {
		return err
	}
	indentLevel++
	// return ghtmxruntime.GeneratedTemplate(func(ghtmx_7f3b9d1a_Input ghtmxruntime.GeneratedComponentInput) (ghtmx_7f3b9d1a_Err error) {
	if _, err = g.w.WriteIndent(indentLevel, "return ghtmxruntime.GeneratedTemplate(func(ghtmx_7f3b9d1a_Input ghtmxruntime.GeneratedComponentInput) (ghtmx_7f3b9d1a_Err error) {\n"); err != nil {
		return err
	}
	{
		indentLevel++
		if _, err = g.w.WriteIndent(indentLevel, "ghtmx_7f3b9d1a_W, ctx := ghtmx_7f3b9d1a_Input.Writer, ghtmx_7f3b9d1a_Input.Context\n"); err != nil {
			return err
		}
		if _, err = g.w.WriteIndent(indentLevel, "if ghtmx_7f3b9d1a_CtxErr := ctx.Err(); ghtmx_7f3b9d1a_CtxErr != nil {\n"); err != nil {
			return err
		}
		{
			indentLevel++
			if _, err = g.w.WriteIndent(indentLevel, "return ghtmx_7f3b9d1a_CtxErr"); err != nil {
				return err
			}
			indentLevel--
		}
		if _, err = g.w.WriteIndent(indentLevel, "}\n"); err != nil {
			return err
		}
		if err := g.writeTemplBuffer(indentLevel); err != nil {
			return err
		}
		// ctx = ghtmx.InitializeContext(ctx)
		if _, err = g.w.WriteIndent(indentLevel, "ctx = ghtmx.InitializeContext(ctx)\n"); err != nil {
			return err
		}
		g.childrenVar = g.createVariableName()
		// ghtmx_7f3b9d1a_Var1 := ghtmx.GetChildren(ctx)
		// if ghtmx_7f3b9d1a_Var1 == nil {
		//  	ghtmx_7f3b9d1a_Var1 = ghtmx.NopComponent
		// }
		if _, err = g.w.WriteIndent(indentLevel, fmt.Sprintf("%s := ghtmx.GetChildren(ctx)\n", g.childrenVar)); err != nil {
			return err
		}
		if _, err = g.w.WriteIndent(indentLevel, fmt.Sprintf("if %s == nil {\n", g.childrenVar)); err != nil {
			return err
		}
		{
			indentLevel++
			if _, err = g.w.WriteIndent(indentLevel, fmt.Sprintf("%s = ghtmx.NopComponent\n", g.childrenVar)); err != nil {
				return err
			}
			indentLevel--
		}
		if _, err = g.w.WriteIndent(indentLevel, "}\n"); err != nil {
			return err
		}
		// ctx = ghtmx.ClearChildren(children)
		if _, err = g.w.WriteIndent(indentLevel, "ctx = ghtmx.ClearChildren(ctx)\n"); err != nil {
			return err
		}
		// Nodes.
		if err = g.writeNodes(indentLevel, stripWhitespace(t.Children), nil); err != nil {
			return err
		}
		// return nil
		if _, err = g.w.WriteIndent(indentLevel, "return nil\n"); err != nil {
			return err
		}
		indentLevel--
	}
	// })
	if _, err = g.w.WriteIndent(indentLevel, "})\n"); err != nil {
		return err
	}
	indentLevel--
	// }

	// Note: gofmt wants to remove a single empty line at the end of a file
	// so we have to make sure we don't output one if this is the last node.
	closingBrace := "}\n\n"
	if nodeIdx+1 >= len(g.tf.Nodes) {
		closingBrace = "}\n"
	}

	if r, err = g.w.WriteIndent(indentLevel, closingBrace); err != nil {
		return err
	}

	// Keep a track of symbol ranges for the LSP.
	tgtSymbolRange.To = r.To
	g.sourceMap.AddSymbolRange(t.Range, tgtSymbolRange)

	return nil
}

func stripWhitespace(input []parser.Node) (output []parser.Node) {
	for i, n := range input {
		if _, isWhiteSpace := n.(*parser.Whitespace); !isWhiteSpace {
			output = append(output, input[i])
		}
	}
	return output
}

func stripLeadingWhitespace(nodes []parser.Node) []parser.Node {
	for i, n := range nodes {
		if _, isWhiteSpace := n.(*parser.Whitespace); !isWhiteSpace {
			return nodes[i:]
		}
	}
	return []parser.Node{}
}

func stripTrailingWhitespace(nodes []parser.Node) []parser.Node {
	for i := len(nodes) - 1; i >= 0; i-- {
		n := nodes[i]
		if _, isWhiteSpace := n.(*parser.Whitespace); !isWhiteSpace {
			return nodes[0 : i+1]
		}
	}
	return []parser.Node{}
}

func stripLeadingAndTrailingWhitespace(nodes []parser.Node) []parser.Node {
	return stripTrailingWhitespace(stripLeadingWhitespace(nodes))
}

func (g *generator) writeNodes(indentLevel int, nodes []parser.Node, next parser.Node) error {
	for i, curr := range nodes {
		var nextNode parser.Node
		if i+1 < len(nodes) {
			nextNode = nodes[i+1]
		}
		if nextNode == nil {
			nextNode = next
		}
		if err := g.writeNode(indentLevel, curr, nextNode); err != nil {
			return err
		}
	}
	return nil
}

func (g *generator) writeNode(indentLevel int, current parser.Node, next parser.Node) (err error) {
	switch n := current.(type) {
	case *parser.DocType:
		err = g.writeDocType(indentLevel, n)
	case *parser.Element:
		err = g.writeElement(indentLevel, n)
	case *parser.HTMLComment:
		err = g.writeComment(indentLevel, n)
	case *parser.ChildrenExpression:
		err = g.writeChildrenExpression(indentLevel)
	case *parser.RawElement:
		err = g.writeRawElement(indentLevel, n)
	case *parser.ScriptElement:
		err = g.writeScriptElement(indentLevel, n)
	case *parser.ForExpression:
		err = g.writeForExpression(indentLevel, n, next)
	case *parser.CallTemplateExpression:
		err = g.writeCallTemplateExpression(indentLevel, n)
	case *parser.TemplElementExpression:
		err = g.writeTemplElementExpression(indentLevel, n)
	case *parser.IfExpression:
		err = g.writeIfExpression(indentLevel, n, next)
	case *parser.FragmentDeclaration:
		// Inline participation at the declaration site (FR-030): a direct
		// call to the shared body with the enclosing scope's variables,
		// bound by name. The body and wrappers are hoisted to file level
		// after the enclosing template.
		g.pendingFragments = append(g.pendingFragments, n)
		err = g.writeFragmentBodyCall(indentLevel, n)
	case *parser.SwitchExpression:
		err = g.writeSwitchExpression(indentLevel, n, next)
	case *parser.StringExpression:
		err = g.writeStringExpression(indentLevel, n.Expression)
	case *parser.GoCode:
		err = g.writeGoCode(indentLevel, n.Expression)
	case *parser.Whitespace:
		err = g.writeWhitespace(indentLevel, n)
	case *parser.Text:
		err = g.writeText(indentLevel, n)
	case *parser.Fallthrough:
		err = g.writeFallthrough(indentLevel)
	case *parser.GoComment:
		// Do not render Go comments in the output HTML.
		return
	default:
		return fmt.Errorf("unhandled type: %v", reflect.TypeOf(n))
	}
	// Write trailing whitespace, if there is a next node that might need the space.
	if ws, ok := current.(parser.WhitespaceTrailer); ok && isTrailingSpaceNeeded(current, next) {
		if err := g.writeWhitespaceTrailer(indentLevel, ws.Trailing()); err != nil {
			return err
		}
	}
	return
}

func isInlineOrText(next parser.Node) bool {
	// While these are formatted as blocks when they're written in the HTML template.
	// They're inline - i.e. there's no whitespace rendered around them at runtime for minification.
	if next == nil {
		return false
	}
	switch n := next.(type) {
	case *parser.IfExpression:
		return true
	case *parser.SwitchExpression:
		return true
	case *parser.ForExpression:
		return true
	case *parser.Element:
		return !n.IsBlockElement()
	case *parser.Text:
		return true
	case *parser.StringExpression:
		return true
	}
	return false
}

func isTextLike(n parser.Node) bool {
	switch n.(type) {
	case *parser.Text:
		return true
	case *parser.StringExpression:
		return true
	}
	return false
}

func isSelfClosingTemplElementExpression(n parser.Node) bool {
	tee, ok := n.(*parser.TemplElementExpression)
	return ok && len(tee.Children) == 0
}

// isTrailingSpaceNeeded reports whether trailing whitespace should be emitted between current and next.
// Trailing space is needed when both nodes are inline content (text, inline elements, control flow), or
// when a self-closing templ element expression is adjacent to text or a string expression. When text
// precedes a self-closing templ expression, trailing space is only emitted for horizontal whitespace
// (same-line), not vertical (different lines), to avoid adding spaces before block-level components.
func isTrailingSpaceNeeded(current, next parser.Node) bool {
	if isInlineOrText(current) && isInlineOrText(next) {
		return true
	}
	if isSelfClosingTemplElementExpression(current) && isTextLike(next) {
		return true
	}
	if isTextLike(current) && isSelfClosingTemplElementExpression(next) {
		ws, ok := current.(parser.WhitespaceTrailer)
		return ok && ws.Trailing() == parser.SpaceHorizontal
	}
	return false
}

func (g *generator) writeWhitespaceTrailer(indentLevel int, n parser.TrailingSpace) (err error) {
	if n == parser.SpaceNone {
		return nil
	}
	// Normalize whitespace for minified output. In HTML, a single space is equivalent to
	// any number of spaces, tabs, or newlines.
	if n == parser.SpaceVertical {
		n = parser.SpaceHorizontal
	}
	if _, err = g.w.WriteStringLiteral(indentLevel, string(n)); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeDocType(indentLevel int, n *parser.DocType) (err error) {
	if _, err = g.w.WriteStringLiteral(indentLevel, fmt.Sprintf("<!doctype %s>", escapeQuotes(n.Value))); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeFallthrough(indentLevel int) (err error) {
	_, err = g.w.WriteIndent(indentLevel, "fallthrough\n")
	return err
}

func escapeQuotes(s string) string {
	quoted := strconv.Quote(s)
	return quoted[1 : len(quoted)-1]
}

func (g *generator) writeIfExpression(indentLevel int, n *parser.IfExpression, nextNode parser.Node) (err error) {
	var r parser.Range
	// if
	if _, err = g.w.WriteIndent(indentLevel, `if `); err != nil {
		return err
	}
	// x == y {
	if r, err = g.w.Write(n.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(n.Expression, r)
	// {
	if _, err = g.w.Write(` {` + "\n"); err != nil {
		return err
	}
	{
		indentLevel++
		if err = g.writeNodes(indentLevel, stripLeadingAndTrailingWhitespace(n.Then), nextNode); err != nil {
			return err
		}
		indentLevel--
	}
	for _, elseIf := range n.ElseIfs {
		// } else if {
		if _, err = g.w.WriteIndent(indentLevel, `} else if `); err != nil {
			return err
		}
		// x == y {
		if r, err = g.w.Write(elseIf.Expression.Value); err != nil {
			return err
		}
		g.sourceMap.Add(elseIf.Expression, r)
		// {
		if _, err = g.w.Write(` {` + "\n"); err != nil {
			return err
		}
		{
			indentLevel++
			if err = g.writeNodes(indentLevel, stripLeadingAndTrailingWhitespace(elseIf.Then), nextNode); err != nil {
				return err
			}
			indentLevel--
		}
	}
	if len(n.Else) > 0 {
		// } else {
		if _, err = g.w.WriteIndent(indentLevel, `} else {`+"\n"); err != nil {
			return err
		}
		{
			indentLevel++
			if err = g.writeNodes(indentLevel, stripLeadingAndTrailingWhitespace(n.Else), nextNode); err != nil {
				return err
			}
			indentLevel--
		}
	}
	// }
	if _, err = g.w.WriteIndent(indentLevel, `}`+"\n"); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeSwitchExpression(indentLevel int, n *parser.SwitchExpression, next parser.Node) (err error) {
	var r parser.Range
	// switch
	if _, err = g.w.WriteIndent(indentLevel, `switch `); err != nil {
		return err
	}
	// val
	if r, err = g.w.Write(n.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(n.Expression, r)
	// {
	if _, err = g.w.Write(` {` + "\n"); err != nil {
		return err
	}

	if len(n.Cases) > 0 {
		for _, c := range n.Cases {
			// case x:
			// default:
			if r, err = g.w.WriteIndent(indentLevel, c.Expression.Value); err != nil {
				return err
			}
			g.sourceMap.Add(c.Expression, r)
			indentLevel++
			if err = g.writeNodes(indentLevel, stripLeadingAndTrailingWhitespace(c.Children), next); err != nil {
				return err
			}
			indentLevel--
		}
	}
	// }
	if _, err = g.w.WriteIndent(indentLevel, `}`+"\n"); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeChildrenExpression(indentLevel int) (err error) {
	if _, err = g.w.WriteIndent(indentLevel, fmt.Sprintf("ghtmx_7f3b9d1a_Err = %s.Render(ctx, ghtmx_7f3b9d1a_Buffer)\n", g.childrenVar)); err != nil {
		return err
	}
	if err = g.writeErrorHandler(indentLevel); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeTemplElementExpression(indentLevel int, n *parser.TemplElementExpression) (err error) {
	if len(n.Children) == 0 {
		return g.writeSelfClosingTemplElementExpression(indentLevel, n)
	}
	return g.writeBlockTemplElementExpression(indentLevel, n)
}

func (g *generator) writeBlockTemplElementExpression(indentLevel int, n *parser.TemplElementExpression) (err error) {
	var r parser.Range
	childrenName := g.createVariableName()
	if _, err = g.w.WriteIndent(indentLevel, childrenName+" := ghtmxruntime.GeneratedTemplate(func(ghtmx_7f3b9d1a_Input ghtmxruntime.GeneratedComponentInput) (ghtmx_7f3b9d1a_Err error) {\n"); err != nil {
		return err
	}
	indentLevel++
	if _, err = g.w.WriteIndent(indentLevel, "ghtmx_7f3b9d1a_W, ctx := ghtmx_7f3b9d1a_Input.Writer, ghtmx_7f3b9d1a_Input.Context\n"); err != nil {
		return err
	}
	if err := g.writeTemplBuffer(indentLevel); err != nil {
		return err
	}
	// ctx = ghtmx.InitializeContext(ctx)
	if _, err = g.w.WriteIndent(indentLevel, "ctx = ghtmx.InitializeContext(ctx)\n"); err != nil {
		return err
	}
	if err = g.writeNodes(indentLevel, stripLeadingAndTrailingWhitespace(n.Children), nil); err != nil {
		return err
	}
	// return nil
	if _, err = g.w.WriteIndent(indentLevel, "return nil\n"); err != nil {
		return err
	}
	indentLevel--
	if _, err = g.w.WriteIndent(indentLevel, "})\n"); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(indentLevel, `ghtmx_7f3b9d1a_Err = `); err != nil {
		return err
	}
	if r, err = g.w.Write(n.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(n.Expression, r)
	// .Render(ghtmx.WithChildren(ctx, children), ghtmx_7f3b9d1a_Buffer)
	if _, err = g.w.Write(".Render(ghtmx.WithChildren(ctx, " + childrenName + "), ghtmx_7f3b9d1a_Buffer)\n"); err != nil {
		return err
	}
	if err = g.writeErrorHandler(indentLevel); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeSelfClosingTemplElementExpression(indentLevel int, n *parser.TemplElementExpression) (err error) {
	if _, err = g.w.WriteIndent(indentLevel, `ghtmx_7f3b9d1a_Err = `); err != nil {
		return err
	}
	// Template expression.
	var r parser.Range
	if r, err = g.w.Write(n.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(n.Expression, r)
	// .Render(ctx, ghtmx_7f3b9d1a_Buffer)
	if _, err = g.w.Write(".Render(ctx, ghtmx_7f3b9d1a_Buffer)\n"); err != nil {
		return err
	}
	if err = g.writeErrorHandler(indentLevel); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeCallTemplateExpression(indentLevel int, n *parser.CallTemplateExpression) (err error) {
	if _, err = g.w.WriteIndent(indentLevel, `ghtmx_7f3b9d1a_Err = `); err != nil {
		return err
	}
	// Template expression.
	var r parser.Range
	if r, err = g.w.Write(n.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(n.Expression, r)
	// .Render(ctx, ghtmx_7f3b9d1a_Buffer)
	if _, err = g.w.Write(".Render(ctx, ghtmx_7f3b9d1a_Buffer)\n"); err != nil {
		return err
	}
	if err = g.writeErrorHandler(indentLevel); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeForExpression(indentLevel int, n *parser.ForExpression, next parser.Node) (err error) {
	var r parser.Range
	// for
	if _, err = g.w.WriteIndent(indentLevel, `for `); err != nil {
		return err
	}
	// i, v := range p.Stuff
	if r, err = g.w.Write(n.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(n.Expression, r)
	// {
	if _, err = g.w.Write(` {` + "\n"); err != nil {
		return err
	}
	// Children.
	indentLevel++
	if err = g.writeNodes(indentLevel, stripLeadingAndTrailingWhitespace(n.Children), next); err != nil {
		return err
	}
	indentLevel--
	// }
	if _, err = g.w.WriteIndent(indentLevel, `}`+"\n"); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeErrorHandler(indentLevel int) (err error) {
	_, err = g.w.WriteIndent(indentLevel, "if ghtmx_7f3b9d1a_Err != nil {\n")
	if err != nil {
		return err
	}
	indentLevel++
	_, err = g.w.WriteIndent(indentLevel, "return ghtmx_7f3b9d1a_Err\n")
	if err != nil {
		return err
	}
	indentLevel--
	_, err = g.w.WriteIndent(indentLevel, "}\n")
	if err != nil {
		return err
	}
	return err
}

func (g *generator) writeExpressionErrorHandler(indentLevel int, expression parser.Expression) (err error) {
	_, err = g.w.WriteIndent(indentLevel, "if ghtmx_7f3b9d1a_Err != nil {\n")
	if err != nil {
		return err
	}
	indentLevel++
	line := int(expression.Range.To.Line + 1)
	col := int(expression.Range.To.Col)
	_, err = g.w.WriteIndent(indentLevel, "return	ghtmx.Error{Err: ghtmx_7f3b9d1a_Err, FileName: "+createGoString(g.options.FileName)+", Line: "+strconv.Itoa(line)+", Col: "+strconv.Itoa(col)+"}\n")
	if err != nil {
		return err
	}
	indentLevel--
	_, err = g.w.WriteIndent(indentLevel, "}\n")
	if err != nil {
		return err
	}
	return err
}

func (g *generator) writeElement(indentLevel int, n *parser.Element) (err error) {
	if len(n.Attributes) == 0 {
		// <div>
		if _, err = g.w.WriteStringLiteral(indentLevel, fmt.Sprintf(`<%s>`, html.EscapeString(n.Name))); err != nil {
			return err
		}
	} else {
		attrs := parser.CopyAttributes(n.Attributes)
		// <style type="text/css"></style>
		if err = g.writeElementCSS(indentLevel, attrs); err != nil {
			return err
		}
		// <script></script>
		if err = g.writeElementScript(indentLevel, attrs); err != nil {
			return err
		}
		// <div
		if _, err = g.w.WriteStringLiteral(indentLevel, fmt.Sprintf(`<%s`, html.EscapeString(n.Name))); err != nil {
			return err
		}
		if err = g.writeElementAttributes(indentLevel, n.Name, attrs); err != nil {
			return err
		}
		// >
		if _, err = g.w.WriteStringLiteral(indentLevel, `>`); err != nil {
			return err
		}
	}
	// Skip children and close tag for void elements.
	if n.IsVoidElement() && len(n.Children) == 0 {
		return nil
	}
	// Children.
	if err = g.writeNodes(indentLevel, stripWhitespace(n.Children), nil); err != nil {
		return err
	}
	// </div>
	if _, err = g.w.WriteStringLiteral(indentLevel, fmt.Sprintf(`</%s>`, html.EscapeString(n.Name))); err != nil {
		return err
	}
	return err
}

func (g *generator) writeAttributeCSS(indentLevel int, attr *parser.ExpressionAttribute) (result *parser.ExpressionAttribute, ok bool, err error) {
	var r parser.Range
	name := html.EscapeString(attr.Key.String())
	if name != "class" {
		ok = false
		return
	}
	// Create a class name for the style.
	// The expression can either be expecting a ghtmx.Classes call, or an expression that returns
	// var ghtmx_7f3b9d1a_CSSClasses = []any{
	classesName := g.createVariableName()
	if _, err = g.w.WriteIndent(indentLevel, "var "+classesName+" = []any{"); err != nil {
		return
	}
	// p.Name()
	if r, err = g.w.Write(attr.Expression.Value); err != nil {
		return
	}
	g.sourceMap.Add(attr.Expression, r)
	// }\n
	if _, err = g.w.Write("}\n"); err != nil {
		return
	}
	// Render the CSS before the element if required.
	// ghtmx_7f3b9d1a_Err = ghtmx.RenderCSSItems(ctx, ghtmx_7f3b9d1a_Buffer, ghtmx_7f3b9d1a_CSSClasses...)
	if _, err = g.w.WriteIndent(indentLevel, "ghtmx_7f3b9d1a_Err = ghtmx.RenderCSSItems(ctx, ghtmx_7f3b9d1a_Buffer, "+classesName+"...)\n"); err != nil {
		return
	}
	if err = g.writeErrorHandler(indentLevel); err != nil {
		return
	}
	// Rewrite the ExpressionAttribute to point at the new variable.
	newAttr := &parser.ExpressionAttribute{
		Key: attr.Key,
		Expression: parser.Expression{
			Value: "ghtmx.CSSClasses(" + classesName + ").String()",
		},
	}
	return newAttr, true, nil
}

func (g *generator) writeAttributesCSS(indentLevel int, attrs []parser.Attribute) (err error) {
	for i, attr := range attrs {
		if attr, ok := attr.(*parser.ExpressionAttribute); ok {
			attr, ok, err = g.writeAttributeCSS(indentLevel, attr)
			if err != nil {
				return err
			}
			if ok {
				attrs[i] = attr
			}
		}
		if cattr, ok := attr.(*parser.ConditionalAttribute); ok {
			err = g.writeAttributesCSS(indentLevel, cattr.Then)
			if err != nil {
				return err
			}
			err = g.writeAttributesCSS(indentLevel, cattr.Else)
			if err != nil {
				return err
			}
			attrs[i] = cattr
		}
	}
	return nil
}

func (g *generator) writeElementCSS(indentLevel int, attrs []parser.Attribute) (err error) {
	return g.writeAttributesCSS(indentLevel, attrs)
}

func isScriptAttribute(name string) bool {
	for _, prefix := range []string{"on", "hx-on:"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func (g *generator) writeElementScript(indentLevel int, attrs []parser.Attribute) (err error) {
	var scriptExpressions []string
	for _, attr := range attrs {
		scriptExpressions = append(scriptExpressions, getAttributeScripts(attr)...)
	}
	if len(scriptExpressions) == 0 {
		return
	}
	// Render the scripts before the element if required.
	// ghtmx_7f3b9d1a_Err = ghtmx.RenderScriptItems(ctx, ghtmx_7f3b9d1a_Buffer, a, b, c)
	if _, err = g.w.WriteIndent(indentLevel, "ghtmx_7f3b9d1a_Err = ghtmx.RenderScriptItems(ctx, ghtmx_7f3b9d1a_Buffer, "+strings.Join(scriptExpressions, ", ")+")\n"); err != nil {
		return err
	}
	if err = g.writeErrorHandler(indentLevel); err != nil {
		return err
	}
	return err
}

func getAttributeScripts(attr parser.Attribute) (scripts []string) {
	if attr, ok := attr.(*parser.ConditionalAttribute); ok {
		for _, attr := range attr.Then {
			scripts = append(scripts, getAttributeScripts(attr)...)
		}
		for _, attr := range attr.Else {
			scripts = append(scripts, getAttributeScripts(attr)...)
		}
	}
	if attr, ok := attr.(*parser.ExpressionAttribute); ok {
		name := html.EscapeString(attr.Key.String())
		if isScriptAttribute(name) {
			scripts = append(scripts, attr.Expression.Value)
		}
	}
	return scripts
}

func (g *generator) writeAttributeKey(indentLevel int, attr parser.AttributeKey) (err error) {
	if attr, ok := attr.(parser.ConstantAttributeKey); ok {
		name := html.EscapeString(attr.Name)
		if _, err = g.w.WriteStringLiteral(indentLevel, fmt.Sprintf(` %s`, name)); err != nil {
			return err
		}
		return nil
	}
	if attr, ok := attr.(parser.ExpressionAttributeKey); ok {
		var r parser.Range
		vn := g.createVariableName()
		// var vn string
		if _, err = g.w.WriteIndent(indentLevel, "var "+vn+" string\n"); err != nil {
			return err
		}
		// vn, ghtmx_7f3b9d1a_Err = ghtmx.JoinStringErrs(
		if _, err = g.w.WriteIndent(indentLevel, vn+", ghtmx_7f3b9d1a_Err = ghtmx.JoinStringErrs("); err != nil {
			return err
		}
		// p.Name()
		if r, err = g.w.Write(attr.Expression.Value); err != nil {
			return err
		}
		g.sourceMap.Add(attr.Expression, r)
		// )
		if _, err = g.w.Write(")\n"); err != nil {
			return err
		}
		// Attribute expression error handler.
		err = g.writeExpressionErrorHandler(indentLevel, attr.Expression)
		if err != nil {
			return err
		}

		// _, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString(vn)
		if _, err = g.w.WriteIndent(indentLevel, "_, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString(ghtmx.EscapeString(` `+"+vn+"))\n"); err != nil {
			return err
		}
		return g.writeErrorHandler(indentLevel)
	}
	return fmt.Errorf("unknown attribute key type %T", attr)
}

func (g *generator) writeBoolConstantAttribute(indentLevel int, attr *parser.BoolConstantAttribute) (err error) {
	return g.writeAttributeKey(indentLevel, attr.Key)
}

func (g *generator) writeConstantAttribute(indentLevel int, attr *parser.ConstantAttribute) (err error) {
	if err = g.writeAttributeKey(indentLevel, attr.Key); err != nil {
		return err
	}
	quote := `"`
	if attr.SingleQuote {
		quote = "'"
	}

	// Strip superfluous whitespace from class attributes.
	attrValue := attr.Value
	if k, ok := attr.Key.(parser.ConstantAttributeKey); ok && strings.EqualFold(k.Name, "class") {
		attrValue = strings.Join(strings.Fields(attrValue), " ")
	}

	value := escapeQuotes("=" + quote + attrValue + quote)
	if _, err = g.w.WriteStringLiteral(indentLevel, value); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeBoolExpressionAttribute(indentLevel int, attr *parser.BoolExpressionAttribute) (err error) {
	// if
	if _, err = g.w.WriteIndent(indentLevel, `if `); err != nil {
		return err
	}
	// x == y
	var r parser.Range
	if r, err = g.w.Write(attr.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(attr.Expression, r)
	// {
	if _, err = g.w.Write(` {` + "\n"); err != nil {
		return err
	}
	{
		indentLevel++
		if err = g.writeAttributeKey(indentLevel, attr.Key); err != nil {
			return err
		}
		indentLevel--
	}
	// }
	if _, err = g.w.WriteIndent(indentLevel, `}`+"\n"); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeExpressionAttributeValueURL(indentLevel int, attr *parser.ExpressionAttribute) (err error) {
	vn := g.createVariableName()
	// var vn ghtmx.SafeURL
	if _, err = g.w.WriteIndent(indentLevel, "var "+vn+" ghtmx.SafeURL\n"); err != nil {
		return err
	}
	// vn, ghtmx_7f3b9d1a_Err = ghtmx.JoinURLErrs(
	if _, err = g.w.WriteIndent(indentLevel, vn+", ghtmx_7f3b9d1a_Err = ghtmx.JoinURLErrs("); err != nil {
		return err
	}
	// p.Name()
	var r parser.Range
	if r, err = g.w.Write(attr.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(attr.Expression, r)
	// )
	if _, err = g.w.Write(")\n"); err != nil {
		return err
	}
	// Attribute expression error handler.
	err = g.writeExpressionErrorHandler(indentLevel, attr.Expression)
	if err != nil {
		return err
	}
	// _, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString(vn)
	if _, err = g.w.WriteIndent(indentLevel, "_, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString(ghtmx.EscapeString("+vn+"))\n"); err != nil {
		return err
	}
	return g.writeErrorHandler(indentLevel)
}

// isHxVerbAttribute reports whether the attribute is one of the five
// route-aware htmx verb attributes.
func isHxVerbAttribute(key parser.AttributeKey) bool {
	k, ok := key.(parser.ConstantAttributeKey)
	if !ok {
		return false
	}
	switch k.Name {
	case "hx-get", "hx-post", "hx-put", "hx-patch", "hx-delete":
		return true
	}
	return false
}

// writeExpressionAttributeValueHxURL emits a route-aware hx-* verb
// attribute value (FR-023, S1.1). The expression must produce a
// ghtmx.SafeURL — generated route constructors do, and anything else is a
// Go type error, which keeps the escaping contract enforceable even if
// analysis were bypassed. URL escaping happened inside the constructor,
// percent-encoded per path position; HTML attribute-value escaping
// composes on top here. The context is fixed by the engine and is not
// selectable at the binding site.
//
// WithEditorBindings drops that type expectation, and only that: the
// language server generates without a route table, so an unlowered
// symbol binding would otherwise fail to type-check. Nothing that is
// compiled for real is generated with the option set.
func (g *generator) writeExpressionAttributeValueHxURL(indentLevel int, attr *parser.ExpressionAttribute) (err error) {
	vn := g.createVariableName()
	// var vn ghtmx.SafeURL = expr, or, for the editor, the expression on
	// its own so no type is demanded of it (see WithEditorBindings).
	decl := "var " + vn + " ghtmx.SafeURL = "
	if g.options.EditorBindings && relaxHxURLType(attr.Expression.Value, g.options.EditorGeneratedPkg) {
		if _, err = g.w.WriteIndent(indentLevel, "var "+vn+" ghtmx.SafeURL\n"); err != nil {
			return err
		}
		decl = "_ = "
	}
	if _, err = g.w.WriteIndent(indentLevel, decl); err != nil {
		return err
	}
	var r parser.Range
	if r, err = g.w.Write(attr.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(attr.Expression, r)
	if _, err = g.w.Write("\n"); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(indentLevel, "_, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString(ghtmx.EscapeString(string("+vn+")))\n"); err != nil {
		return err
	}
	return g.writeErrorHandler(indentLevel)
}

func (g *generator) writeExpressionAttributeValueScript(indentLevel int, attr *parser.ExpressionAttribute) (err error) {
	// It's a JavaScript handler, and requires special handling, because we expect a JavaScript expression.
	vn := g.createVariableName()
	// var vn ghtmx.ComponentScript =
	if _, err = g.w.WriteIndent(indentLevel, "var "+vn+" ghtmx.ComponentScript = "); err != nil {
		return err
	}
	// p.Name()
	var r parser.Range
	if r, err = g.w.Write(attr.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(attr.Expression, r)
	if _, err = g.w.Write("\n"); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(indentLevel, "_, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString("+vn+".Call)\n"); err != nil {
		return err
	}
	return g.writeErrorHandler(indentLevel)
}

func (g *generator) writeExpressionAttributeValueDefault(indentLevel int, attr *parser.ExpressionAttribute) (err error) {
	var r parser.Range
	vn := g.createVariableName()
	// var vn string
	if _, err = g.w.WriteIndent(indentLevel, "var "+vn+" string\n"); err != nil {
		return err
	}
	// vn, ghtmx_7f3b9d1a_Err = ghtmx.ResolveAttributeValue(
	if _, err = g.w.WriteIndent(indentLevel, vn+", ghtmx_7f3b9d1a_Err = ghtmx.ResolveAttributeValue("); err != nil {
		return err
	}
	// p.Name()
	if r, err = g.w.Write(attr.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(attr.Expression, r)
	// )
	if _, err = g.w.Write(")\n"); err != nil {
		return err
	}
	// Attribute expression error handler.
	err = g.writeExpressionErrorHandler(indentLevel, attr.Expression)
	if err != nil {
		return err
	}

	// _, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString(vn)
	if _, err = g.w.WriteIndent(indentLevel, "_, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString("+vn+")\n"); err != nil {
		return err
	}
	return g.writeErrorHandler(indentLevel)
}

func (g *generator) writeExpressionAttributeValueStyle(indentLevel int, attr *parser.ExpressionAttribute) (err error) {
	var r parser.Range
	vn := g.createVariableName()
	// var vn string
	if _, err = g.w.WriteIndent(indentLevel, "var "+vn+" string\n"); err != nil {
		return err
	}
	// vn, ghtmx_7f3b9d1a_Err = ghtmxruntime.SanitizeStyleAttributeValues(
	if _, err = g.w.WriteIndent(indentLevel, vn+", ghtmx_7f3b9d1a_Err = ghtmxruntime.SanitizeStyleAttributeValues("); err != nil {
		return err
	}
	// value
	if r, err = g.w.Write(attr.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(attr.Expression, r)
	// )
	if _, err = g.w.Write(")\n"); err != nil {
		return err
	}
	// Attribute expression error handler.
	err = g.writeExpressionErrorHandler(indentLevel, attr.Expression)
	if err != nil {
		return err
	}

	// _, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString(ghtmx.EscapeString(vn))
	if _, err = g.w.WriteIndent(indentLevel, "_, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString(ghtmx.EscapeString("+vn+"))\n"); err != nil {
		return err
	}
	return g.writeErrorHandler(indentLevel)
}

func (g *generator) writeExpressionAttribute(indentLevel int, elementName string, attr *parser.ExpressionAttribute) (err error) {
	if err = g.writeAttributeKey(indentLevel, attr.Key); err != nil {
		return err
	}
	// ="
	if _, err = g.w.WriteStringLiteral(indentLevel, `=\"`); err != nil {
		return err
	}
	attrKey := html.EscapeString(attr.Key.String())
	// Value.
	if isHxVerbAttribute(attr.Key) {
		if err := g.writeExpressionAttributeValueHxURL(indentLevel, attr); err != nil {
			return err
		}
	} else if isExpressionAttributeValueURL(elementName, attrKey) {
		if err := g.writeExpressionAttributeValueURL(indentLevel, attr); err != nil {
			return err
		}
	} else if isScriptAttribute(attrKey) {
		if err := g.writeExpressionAttributeValueScript(indentLevel, attr); err != nil {
			return err
		}
	} else if attrKey == "style" {
		if err := g.writeExpressionAttributeValueStyle(indentLevel, attr); err != nil {
			return err
		}
	} else {
		if err := g.writeExpressionAttributeValueDefault(indentLevel, attr); err != nil {
			return err
		}
	}
	// Close quote.
	if _, err = g.w.WriteStringLiteral(indentLevel, `\"`); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeSpreadAttributes(indentLevel int, attr *parser.SpreadAttributes) (err error) {
	// ghtmx.RenderAttributes(ctx, w, spreadAttrs)
	if _, err = g.w.WriteIndent(indentLevel, `ghtmx_7f3b9d1a_Err = ghtmx.RenderAttributes(ctx, ghtmx_7f3b9d1a_Buffer, `); err != nil {
		return err
	}
	// spreadAttrs
	var r parser.Range
	if r, err = g.w.Write(attr.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(attr.Expression, r)
	// )
	if _, err = g.w.Write(")\n"); err != nil {
		return err
	}
	if err = g.writeErrorHandler(indentLevel); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeConditionalAttribute(indentLevel int, elementName string, attr *parser.ConditionalAttribute) (err error) {
	// if
	if _, err = g.w.WriteIndent(indentLevel, `if `); err != nil {
		return err
	}
	// x == y
	var r parser.Range
	if r, err = g.w.Write(attr.Expression.Value); err != nil {
		return err
	}
	g.sourceMap.Add(attr.Expression, r)
	// {
	if _, err = g.w.Write(` {` + "\n"); err != nil {
		return err
	}
	{
		indentLevel++
		if err = g.writeElementAttributes(indentLevel, elementName, attr.Then); err != nil {
			return err
		}
		indentLevel--
	}
	if len(attr.Else) > 0 {
		// } else {
		if _, err = g.w.WriteIndent(indentLevel, `} else {`+"\n"); err != nil {
			return err
		}
		{
			indentLevel++
			if err = g.writeElementAttributes(indentLevel, elementName, attr.Else); err != nil {
				return err
			}
			indentLevel--
		}
	}
	// }
	if _, err = g.w.WriteIndent(indentLevel, `}`+"\n"); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeElementAttributes(indentLevel int, name string, attrs []parser.Attribute) (err error) {
	for _, attr := range attrs {
		switch attr := attr.(type) {
		case *parser.BoolConstantAttribute:
			err = g.writeBoolConstantAttribute(indentLevel, attr)
		case *parser.ConstantAttribute:
			err = g.writeConstantAttribute(indentLevel, attr)
		case *parser.BoolExpressionAttribute:
			err = g.writeBoolExpressionAttribute(indentLevel, attr)
		case *parser.ExpressionAttribute:
			err = g.writeExpressionAttribute(indentLevel, name, attr)
		case *parser.SpreadAttributes:
			err = g.writeSpreadAttributes(indentLevel, attr)
		case *parser.ConditionalAttribute:
			err = g.writeConditionalAttribute(indentLevel, name, attr)
		case *parser.AttributeComment:
			continue
		default:
			err = fmt.Errorf("unknown attribute type %T", attr)
		}
	}
	return
}

func (g *generator) writeRawElement(indentLevel int, n *parser.RawElement) (err error) {
	if len(n.Attributes) == 0 {
		// <div>
		if _, err = g.w.WriteStringLiteral(indentLevel, fmt.Sprintf(`<%s>`, html.EscapeString(n.Name))); err != nil {
			return err
		}
	} else {
		// <script></script>
		if err = g.writeElementScript(indentLevel, n.Attributes); err != nil {
			return err
		}
		// <div
		if _, err = g.w.WriteStringLiteral(indentLevel, fmt.Sprintf(`<%s`, html.EscapeString(n.Name))); err != nil {
			return err
		}
		if err = g.writeElementAttributes(indentLevel, n.Name, n.Attributes); err != nil {
			return err
		}
		// >
		if _, err = g.w.WriteStringLiteral(indentLevel, `>`); err != nil {
			return err
		}
	}
	// Contents.
	if err = g.writeText(indentLevel, &parser.Text{Value: n.Contents}); err != nil {
		return err
	}
	// </div>
	if _, err = g.w.WriteStringLiteral(indentLevel, fmt.Sprintf(`</%s>`, html.EscapeString(n.Name))); err != nil {
		return err
	}
	return err
}

func (g *generator) writeScriptElement(indentLevel int, n *parser.ScriptElement) (err error) {
	if len(n.Attributes) == 0 {
		// <div>
		if _, err = g.w.WriteStringLiteral(indentLevel, `<script>`); err != nil {
			return err
		}
	} else {
		// <script></script>
		if err = g.writeElementScript(indentLevel, n.Attributes); err != nil {
			return err
		}
		// <div
		if _, err = g.w.WriteStringLiteral(indentLevel, "<script"); err != nil {
			return err
		}
		if err = g.writeElementAttributes(indentLevel, "script", n.Attributes); err != nil {
			return err
		}
		// >
		if _, err = g.w.WriteStringLiteral(indentLevel, `>`); err != nil {
			return err
		}
	}
	// Contents.
	for _, c := range n.Contents {
		if err = g.writeScriptContents(indentLevel, c); err != nil {
			return err
		}
	}
	// </div>
	if _, err = g.w.WriteStringLiteral(indentLevel, "</script>"); err != nil {
		return err
	}
	return err
}

func (g *generator) writeScriptContents(indentLevel int, c parser.ScriptContents) (err error) {
	if c.Value != nil {
		if *c.Value == "" {
			return nil
		}
		// This is a JS expression and can be written directly to the output.
		return g.writeText(indentLevel, &parser.Text{Value: *c.Value})
	}
	if c.GoCode != nil {
		// This is a Go code block. The code needs to be evaluated, and the result written to the output.
		// The variable is JSON encoded to ensure that it is safe to use within a script tag.
		var r parser.Range
		vn := g.createVariableName()
		// Here, we need to get the result, which might be any type. We can use ghtmx.ScriptContent to get the result.
		// vn, ghtmx_7f3b9d1a_Err := ghtmxruntime.ScriptContent(
		fnCall := "ghtmxruntime.ScriptContentOutsideStringLiteral"
		if c.InsideStringLiteral {
			fnCall = "ghtmxruntime.ScriptContentInsideStringLiteral"
		}
		if _, err = g.w.WriteIndent(indentLevel, vn+", ghtmx_7f3b9d1a_Err := "+fnCall+"("); err != nil {
			return err
		}
		// p.Name()
		if r, err = g.w.Write(c.GoCode.Expression.Value); err != nil {
			return err
		}
		g.sourceMap.Add(c.GoCode.Expression, r)
		// )
		if _, err = g.w.Write(")\n"); err != nil {
			return err
		}

		// Expression error handler.
		err = g.writeExpressionErrorHandler(indentLevel, c.GoCode.Expression)
		if err != nil {
			return err
		}

		// _, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString(jvn)
		if _, err = g.w.WriteIndent(indentLevel, "_, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString("+vn+")\n"); err != nil {
			return err
		}
		if err = g.writeErrorHandler(indentLevel); err != nil {
			return err
		}

		// Write any trailing space.
		if c.GoCode.TrailingSpace != "" {
			if err = g.writeText(indentLevel, &parser.Text{Value: string(c.GoCode.TrailingSpace)}); err != nil {
				return err
			}
		}

		return nil
	}
	return errors.New("unknown script content")
}

func (g *generator) writeComment(indentLevel int, c *parser.HTMLComment) (err error) {
	// <!--
	if _, err = g.w.WriteStringLiteral(indentLevel, "<!--"); err != nil {
		return err
	}
	// Contents.
	if err = g.writeText(indentLevel, &parser.Text{Value: c.Contents}); err != nil {
		return err
	}
	// -->
	if _, err = g.w.WriteStringLiteral(indentLevel, "-->"); err != nil {
		return err
	}
	return err
}

func (g *generator) createVariableName() string {
	g.variableID++
	return "ghtmx_7f3b9d1a_Var" + strconv.Itoa(g.variableID)
}

func (g *generator) writeGoCode(indentLevel int, e parser.Expression) (err error) {
	if strings.TrimSpace(e.Value) == "" {
		return
	}
	var r parser.Range
	if r, err = g.w.WriteIndent(indentLevel, e.Value+"\n"); err != nil {
		return err
	}
	g.sourceMap.Add(e, r)
	return nil
}

func (g *generator) writeStringExpression(indentLevel int, e parser.Expression) (err error) {
	if strings.TrimSpace(e.Value) == "" {
		return
	}
	var r parser.Range
	vn := g.createVariableName()
	// var vn string
	if _, err = g.w.WriteIndent(indentLevel, "var "+vn+" string\n"); err != nil {
		return err
	}
	// vn, ghtmx_7f3b9d1a_Err = ghtmx.JoinStringErrs(
	if _, err = g.w.WriteIndent(indentLevel, vn+", ghtmx_7f3b9d1a_Err = ghtmx.JoinStringErrs("); err != nil {
		return err
	}
	// p.Name()
	if r, err = g.w.Write(e.Value); err != nil {
		return err
	}
	g.sourceMap.Add(e, r)
	// )
	if _, err = g.w.Write(")\n"); err != nil {
		return err
	}

	// String expression error handler.
	err = g.writeExpressionErrorHandler(indentLevel, e)
	if err != nil {
		return err
	}

	// _, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString(vn)
	if _, err = g.w.WriteIndent(indentLevel, "_, ghtmx_7f3b9d1a_Err = ghtmx_7f3b9d1a_Buffer.WriteString(ghtmx.EscapeString("+vn+"))\n"); err != nil {
		return err
	}
	if err = g.writeErrorHandler(indentLevel); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeWhitespace(indentLevel int, n *parser.Whitespace) (err error) {
	if len(n.Value) == 0 {
		return
	}
	// _, err = ghtmx_7f3b9d1a_Buffer.WriteString(` `)
	if _, err = g.w.WriteStringLiteral(indentLevel, " "); err != nil {
		return err
	}
	return nil
}

func (g *generator) writeText(indentLevel int, n *parser.Text) (err error) {
	_, err = g.w.WriteStringLiteral(indentLevel, escapeQuotes(n.Value))
	return err
}

func createGoString(s string) string {
	var sb strings.Builder
	sb.WriteRune('`')
	sects := strings.Split(s, "`")
	for i, sect := range sects {
		sb.WriteString(sect)
		if len(sects) > i+1 {
			sb.WriteString("` + \"`\" + `")
		}
	}
	sb.WriteRune('`')
	return sb.String()
}

func (g *generator) writeScript(t *parser.ScriptTemplate) error {
	if t == nil {
		return errors.New("script template is nil")
	}
	var r parser.Range
	var tgtSymbolRange parser.Range
	var err error
	var indentLevel int

	// func
	if r, err = g.w.Write("func "); err != nil {
		return err
	}
	tgtSymbolRange.From = r.From
	if r, err = g.w.Write(t.Name.Value); err != nil {
		return err
	}
	g.sourceMap.Add(t.Name, r)
	// (
	if _, err = g.w.Write("("); err != nil {
		return err
	}
	// Write parameters.
	if r, err = g.w.Write(t.Parameters.Value); err != nil {
		return err
	}
	g.sourceMap.Add(t.Parameters, r)
	// ) ghtmx.ComponentScript {
	if _, err = g.w.Write(") ghtmx.ComponentScript {\n"); err != nil {
		return err
	}
	indentLevel++
	// return ghtmx.ComponentScript{
	if _, err = g.w.WriteIndent(indentLevel, "return ghtmx.ComponentScript{\n"); err != nil {
		return err
	}
	{
		indentLevel++
		fn := functionName(t.Name.Value, t.Value)
		goFn := createGoString(fn)
		// Name: "scriptName",
		if _, err = g.w.WriteIndent(indentLevel, "Name: "+goFn+",\n"); err != nil {
			return err
		}
		// Function: `function scriptName(a, b, c){` + `constantScriptValue` + `}`,
		prefix := "function " + fn + "(" + stripTypes(t.Parameters.Value) + "){"
		body := strings.TrimLeftFunc(t.Value, unicode.IsSpace)
		suffix := "}"
		if _, err = g.w.WriteIndent(indentLevel, "Function: "+createGoString(prefix+body+suffix)+",\n"); err != nil {
			return err
		}
		// Call: ghtmx.SafeScript(scriptName, a, b, c)
		if _, err = g.w.WriteIndent(indentLevel, "Call: ghtmx.SafeScript("+goFn+", "+stripTypes(t.Parameters.Value)+"),\n"); err != nil {
			return err
		}
		// CallInline: ghtmx.SafeScriptInline(scriptName, a, b, c)
		if _, err = g.w.WriteIndent(indentLevel, "CallInline: ghtmx.SafeScriptInline("+goFn+", "+stripTypes(t.Parameters.Value)+"),\n"); err != nil {
			return err
		}
		indentLevel--
	}
	// }
	if _, err = g.w.WriteIndent(indentLevel, "}\n"); err != nil {
		return err
	}
	indentLevel--
	// }
	if r, err = g.w.WriteIndent(indentLevel, "}\n\n"); err != nil {
		return err
	}

	// Keep track of the symbol range for the LSP.
	tgtSymbolRange.To = r.To
	g.sourceMap.AddSymbolRange(t.Range, tgtSymbolRange)

	return nil
}

// writeBlankAssignmentForRuntimeImport writes out a blank identifier assignment.
// This ensures that even if the github.com/go-monolith/ghtmx/runtime package is not used in the generated code,
// the Go compiler will not complain about the unused import.
func (g *generator) writeBlankAssignmentForRuntimeImport() error {
	var err error
	if _, err = g.w.Write("var _ = ghtmxruntime.GeneratedTemplate"); err != nil {
		return err
	}
	return nil
}

func functionName(name string, body string) string {
	h := sha256.New()
	h.Write([]byte(body))
	hp := hex.EncodeToString(h.Sum(nil))[0:4]
	return "__ghtmx_" + name + "_" + hp
}

func stripTypes(parameters string) string {
	variableNames := []string{}
	params := strings.Split(parameters, ",")
	for _, param := range params {
		p := strings.Split(strings.TrimSpace(param), " ")
		variableNames = append(variableNames, strings.TrimSpace(p[0]))
	}
	return strings.Join(variableNames, ", ")
}

func isExpressionAttributeValueURL(elementName, attrName string) bool {
	switch elementName {
	case "a", "link":
		return attrName == "href"
	case "form":
		return attrName == "action"
	case "object":
		return attrName == "data"
	}
	return false
}
