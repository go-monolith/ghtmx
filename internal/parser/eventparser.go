package parser

import (
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	gotypes "go/types"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/a-h/parse"
	"github.com/go-monolith/ghtmx/internal/parser/goexpression"
)

// eventDeclarationParser parses a top-level event declaration (FR-037):
//
//	event UserCreated(id string, name string)
//	event CartCleared()
//
// An event has no body: the declaration is the whole line (a trailing
// line comment is kept). The name must be an exported Go identifier — the
// generated emission symbols (EmitUserCreated, UserCreatedPayload) live in
// the central generated package and are unusable otherwise — and must
// yield a dashed kebab-case wire name (at least two words): a single-word
// wire name would collide with the DOM event namespace and could never be
// distinguished from a native event in hx-on or hx-trigger.
type eventDeclarationParser struct{}

var eventDeclaration eventDeclarationParser

func (p eventDeclarationParser) Parse(pi *parse.Input) (n *EventDeclaration, matched bool, err error) {
	start := pi.Position()
	if !peekPrefix(pi, "event ") {
		return nil, false, nil
	}

	from := pi.Index()
	rest, _ := pi.Peek(-1)
	line := rest
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	sig := strings.TrimPrefix(line, "event ")
	name, expr, err := goexpression.Func("func " + sig + "{}")
	if err != nil {
		return nil, true, parse.Error("event: invalid event declaration, expected `event Name(typed params...)`: "+err.Error(), pi.Position())
	}
	if first, _ := utf8.DecodeRuneInString(name); !unicode.IsUpper(first) {
		return nil, true, parse.Error("event: event names must be exported (start with an upper-case letter): the generated emission symbols are package-level API", pi.Position())
	}
	for _, r := range name {
		if r >= 0x80 {
			return nil, true, parse.Error("event: event names must be ASCII: the wire name travels in an HTTP header, which cannot carry non-ASCII bytes", pi.Position())
		}
	}
	if i := strings.IndexByte(expr, '('); i >= 0 && strings.Contains(expr[:i], "[") {
		return nil, true, parse.Error("event: event declarations do not support type parameters: an HX-Trigger payload is serialized, so its types must be concrete", pi.Position())
	}
	if err := checkEventParams(expr); err != nil {
		return nil, true, parse.Error("event: "+err.Error(), pi.Position())
	}
	if !hasWireWordBoundary(name) {
		return nil, true, parse.Error("event: event name "+name+" yields the single-word wire name "+strings.ToLower(name)+", which collides with the DOM event namespace: use at least two words, e.g. "+name+"Done", pi.Position())
	}

	// Consume through the end of the matched signature, byte-aligned with
	// the source (the signature may carry extra interior whitespace, which
	// the stored expression drops).
	sigStart := strings.Index(line, expr)
	if sigStart < 0 {
		return nil, true, parse.Error("event: invalid event declaration", pi.Position())
	}
	trimmed := strings.TrimLeft(expr, " \t")
	exprStart := sigStart + len(expr) - len(trimmed)
	pi.Take(sigStart + len(expr))
	e := &EventDeclaration{
		Name:       name,
		Expression: NewExpression(trimmed, pi.PositionAt(from+exprStart), pi.Position()),
	}
	fail := func(msg string) (*EventDeclaration, bool, error) {
		// Partial node with a real range for the LSP.
		e.Range = NewRange(start, pi.Position())
		return e, true, parse.Error(msg, pi.Position())
	}

	// Only whitespace or a trailing line comment may follow: events have
	// no body.
	trailing, _ := pi.Peek(-1)
	if i := strings.IndexAny(trailing, "\r\n"); i >= 0 {
		trailing = trailing[:i]
	}
	if t := strings.TrimSpace(trailing); t != "" {
		if !strings.HasPrefix(t, "//") {
			return fail("event: an event declaration has no body: expected the end of the line after `event " + expr + "`")
		}
		e.Comment = t
	}
	pi.Take(len(trailing))
	_, _, _ = parse.OptionalWhitespace.Parse(pi)

	e.Range = NewRange(start, pi.Position())
	return e, true, nil
}

// hasWireWordBoundary reports whether the kebab-case wire name of the
// event name contains a dash — i.e. the name has at least two words.
func hasWireWordBoundary(name string) bool {
	runes := []rune(name)
	for i := 1; i < len(runes); i++ {
		if unicode.IsUpper(runes[i]) &&
			(!unicode.IsUpper(runes[i-1]) || (i+1 < len(runes) && unicode.IsLower(runes[i+1]))) {
			return true
		}
	}
	return false
}

// checkEventParams enforces the payload contract: parameters are named
// with ASCII-letter-initial identifiers (each becomes an exported field of
// the generated payload struct), unique after export, non-variadic, and of
// JSON-serializable builtin shape — the generated package imports nothing
// user-defined, so named or qualified types cannot appear (FR-037).
func checkEventParams(expr string) error {
	i := strings.IndexByte(expr, '(')
	if i < 0 {
		return nil
	}
	parsed, err := goparser.ParseExpr("func" + expr[i:])
	if err != nil {
		return nil // The signature parsed once already; leave errors there.
	}
	ft, ok := parsed.(*goast.FuncType)
	if !ok || ft.Params == nil {
		return nil
	}
	seen := map[string]string{}
	for _, field := range ft.Params.List {
		if len(field.Names) == 0 {
			return fmt.Errorf("payload parameters must be named: each becomes a field of the generated payload struct")
		}
		if _, variadic := field.Type.(*goast.Ellipsis); variadic {
			return fmt.Errorf("payload parameters cannot be variadic: declare a slice parameter instead")
		}
		if err := checkPayloadType(field.Type); err != nil {
			return err
		}
		for _, n := range field.Names {
			first := n.Name[0]
			if n.Name == "_" || !(('a' <= first && first <= 'z') || ('A' <= first && first <= 'Z')) {
				return fmt.Errorf("payload parameter %q must start with an ASCII letter: it becomes an exported field of the generated payload struct", n.Name)
			}
			exported := strings.ToUpper(n.Name[:1]) + n.Name[1:]
			if prev, dup := seen[exported]; dup {
				return fmt.Errorf("payload parameters %q and %q both export as field %s: rename one", prev, n.Name, exported)
			}
			seen[exported] = n.Name
		}
	}
	return nil
}

// payloadBuiltins are the predeclared types allowed in event payloads.
var payloadBuiltins = map[string]bool{
	"bool": true, "string": true, "any": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"float32": true, "float64": true, "byte": true, "rune": true,
}

// checkPayloadType accepts builtin types and composites of builtins.
func checkPayloadType(t goast.Expr) error {
	switch e := t.(type) {
	case *goast.Ident:
		if payloadBuiltins[e.Name] {
			return nil
		}
		return fmt.Errorf("payload type %s is not a builtin: the generated package imports no user code, so payload fields use builtin types (or slices/maps/pointers of them)", e.Name)
	case *goast.SelectorExpr:
		return fmt.Errorf("payload type %s is qualified: the generated package imports no user code, so payload fields use builtin types (or slices/maps/pointers of them)", gotypes.ExprString(e))
	case *goast.ArrayType:
		return checkPayloadType(e.Elt)
	case *goast.MapType:
		if err := checkPayloadType(e.Key); err != nil {
			return err
		}
		return checkPayloadType(e.Value)
	case *goast.StarExpr:
		return checkPayloadType(e.X)
	}
	return fmt.Errorf("payload type %s is not JSON-serializable as a builtin shape", gotypes.ExprString(t))
}
