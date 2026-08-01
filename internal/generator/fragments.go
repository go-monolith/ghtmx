package generator

import (
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	"strings"

	parser "github.com/go-monolith/ghtmx/internal/parser"
)

// Fragment code generation (FR-031, FR-032, solution design D4): every
// fragment compiles to exactly one unexported shared body function plus
// two thin wrappers over it — the inline wrapper returning ghtmx.Component
// and the standalone wrapper returning ghtmx.Fragment. Because there is
// only one body, the inline and standalone paths cannot diverge: the
// byte-identical guarantee is structural, not tested-for. A nested
// declaration site calls the body directly with the enclosing scope's
// variables; a reference from any page calls the inline wrapper, which
// calls the same body.

// fragmentBodyName returns the unexported shared body function name.
func fragmentBodyName(name string) string {
	return "ghtmxFragmentBody_" + name
}

// fragmentSignature splits a declaration expression like
// "Name(u User, n int)" into the parameter list source "(u User, n int)"
// and the ordered call-position arguments forwarding those parameters (a
// variadic parameter forwards as "name..."). Parameters must be plain
// named parameters: the names double as the call-site argument list, so
// unnamed or blank parameters cannot be forwarded, and type parameters
// have no inference site on the body call.
func fragmentSignature(exprValue string) (params string, names []string, err error) {
	i := strings.IndexByte(exprValue, '(')
	if i < 0 {
		return "", nil, fmt.Errorf("fragment declaration %q has no parameter list", exprValue)
	}
	if strings.Contains(exprValue[:i], "[") {
		return "", nil, fmt.Errorf("fragment declaration %q is generic: fragments do not support type parameters", exprValue)
	}
	params = exprValue[i:]
	expr, err := goparser.ParseExpr("func" + params)
	if err != nil {
		return "", nil, fmt.Errorf("fragment declaration %q has an invalid parameter list: %w", exprValue, err)
	}
	ft, ok := expr.(*goast.FuncType)
	if !ok || ft.Params == nil {
		return "", nil, fmt.Errorf("fragment declaration %q has an invalid parameter list", exprValue)
	}
	for _, field := range ft.Params.List {
		if len(field.Names) == 0 {
			return "", nil, fmt.Errorf("fragment declaration %q has an unnamed parameter: fragment parameters must be named", exprValue)
		}
		_, variadic := field.Type.(*goast.Ellipsis)
		for _, n := range field.Names {
			if n.Name == "_" {
				return "", nil, fmt.Errorf("fragment declaration %q has a blank parameter: fragment parameters must be named", exprValue)
			}
			if variadic {
				names = append(names, n.Name+"...")
				continue
			}
			names = append(names, n.Name)
		}
	}
	return params, names, nil
}

// writeFragmentBodyCall emits the direct shared-body call used at a nested
// declaration site: parameters are bound by name from the enclosing scope,
// so existence and types are checked by the Go compiler at this call (D9).
func (g *generator) writeFragmentBodyCall(indentLevel int, f *parser.FragmentDeclaration) error {
	_, names, err := fragmentSignature(f.Expression.Value)
	if err != nil {
		return err
	}
	args := ""
	if len(names) > 0 {
		args = ", " + strings.Join(names, ", ")
	}
	if _, err := g.w.WriteIndent(indentLevel, "ghtmx_7f3b9d1a_Err = "+fragmentBodyName(f.Name)+"(ghtmxruntime.GeneratedComponentInput{Context: ctx, Writer: ghtmx_7f3b9d1a_Buffer}"+args+")\n"); err != nil {
		return err
	}
	return g.writeErrorHandler(indentLevel)
}

// writeFragmentDeclaration emits the shared body function and the two
// wrappers for one fragment.
func (g *generator) writeFragmentDeclaration(f *parser.FragmentDeclaration) error {
	params, names, err := fragmentSignature(f.Expression.Value)
	if err != nil {
		return err
	}
	args := ""
	if len(names) > 0 {
		args = ", " + strings.Join(names, ", ")
	}
	body := fragmentBodyName(f.Name)

	var r parser.Range
	var tgtSymbolRange parser.Range

	// Shared body: emitted exactly once per fragment.
	if r, err = g.w.Write("// " + body + " is the shared body of fragment " + f.Name + ": both entry\n// points and every declaration site execute it, so the render modes cannot\n// diverge.\n"); err != nil {
		return err
	}
	tgtSymbolRange.From = r.From
	if _, err = g.w.Write("func " + body + "(ghtmx_7f3b9d1a_Input ghtmxruntime.GeneratedComponentInput"); err != nil {
		return err
	}
	declared := strings.TrimPrefix(strings.TrimSuffix(params, ")"), "(")
	if strings.TrimSpace(declared) != "" {
		declared = ", " + declared
	}
	if _, err = g.w.Write(declared + ") (ghtmx_7f3b9d1a_Err error) {\n"); err != nil {
		return err
	}
	indentLevel := 1
	if _, err = g.w.WriteIndent(indentLevel, "ghtmx_7f3b9d1a_W, ctx := ghtmx_7f3b9d1a_Input.Writer, ghtmx_7f3b9d1a_Input.Context\n"); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(indentLevel, "if ghtmx_7f3b9d1a_CtxErr := ctx.Err(); ghtmx_7f3b9d1a_CtxErr != nil {\n"); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(indentLevel+1, "return ghtmx_7f3b9d1a_CtxErr\n"); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(indentLevel, "}\n"); err != nil {
		return err
	}
	if err := g.writeTemplBuffer(indentLevel); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(indentLevel, "ctx = ghtmx.InitializeContext(ctx)\n"); err != nil {
		return err
	}
	// Fragments take no children (GHTMX-E0302 guards the construct): clear
	// any children a caller put in ctx so both render modes see the same
	// context, and isolate the enclosing template's children variable.
	if _, err = g.w.WriteIndent(indentLevel, "ctx = ghtmx.ClearChildren(ctx)\n"); err != nil {
		return err
	}
	previousChildrenVar := g.childrenVar
	g.childrenVar = ""
	if err = g.writeNodes(indentLevel, stripWhitespace(f.Children), nil); err != nil {
		return err
	}
	g.childrenVar = previousChildrenVar
	if _, err = g.w.WriteIndent(indentLevel, "return nil\n"); err != nil {
		return err
	}
	if _, err = g.w.Write("}\n\n"); err != nil {
		return err
	}

	// Inline wrapper: the fragment as an ordinary component, used at
	// reference sites (@Name(args)) and page composition.
	if _, err = g.w.Write("// " + f.Name + " renders the fragment inline as a component.\n"); err != nil {
		return err
	}
	if _, err = g.w.Write("func "); err != nil {
		return err
	}
	// Map the signature to the declaration for diagnostics and the LSP.
	if r, err = g.w.Write(f.Name + params); err != nil {
		return err
	}
	g.sourceMap.Add(f.Expression, r)
	if _, err = g.w.Write(" ghtmx.Component {\n"); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(1, "return ghtmxruntime.GeneratedTemplate(func(ghtmx_7f3b9d1a_Input ghtmxruntime.GeneratedComponentInput) error {\n"); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(2, "return "+body+"(ghtmx_7f3b9d1a_Input"+args+")\n"); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(1, "})\n"); err != nil {
		return err
	}
	if _, err = g.w.Write("}\n\n"); err != nil {
		return err
	}

	// Standalone wrapper: renders only the fragment, for htmx swaps. It is
	// independent of any page: it closes over nothing but the declared
	// parameters.
	if _, err = g.w.Write("// " + f.Name + "Fragment renders only the fragment, for an htmx swap (FR-031).\n"); err != nil {
		return err
	}
	if _, err = g.w.Write("func " + f.Name + "Fragment" + params + " ghtmx.Fragment {\n"); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(1, "return ghtmxruntime.GeneratedFragment(func(ghtmx_7f3b9d1a_Input ghtmxruntime.GeneratedComponentInput) error {\n"); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(2, "return "+body+"(ghtmx_7f3b9d1a_Input"+args+")\n"); err != nil {
		return err
	}
	if _, err = g.w.WriteIndent(1, "})\n"); err != nil {
		return err
	}
	if r, err = g.w.Write("}\n\n"); err != nil {
		return err
	}
	tgtSymbolRange.To = r.To
	g.sourceMap.AddSymbolRange(f.Range, tgtSymbolRange)
	return nil
}

// flushPendingFragments emits the bodies and wrappers of fragments
// declared inside the template that was just written.
func (g *generator) flushPendingFragments() error {
	// Writing a fragment body can queue further nested fragments; drain
	// until none remain.
	for len(g.pendingFragments) > 0 {
		pending := g.pendingFragments
		g.pendingFragments = nil
		for _, f := range pending {
			if err := g.writeFragmentDeclaration(f); err != nil {
				return err
			}
		}
	}
	return nil
}
