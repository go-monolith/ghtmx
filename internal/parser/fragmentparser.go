package parser

import (
	"github.com/a-h/parse"
)

// fragmentExpression parses the fragment declaration header:
//
//	fragment UserRow(u User) {
type fragmentExpression struct {
	Name       string
	Expression Expression
}

var fragmentExpressionParser = parse.Func(func(pi *parse.Input) (r fragmentExpression, matched bool, err error) {
	start := pi.Index()

	if !peekPrefix(pi, "fragment ") {
		return r, false, nil
	}

	// Once we have the prefix, everything to the brace is Go.
	if r.Name, r.Expression, err = parseGoFuncDecl("fragment", pi); err != nil {
		return r, true, err
	}

	// Eat " {\n".
	if _, matched, err = parse.All(openBraceWithOptionalPadding, parse.StringFrom(parse.Optional(parse.NewLine))).Parse(pi); err != nil || !matched {
		return r, true, parse.Error("fragment: malformed fragment declaration, expected `fragment FragmentName(...) {`", pi.PositionAt(start))
	}

	return r, true, nil
})

// fragmentDeclarationParser parses a full fragment declaration with its
// body (FR-030). It serves both positions: nested in a page template and
// at the top level of a file; the caller records which via TopLevel.
type fragmentDeclarationParser struct{}

var fragmentDeclaration fragmentDeclarationParser

func (p fragmentDeclarationParser) Parse(pi *parse.Input) (n Node, matched bool, err error) {
	start := pi.Position()

	var fe fragmentExpression
	if fe, matched, err = fragmentExpressionParser.Parse(pi); err != nil || !matched {
		return n, matched, err
	}
	r := &FragmentDeclaration{
		Name:       fe.Name,
		Expression: fe.Expression,
	}
	defer func() {
		r.Range = NewRange(start, pi.Position())
	}()

	var nodes Nodes
	nodes, matched, err = newTemplateNodeParser(closeBraceWithOptionalPadding, "fragment closing brace").Parse(pi)
	if err != nil {
		// Partial AST for the LSP.
		r.Children = nodes.Nodes
		return r, true, err
	}
	if !matched {
		return r, true, parse.Error("fragment: expected nodes in fragment body, but found none", pi.Position())
	}
	r.Children = nodes.Nodes

	// Eat any whitespace.
	if _, _, err = parse.OptionalWhitespace.Parse(pi); err != nil {
		return r, true, err
	}

	// Try for }
	if _, matched, err = closeBraceWithOptionalPadding.Parse(pi); err != nil || !matched {
		return r, true, parse.Error("fragment: missing closing brace", pi.Position())
	}

	return r, true, nil
}
