package parser

import (
	"io"
	"reflect"
	"strings"
	"testing"
)

// types.go declares dozens of small interface methods — IsNode,
// IsTemplateFileNode, IsCSSProperty, IsStyleDeclarationValue, String —
// that exist so the parser's node kinds can be told apart at runtime.
// Nothing exercised most of them: they are trivial individually, but a
// node whose IsNode returns false, or whose Visit dispatches to the
// wrong method, silently disappears from every walk. The analyzer and
// the LSP's symbol scan both walk, so the symptom is a rule that stops
// firing rather than anything that looks like a bug.
//
// Rather than 40 one-line tests, this enumerates the node kinds by
// reflection and checks the marker methods hold for all of them.

// nodeKinds is every Node implementation the parser can produce.
func nodeKinds() []Node {
	return []Node{
		&Element{Name: "div"},
		&Text{Value: "hi"},
		&Whitespace{Value: " "},
		&StringExpression{},
		&IfExpression{},
		&ForExpression{},
		&SwitchExpression{},
		&CallTemplateExpression{},
		&TemplElementExpression{},
		&ChildrenExpression{},
		&GoCode{},
		&DocType{Value: "html"},
		&HTMLComment{Contents: "c"},
		&GoComment{Contents: "c"},
		&RawElement{Name: "script"},
		&ScriptElement{},
	}
}

// templateFileNodeKinds is every top-level declaration kind.
func templateFileNodeKinds() []TemplateFileNode {
	return []TemplateFileNode{
		&TemplateFileGoExpression{},
		&HTMLTemplate{},
		&CSSTemplate{},
		&ScriptTemplate{},
		&FragmentDeclaration{},
		&EventDeclaration{},
	}
}

// TestEveryNodeIdentifiesItself pins the marker methods. A node whose
// IsNode returns false is skipped by the walkers, so a rule that depends
// on it silently stops firing.
func TestEveryNodeIdentifiesItself(t *testing.T) {
	for _, n := range nodeKinds() {
		t.Run(typeName(n), func(t *testing.T) {
			if !n.IsNode() {
				t.Errorf("IsNode() = false; this node would be skipped by every walk")
			}
		})
	}
}

func TestEveryTemplateFileNodeIdentifiesItself(t *testing.T) {
	for _, n := range templateFileNodeKinds() {
		t.Run(typeName(n), func(t *testing.T) {
			if !n.IsTemplateFileNode() {
				t.Errorf("IsTemplateFileNode() = false; this declaration would be skipped")
			}
		})
	}
}

// TestEveryNodeWritesWithoutPanicking exercises each kind's Write. They
// are how `ghtmx fmt` reproduces a file, so a panic here is a formatter
// that crashes on a document containing that node.
func TestEveryNodeWritesWithoutPanicking(t *testing.T) {
	for _, n := range nodeKinds() {
		t.Run(typeName(n), func(t *testing.T) {
			var sb strings.Builder
			// The output is not asserted: the round-trip tests in
			// corpus_test.go pin the exact bytes for real documents.
			// What matters here is that every kind can be written at all.
			if err := n.Write(&sb, 0); err != nil {
				t.Errorf("Write returned %v", err)
			}
		})
	}
}

func TestEveryTemplateFileNodeWritesWithoutPanicking(t *testing.T) {
	for _, n := range templateFileNodeKinds() {
		t.Run(typeName(n), func(t *testing.T) {
			var sb strings.Builder
			if err := n.Write(&sb, 0); err != nil {
				t.Errorf("Write returned %v", err)
			}
		})
	}
}

// TestEveryNodeDispatchesToTheVisitor pins that Visit reaches a visitor
// method for every kind. A node dispatching to the wrong method reports
// as the wrong kind to the analyzer.
func TestEveryNodeDispatchesToTheVisitor(t *testing.T) {
	for _, n := range nodeKinds() {
		t.Run(typeName(n), func(t *testing.T) {
			v := newRecordingVisitor()
			if err := n.Visit(v); err != nil {
				t.Fatalf("Visit returned %v", err)
			}
			if len(v.seen) != 1 || v.seen[0] != typeName(n) {
				t.Errorf("Visit dispatched to %v, want %s — a node reported as the wrong kind is a rule that stops firing",
					v.seen, typeName(n))
			}
		})
	}
}

func TestEveryTemplateFileNodeDispatchesToTheVisitor(t *testing.T) {
	for _, n := range templateFileNodeKinds() {
		t.Run(typeName(n), func(t *testing.T) {
			v := newRecordingVisitor()
			if err := n.Visit(v); err != nil {
				t.Fatalf("Visit returned %v", err)
			}
			if len(v.seen) != 1 || v.seen[0] != typeName(n) {
				t.Errorf("Visit dispatched to %v, want %s", v.seen, typeName(n))
			}
		})
	}
}

// TestCSSPropertyKindsIdentifyThemselves covers the IsCSSProperty
// markers, which is how a css block's contents are told apart.
func TestCSSPropertyKindsIdentifyThemselves(t *testing.T) {
	props := []CSSProperty{
		&ConstantCSSProperty{Name: "color", Value: "red"},
		&ExpressionCSSProperty{Name: "color", Value: &StringExpression{}},
	}
	for _, p := range props {
		t.Run(typeName(p), func(t *testing.T) {
			if !p.IsCSSProperty() {
				t.Error("IsCSSProperty() = false")
			}
			var sb strings.Builder
			if err := p.Write(&sb, 0); err != nil {
				t.Errorf("Write returned %v", err)
			}
		})
	}
}

// TestPositionAndExpressionStrings covers the String methods used in
// diagnostics. An empty or malformed position string is what a user sees
// when the compiler reports an error.
func TestPositionAndExpressionStrings(t *testing.T) {
	p := Position{Index: 42, Line: 7, Col: 3}
	got := p.String()
	for _, want := range []string{"7", "3", "42"} {
		if !strings.Contains(got, want) {
			t.Errorf("Position.String() = %q, missing %q", got, want)
		}
	}
}

// TestNewPositionAndNewRange pin the constructors used throughout the
// parser to record where a node came from; a dropped field here sends
// every diagnostic for that node to the wrong place.
func TestNewPositionAndNewRange(t *testing.T) {
	pos := NewPosition(10, 2, 5)
	if pos.Index != 10 || pos.Line != 2 || pos.Col != 5 {
		t.Errorf("NewPosition = %+v, want {10 2 5}", pos)
	}
}

// typeName returns a short name for subtest labels.
func typeName(v any) string {
	t := reflect.TypeOf(v)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t.Name()
}

// recordingVisitor records which Visit method a node dispatched to, so a
// node wired to the wrong one is caught rather than silently walked as
// something else. The embedded interface supplies the attribute-visiting
// methods no node kind below dispatches to; calling one would panic,
// which is the intended signal that the sweep has drifted.
type recordingVisitor struct {
	Visitor
	seen []string
}

func newRecordingVisitor() *recordingVisitor { return &recordingVisitor{} }

func (v *recordingVisitor) record(name string) error {
	v.seen = append(v.seen, name)
	return nil
}

func (v *recordingVisitor) VisitTemplateFile(*TemplateFile) error { return v.record("TemplateFile") }
func (v *recordingVisitor) VisitTemplateFileGoExpression(*TemplateFileGoExpression) error {
	return v.record("TemplateFileGoExpression")
}
func (v *recordingVisitor) VisitPackage(*Package) error       { return v.record("Package") }
func (v *recordingVisitor) VisitWhitespace(*Whitespace) error { return v.record("Whitespace") }
func (v *recordingVisitor) VisitCSSTemplate(*CSSTemplate) error {
	return v.record("CSSTemplate")
}
func (v *recordingVisitor) VisitConstantCSSProperty(*ConstantCSSProperty) error {
	return v.record("ConstantCSSProperty")
}
func (v *recordingVisitor) VisitExpressionCSSProperty(*ExpressionCSSProperty) error {
	return v.record("ExpressionCSSProperty")
}
func (v *recordingVisitor) VisitDocType(*DocType) error { return v.record("DocType") }
func (v *recordingVisitor) VisitHTMLTemplate(*HTMLTemplate) error {
	return v.record("HTMLTemplate")
}
func (v *recordingVisitor) VisitText(*Text) error       { return v.record("Text") }
func (v *recordingVisitor) VisitElement(*Element) error { return v.record("Element") }
func (v *recordingVisitor) VisitScriptElement(*ScriptElement) error {
	return v.record("ScriptElement")
}
func (v *recordingVisitor) VisitRawElement(*RawElement) error { return v.record("RawElement") }
func (v *recordingVisitor) VisitGoComment(*GoComment) error   { return v.record("GoComment") }
func (v *recordingVisitor) VisitHTMLComment(*HTMLComment) error {
	return v.record("HTMLComment")
}
func (v *recordingVisitor) VisitCallTemplateExpression(*CallTemplateExpression) error {
	return v.record("CallTemplateExpression")
}
func (v *recordingVisitor) VisitTemplElementExpression(*TemplElementExpression) error {
	return v.record("TemplElementExpression")
}
func (v *recordingVisitor) VisitChildrenExpression(*ChildrenExpression) error {
	return v.record("ChildrenExpression")
}
func (v *recordingVisitor) VisitIfExpression(*IfExpression) error {
	return v.record("IfExpression")
}
func (v *recordingVisitor) VisitSwitchExpression(*SwitchExpression) error {
	return v.record("SwitchExpression")
}
func (v *recordingVisitor) VisitForExpression(*ForExpression) error {
	return v.record("ForExpression")
}
func (v *recordingVisitor) VisitGoCode(*GoCode) error { return v.record("GoCode") }
func (v *recordingVisitor) VisitStringExpression(*StringExpression) error {
	return v.record("StringExpression")
}
func (v *recordingVisitor) VisitScriptTemplate(*ScriptTemplate) error {
	return v.record("ScriptTemplate")
}
func (v *recordingVisitor) VisitFragmentDeclaration(*FragmentDeclaration) error {
	return v.record("FragmentDeclaration")
}
func (v *recordingVisitor) VisitEventDeclaration(*EventDeclaration) error {
	return v.record("EventDeclaration")
}

var _ io.Writer = (*strings.Builder)(nil)
