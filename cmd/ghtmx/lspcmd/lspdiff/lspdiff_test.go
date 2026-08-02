package lspdiff

import (
	"testing"

	"github.com/go-monolith/ghtmx/internal/lsp/protocol"
)

// These comparisons are what the LSP tests assert against, so a wrong
// one makes those tests either unfailable or unfixable. CompletionList
// in particular ignores IsIncomplete deliberately — gopls sets it from
// its own indexing state, which has nothing to do with whether the
// completions are right.

func TestCodeAction(t *testing.T) {
	a := []protocol.CodeAction{{Title: "organise imports"}}

	if diff := CodeAction(a, a); diff != "" {
		t.Errorf("identical code actions differ:\n%s", diff)
	}
	if diff := CodeAction(a, []protocol.CodeAction{{Title: "something else"}}); diff == "" {
		t.Error("different code actions compared equal")
	}
	if diff := CodeAction(nil, a); diff == "" {
		t.Error("nil compared equal to a non-empty list")
	}
}

func TestCompletionListIgnoresIsIncomplete(t *testing.T) {
	items := []protocol.CompletionItem{{Label: "hx-get"}}

	complete := &protocol.CompletionList{IsIncomplete: false, Items: items}
	incomplete := &protocol.CompletionList{IsIncomplete: true, Items: items}

	// gopls sets IsIncomplete from its indexing state, so comparing it
	// would make the LSP tests flaky on a cold cache.
	if diff := CompletionList(complete, incomplete); diff != "" {
		t.Errorf("IsIncomplete was compared:\n%s", diff)
	}
	// The items themselves must still be compared, or the assertion is
	// worthless.
	different := &protocol.CompletionList{Items: []protocol.CompletionItem{{Label: "hx-post"}}}
	if diff := CompletionList(complete, different); diff == "" {
		t.Error("different completion items compared equal")
	}
}

func TestReferences(t *testing.T) {
	a := []protocol.Location{{URI: "file:///a.go"}}

	if diff := References(a, a); diff != "" {
		t.Errorf("identical locations differ:\n%s", diff)
	}
	if diff := References(a, []protocol.Location{{URI: "file:///b.go"}}); diff == "" {
		t.Error("different locations compared equal")
	}
}

func TestCompletionListContainsText(t *testing.T) {
	cl := &protocol.CompletionList{
		Items: []protocol.CompletionItem{{Label: "hx-get"}, {Label: "hx-post"}},
	}

	tests := []struct {
		name string
		cl   *protocol.CompletionList
		text string
		want bool
	}{
		{"present", cl, "hx-get", true},
		{"also present", cl, "hx-post", true},
		{"absent", cl, "hx-delete", false},
		// A nil list is what an unsupported request returns, and asking
		// about it must answer rather than panic.
		{"nil list", nil, "hx-get", false},
		{"empty list", &protocol.CompletionList{}, "hx-get", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CompletionListContainsText(tt.cl, tt.text); got != tt.want {
				t.Errorf("CompletionListContainsText = %v, want %v", got, tt.want)
			}
		})
	}
}
