package parser

import (
	"strings"
	"testing"
)

// Fallthrough and ConditionalAttribute are the two node kinds the
// earlier sweeps missed: the first is only reachable inside a switch,
// and the second is an attribute rather than a node, so neither
// nodeKinds nor attributeKinds enumerated it for the String check.

func TestFallthroughNode(t *testing.T) {
	f := &Fallthrough{}

	if !f.IsNode() {
		t.Error("IsNode() = false; a fallthrough would be skipped by every walk")
	}

	var sb strings.Builder
	if err := f.Write(&sb, 1); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if got := sb.String(); !strings.Contains(got, "fallthrough") {
		t.Errorf("Write produced %q, want it to contain the keyword", got)
	}
	// The indent is what keeps a formatted switch readable.
	if !strings.HasPrefix(sb.String(), "\t") {
		t.Errorf("Write ignored the indent level: %q", sb.String())
	}

	v := newRecordingVisitor()
	if err := f.Visit(v); err != nil {
		t.Fatalf("Visit: %v", err)
	}
	if len(v.seen) != 1 || v.seen[0] != "Fallthrough" {
		t.Errorf("Visit dispatched to %v, want Fallthrough", v.seen)
	}
}

func TestConditionalAttributeString(t *testing.T) {
	cond := &ConditionalAttribute{
		Expression: Expression{Value: "enabled"},
		Then:       []Attribute{&ConstantAttribute{Key: ConstantAttributeKey{Name: "class"}, Value: "on"}},
		Else:       []Attribute{&ConstantAttribute{Key: ConstantAttributeKey{Name: "class"}, Value: "off"}},
	}

	got := cond.String()
	// The rendering is what a diagnostic or hover shows, so both
	// branches have to appear — naming only the `then` side would point
	// a user at half their markup.
	for _, want := range []string{"enabled", "on", "off"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
}
