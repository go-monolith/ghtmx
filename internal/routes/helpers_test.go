package routes

import (
	"go/parser"
	"testing"
)

// These three decide what route discovery can see. isUpperAlpha is how a
// chi-style r.GET(...) is told from an ordinary method call;
// receiverBaseName is how the router variable is found through wrappers;
// and splitErrorPos is what turns a loader failure into a diagnostic
// someone can navigate to. Each is small, and each silently loses routes
// when it is wrong — the user sees a binding that will not resolve and
// no explanation.

func TestIsUpperAlpha(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"GET", true},
		{"POST", true},
		{"DELETE", true},
		{"G", true},
		// Anything mixed or lowercase is an ordinary method call, not a
		// verb: treating Get as a route registration would invent routes
		// from unrelated code.
		{"Get", false},
		{"get", false},
		{"GET2", false},
		{"GET_", false},
		{"GET ", false},
		{"", false},
		{"Ünicode", false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := isUpperAlpha(tt.in); got != tt.want {
				t.Errorf("isUpperAlpha(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestReceiverBaseName(t *testing.T) {
	tests := []struct {
		name string
		expr string
		want string
	}{
		{"plain identifier", "r", "r"},
		{"parenthesised", "(r)", "r"},
		{"doubly parenthesised", "((r))", "r"},
		// A router reached through a call still has to be traced back to
		// its variable, or every route registered on it is lost.
		{"through a call", "r.Group()", "r"},
		{"through a parenthesised call", "(r.Group())", "r"},
		{"selector without a call", "a.b", ""},
		{"literal", `"not a receiver"`, ""},
		{"index expression", "handlers[0]", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			expr, err := parser.ParseExpr(tt.expr)
			if err != nil {
				t.Fatalf("parsing %q: %v", tt.expr, err)
			}
			if got := receiverBaseName(expr); got != tt.want {
				t.Errorf("receiverBaseName(%q) = %q, want %q", tt.expr, got, tt.want)
			}
		})
	}
}

func TestSplitErrorPos(t *testing.T) {
	tests := []struct {
		name     string
		pos      string
		wantFile string
		wantLine uint32
		wantCol  uint32
		wantOK   bool
	}{
		{
			name:     "file line and column",
			pos:      "main.go:12:5",
			wantFile: "main.go", wantLine: 12, wantCol: 5, wantOK: true,
		},
		{
			name:     "absolute path",
			pos:      "/project/internal/app/main.go:3:1",
			wantFile: "/project/internal/app/main.go", wantLine: 3, wantCol: 1, wantOK: true,
		},
		// The go tool emits "-" when it has no position; treating it as
		// a filename would point a diagnostic at a file called "-".
		{name: "no position", pos: "-"},
		{name: "empty", pos: ""},
		{name: "no colon", pos: "main.go"},
		{name: "non-numeric column", pos: "main.go:12:x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file, line, col, ok := splitErrorPos(tt.pos)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (file=%q line=%d col=%d)", ok, tt.wantOK, file, line, col)
			}
			if !tt.wantOK {
				return
			}
			if file != tt.wantFile {
				t.Errorf("file = %q, want %q", file, tt.wantFile)
			}
			if line != tt.wantLine {
				t.Errorf("line = %d, want %d", line, tt.wantLine)
			}
			if col != tt.wantCol {
				t.Errorf("col = %d, want %d", col, tt.wantCol)
			}
		})
	}
}
