package ghtmx_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/go-monolith/ghtmx"
	"github.com/google/go-cmp/cmp"
)

func TestJSONScriptElement(t *testing.T) {
	data := map[string]any{"foo": "bar"}
	tests := []struct {
		name     string
		ctx      context.Context
		e        ghtmx.JSONScriptElement
		expected string
	}{
		{
			name:     "renders data as JSON inside a script element",
			e:        ghtmx.JSONScript("id", data),
			expected: "<script id=\"id\" type=\"application/json\">{\"foo\":\"bar\"}\n</script>",
		},
		{
			name:     "if a nonce is available in the context, it is used",
			ctx:      ghtmx.WithNonce(context.Background(), "nonce-from-context"),
			e:        ghtmx.JSONScript("idc", data),
			expected: "<script id=\"idc\" type=\"application/json\" nonce=\"nonce-from-context\">{\"foo\":\"bar\"}\n</script>",
		},
		{
			name:     "if a nonce is provided, it is used",
			e:        ghtmx.JSONScript("ids", data).WithNonceFromString("nonce-from-string"),
			expected: "<script id=\"ids\" type=\"application/json\" nonce=\"nonce-from-string\">{\"foo\":\"bar\"}\n</script>",
		},
		{
			name:     "if a nonce function is provided, it is used",
			e:        ghtmx.JSONScript("idf", data).WithNonceFrom(func(context.Context) string { return "nonce-from-function" }),
			expected: "<script id=\"idf\" type=\"application/json\" nonce=\"nonce-from-function\">{\"foo\":\"bar\"}\n</script>",
		},
		{
			name:     "if a type is provided, it is used",
			e:        ghtmx.JSONScript("idt", data).WithType("application/ld+json"),
			expected: "<script id=\"idt\" type=\"application/ld+json\">{\"foo\":\"bar\"}\n</script>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := new(bytes.Buffer)
			if err := tt.e.Render(tt.ctx, w); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.expected, w.String()); diff != "" {
				t.Fatalf("unexpected output (-want +got):\n%s", diff)
			}
		})
	}
}
