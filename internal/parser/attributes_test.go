package parser

import (
	"strings"
	"testing"
)

// Every attribute kind implements Write, Visit and Copy. Copy in
// particular is load-bearing and easy to get wrong: the analyzer clones
// attributes before rewriting them, so a Copy that shares backing state
// with its original mutates the parse tree the LSP is still serving.
// These sweeps cover all five kinds and both key kinds at once.

// attributeKinds is every Attribute implementation, populated enough to
// be written out.
func attributeKinds() []Attribute {
	constKey := ConstantAttributeKey{Name: "class"}
	exprKey := ExpressionAttributeKey{Expression: Expression{Value: "key"}}
	return []Attribute{
		&BoolConstantAttribute{Key: constKey},
		&ConstantAttribute{Key: constKey, Value: "card"},
		&BoolExpressionAttribute{Key: constKey, Expression: Expression{Value: "ok"}},
		&ExpressionAttribute{Key: constKey, Expression: Expression{Value: "name"}},
		&SpreadAttributes{Expression: Expression{Value: "attrs"}},
		// The expression-key variants take a different Write path.
		&ConstantAttribute{Key: exprKey, Value: "card"},
		&ExpressionAttribute{Key: exprKey, Expression: Expression{Value: "name"}},
	}
}

func TestEveryAttributeWrites(t *testing.T) {
	for i, a := range attributeKinds() {
		t.Run(typeName(a), func(t *testing.T) {
			var sb strings.Builder
			if err := a.Write(&sb, 0); err != nil {
				t.Fatalf("attribute %d: Write returned %v", i, err)
			}
			if sb.Len() == 0 {
				t.Error("Write produced nothing; the attribute would vanish from a formatted file")
			}
		})
	}
}

// TestEveryAttributeCopyIsIndependent is the property that matters: a
// copy sharing state with its original means the analyzer's rewrite
// leaks into the tree the LSP is serving, and the editor starts showing
// attributes the file does not contain.
func TestEveryAttributeCopyIsIndependent(t *testing.T) {
	for _, a := range attributeKinds() {
		t.Run(typeName(a), func(t *testing.T) {
			original := a.Copy()
			clone := a.Copy()

			var before, after strings.Builder
			if err := original.Write(&before, 0); err != nil {
				t.Fatal(err)
			}
			if err := clone.Write(&after, 0); err != nil {
				t.Fatal(err)
			}
			if before.String() != after.String() {
				t.Errorf("two copies of the same attribute differ:\n%q\n%q", before.String(), after.String())
			}
			if original == clone {
				t.Error("Copy returned the same pointer; a rewrite would mutate the original")
			}
		})
	}
}

func TestEveryAttributeDispatchesToTheVisitor(t *testing.T) {
	for _, a := range attributeKinds() {
		t.Run(typeName(a), func(t *testing.T) {
			v := newAttributeVisitor()
			if err := a.Visit(v); err != nil {
				t.Fatalf("Visit returned %v", err)
			}
			if len(v.seen) != 1 || v.seen[0] != typeName(a) {
				t.Errorf("Visit dispatched to %v, want %s", v.seen, typeName(a))
			}
		})
	}
}

func TestAttributeKeyStrings(t *testing.T) {
	tests := []struct {
		name string
		key  AttributeKey
		want string
	}{
		{"constant", ConstantAttributeKey{Name: "class"}, "class"},
		// The braces are what tell a reader — and the formatter — that
		// the key is computed rather than literal.
		{"expression", ExpressionAttributeKey{Expression: Expression{Value: "k"}}, "{ k }"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.key.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSpreadAttributesString(t *testing.T) {
	sa := &SpreadAttributes{Expression: Expression{Value: "attrs"}}
	if got := sa.String(); !strings.Contains(got, "attrs") {
		t.Errorf("String() = %q, want it to mention the spread expression", got)
	}
}

// attributeVisitor records which attribute Visit method was reached. The
// embedded interface supplies the node-visiting methods no attribute
// dispatches to.
type attributeVisitor struct {
	Visitor
	seen []string
}

func newAttributeVisitor() *attributeVisitor { return &attributeVisitor{} }

func (v *attributeVisitor) record(name string) error {
	v.seen = append(v.seen, name)
	return nil
}

func (v *attributeVisitor) VisitBoolConstantAttribute(*BoolConstantAttribute) error {
	return v.record("BoolConstantAttribute")
}

func (v *attributeVisitor) VisitConstantAttribute(*ConstantAttribute) error {
	return v.record("ConstantAttribute")
}

func (v *attributeVisitor) VisitBoolExpressionAttribute(*BoolExpressionAttribute) error {
	return v.record("BoolExpressionAttribute")
}

func (v *attributeVisitor) VisitExpressionAttribute(*ExpressionAttribute) error {
	return v.record("ExpressionAttribute")
}

func (v *attributeVisitor) VisitSpreadAttributes(*SpreadAttributes) error {
	return v.record("SpreadAttributes")
}

func (v *attributeVisitor) VisitConditionalAttribute(*ConditionalAttribute) error {
	return v.record("ConditionalAttribute")
}

// TestConditionalAttribute covers the one kind that holds other
// attributes, so its Write and Copy have to recurse.
func TestConditionalAttribute(t *testing.T) {
	inner := &ConstantAttribute{Key: ConstantAttributeKey{Name: "class"}, Value: "on"}
	cond := &ConditionalAttribute{
		Expression: Expression{Value: "enabled"},
		Then:       []Attribute{inner},
	}

	var sb strings.Builder
	if err := cond.Write(&sb, 0); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := sb.String()
	for _, want := range []string{"enabled", "class", "on"} {
		if !strings.Contains(got, want) {
			t.Errorf("output %q is missing %q", got, want)
		}
	}

	clone, ok := cond.Copy().(*ConditionalAttribute)
	if !ok {
		t.Fatal("Copy did not return a *ConditionalAttribute")
	}
	if len(clone.Then) != len(cond.Then) {
		t.Fatalf("the copy has %d then-attributes, want %d", len(clone.Then), len(cond.Then))
	}
	// A shallow copy here means rewriting the clone's nested attribute
	// silently edits the original.
	if len(clone.Then) > 0 && clone.Then[0] == cond.Then[0] {
		t.Error("Copy shared the nested attribute with the original")
	}

	v := newAttributeVisitor()
	if err := cond.Visit(v); err != nil {
		t.Fatalf("Visit: %v", err)
	}
	if len(v.seen) == 0 {
		t.Error("Visit reached no visitor method")
	}
}
