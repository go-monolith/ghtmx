package parser

import (
	"strings"
	"testing"
)

// The symbol ranges are a separate index from the expression map: the
// LSP's outline and go-to-symbol use them to map a generated Go
// declaration back to the templ block that produced it. A missed lookup
// sends the editor to the wrong place, and a lookup that answers for a
// position it does not hold is worse — it points confidently at
// nonsense.

func symbolMap() *SourceMap {
	sm := NewSourceMap()
	sm.AddSymbolRange(
		Range{From: Position{Line: 3, Col: 0}, To: Position{Line: 5, Col: 1}},
		Range{From: Position{Line: 12, Col: 0}, To: Position{Line: 20, Col: 1}},
	)
	return sm
}

func TestSymbolRangeLookupsRoundTrip(t *testing.T) {
	sm := symbolMap()

	tgt, ok := sm.SymbolTargetRangeFromSource(3, 0)
	if !ok {
		t.Fatal("the source position has no target symbol range")
	}
	if tgt.From.Line != 12 {
		t.Errorf("target line = %d, want 12", tgt.From.Line)
	}

	src, ok := sm.SymbolSourceRangeFromTarget(12, 0)
	if !ok {
		t.Fatal("the target position has no source symbol range")
	}
	if src.From.Line != 3 {
		t.Errorf("source line = %d, want 3", src.From.Line)
	}
}

func TestSymbolRangeLookupsReportAMiss(t *testing.T) {
	sm := symbolMap()

	tests := []struct {
		name string
		line uint32
		col  uint32
	}{
		// A line the index does not hold at all.
		{"unknown line", 99, 0},
		// A known line but a column with no symbol starting there: the
		// index is keyed on the exact start position, so answering here
		// would return a neighbouring symbol's range.
		{"known line, unknown column", 3, 99},
	}
	for _, tt := range tests {
		t.Run("target from source/"+tt.name, func(t *testing.T) {
			if _, ok := sm.SymbolTargetRangeFromSource(tt.line, tt.col); ok {
				t.Error("a position with no symbol range reported a hit")
			}
		})
	}
	for _, tt := range []struct {
		name string
		line uint32
		col  uint32
	}{
		{"unknown line", 99, 0},
		{"known line, unknown column", 12, 99},
	} {
		t.Run("source from target/"+tt.name, func(t *testing.T) {
			if _, ok := sm.SymbolSourceRangeFromTarget(tt.line, tt.col); ok {
				t.Error("a position with no symbol range reported a hit")
			}
		})
	}
}

// TestSymbolRangeLookupsOnAnEmptyMap pins the state the LSP starts in,
// before anything has been generated.
func TestSymbolRangeLookupsOnAnEmptyMap(t *testing.T) {
	sm := NewSourceMap()

	if _, ok := sm.SymbolTargetRangeFromSource(0, 0); ok {
		t.Error("an empty source map reported a symbol range")
	}
	if _, ok := sm.SymbolSourceRangeFromTarget(0, 0); ok {
		t.Error("an empty source map reported a symbol range")
	}
}

// TestAttributeStringsRenderTheirSource covers the String methods used
// in diagnostics and hover text: an empty one leaves the user reading a
// message about an attribute it does not name.
func TestAttributeStringsRenderTheirSource(t *testing.T) {
	tests := []struct {
		name string
		attr interface{ String() string }
		want string
	}{
		{
			name: "expression attribute",
			attr: &ExpressionAttribute{
				Key:        ConstantAttributeKey{Name: "class"},
				Expression: Expression{Value: "name"},
			},
			want: "class",
		},
		{
			name: "spread attributes",
			attr: &SpreadAttributes{Expression: Expression{Value: "attrs"}},
			want: "attrs",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.attr.String()
			if got == "" {
				t.Fatal("String() is empty; a diagnostic would not name the attribute")
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("String() = %q, want it to mention %q", got, tt.want)
			}
		})
	}
}
