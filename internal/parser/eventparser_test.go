package parser

import (
	"strings"
	"testing"
)

func TestEventDeclarationParsing(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantName string
		wantExpr string
	}{
		{
			name:     "typed payload",
			src:      "package main\n\nevent UserCreated(id string, name string)\n",
			wantName: "UserCreated",
			wantExpr: "UserCreated(id string, name string)",
		},
		{
			name:     "payload-less",
			src:      "package main\n\nevent CartCleared()\n",
			wantName: "CartCleared",
			wantExpr: "CartCleared()",
		},
		{
			name:     "among templates",
			src:      "package main\n\nevent ItemSaved(id string)\n\ntempl page() {\n\t<p>hi</p>\n}\n",
			wantName: "ItemSaved",
			wantExpr: "ItemSaved(id string)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tf, err := ParseString(tt.src)
			if err != nil {
				t.Fatal(err)
			}
			var events []*EventDeclaration
			for _, n := range tf.Nodes {
				if e, ok := n.(*EventDeclaration); ok {
					events = append(events, e)
				}
			}
			if len(events) != 1 {
				t.Fatalf("expected one event declaration, got %d", len(events))
			}
			e := events[0]
			if e.Name != tt.wantName || e.Expression.Value != tt.wantExpr {
				t.Errorf("got name %q expr %q, want %q %q", e.Name, e.Expression.Value, tt.wantName, tt.wantExpr)
			}
		})
	}
}

func TestEventDeclarationErrors(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{
			name:    "unexported name",
			src:     "package main\n\nevent userCreated(id string)\n",
			wantErr: "must be exported",
		},
		{
			name:    "body brace",
			src:     "package main\n\nevent UserCreated(id string) {\n}\n",
			wantErr: "no body",
		},
		{
			name:    "missing parameter list",
			src:     "package main\n\nevent UserCreated\n",
			wantErr: "invalid event declaration",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseString(tt.src)
			if err == nil {
				t.Fatal("expected a parse error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q must contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestEventDeclarationFormats(t *testing.T) {
	src := "package main\n\nevent UserCreated(id string, name string)\n\ntempl page() {\n\t<p>hi</p>\n}\n"
	tf, err := ParseString(src)
	if err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := tf.Write(&sb); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sb.String(), "event UserCreated(id string, name string)\n") {
		t.Errorf("formatter must round-trip the declaration, got:\n%s", sb.String())
	}
	// Idempotence: formatting the formatted output changes nothing.
	tf2, err := ParseString(sb.String())
	if err != nil {
		t.Fatal(err)
	}
	var sb2 strings.Builder
	if err := tf2.Write(&sb2); err != nil {
		t.Fatal(err)
	}
	if sb.String() != sb2.String() {
		t.Errorf("formatting must be idempotent:\nfirst:  %q\nsecond: %q", sb.String(), sb2.String())
	}
}

func TestEventDeclarationEdgeCases(t *testing.T) {
	t.Run("interior double space stays byte-aligned", func(t *testing.T) {
		tf, err := ParseString("package main\n\nevent  UserCreated(id string)\n")
		if err != nil {
			t.Fatal(err)
		}
		var found *EventDeclaration
		for _, n := range tf.Nodes {
			if e, ok := n.(*EventDeclaration); ok {
				found = e
			}
		}
		if found == nil || found.Name != "UserCreated" || found.Expression.Value != "UserCreated(id string)" {
			t.Fatalf("expected the event to parse byte-aligned, got %+v", found)
		}
	})
	t.Run("generic events rejected", func(t *testing.T) {
		_, err := ParseString("package main\n\nevent ListChanged[T any](items []T)\n")
		if err == nil || !strings.Contains(err.Error(), "type parameters") {
			t.Fatalf("expected a type-parameter error, got %v", err)
		}
	})
	t.Run("single-word name rejected", func(t *testing.T) {
		_, err := ParseString("package main\n\nevent Saved()\n")
		if err == nil || !strings.Contains(err.Error(), "DOM event namespace") {
			t.Fatalf("expected a single-word wire-name error, got %v", err)
		}
	})
	t.Run("trailing comment kept and formatted", func(t *testing.T) {
		src := "package main\n\nevent UserCreated(id string) // emitted on signup\n"
		tf, err := ParseString(src)
		if err != nil {
			t.Fatal(err)
		}
		var sb strings.Builder
		if err := tf.Write(&sb); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(sb.String(), "event UserCreated(id string) // emitted on signup\n") {
			t.Errorf("the comment must survive formatting, got:\n%s", sb.String())
		}
	})
}

func TestEventPayloadParamValidation(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string
	}{
		{"unnamed", "event UserCreated(string)", "must be named"},
		{"blank", "event UserCreated(_ string)", "must start with an ASCII letter"},
		{"variadic", "event UserCreated(ids ...string)", "cannot be variadic"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseString("package main\n\n" + tt.src + "\n")
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected %q error, got %v", tt.wantErr, err)
			}
		})
	}
}

func TestEventPayloadTypeValidation(t *testing.T) {
	valid := []string{
		"event UserCreated(id string, n int, ok bool)",
		"event UserCreated(ids []string, meta map[string]any, ref *int)",
	}
	for _, src := range valid {
		if _, err := ParseString("package main\n\n" + src + "\n"); err != nil {
			t.Errorf("%s must parse, got %v", src, err)
		}
	}
	invalid := []struct{ src, wantErr string }{
		{"event UserCreated(at time.Time)", "qualified"},
		{"event UserCreated(item Item)", "not a builtin"},
		{"event UserCreated(ch chan int)", "not JSON-serializable"},
		{"event UserCreated(id string, Id int)", "both export as field"},
		{"event UserCreated(_id string)", "must start with an ASCII letter"},
	}
	for _, tt := range invalid {
		_, err := ParseString("package main\n\n" + tt.src + "\n")
		if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
			t.Errorf("%s: expected %q error, got %v", tt.src, tt.wantErr, err)
		}
	}
}

func TestEventNameMustBeASCII(t *testing.T) {
	_, err := ParseString("package main\n\nevent ÜberFällig()\n")
	if err == nil || !strings.Contains(err.Error(), "ASCII") {
		t.Fatalf("expected an ASCII-name error, got %v", err)
	}
}
