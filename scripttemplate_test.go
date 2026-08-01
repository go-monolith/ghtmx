package ghtmx_test

import (
	"bytes"
	"context"
	"testing"

	"github.com/go-monolith/ghtmx"
	"github.com/google/go-cmp/cmp"
)

func TestRenderScriptItems(t *testing.T) {
	s1 := ghtmx.ComponentScript{
		Name:     "s1",
		Function: "function s1() { return 'hello1'; }",
	}
	s2 := ghtmx.ComponentScript{
		Name:     "s2",
		Function: "function s2() { return 'hello2'; }",
	}
	tests := []struct {
		name     string
		toIgnore []ghtmx.ComponentScript
		toRender []ghtmx.ComponentScript
		expected string
	}{
		{
			name:     "if none are ignored, everything is rendered",
			toIgnore: nil,
			toRender: []ghtmx.ComponentScript{s1, s2},
			expected: `<script>` + s1.Function + s2.Function + `</script>`,
		},
		{
			name: "if something outside the expected is ignored, if has no effect",
			toIgnore: []ghtmx.ComponentScript{
				{
					Name:     "s3",
					Function: "function s3() { return 'hello3'; }",
				},
			},
			toRender: []ghtmx.ComponentScript{s1, s2},
			expected: `<script>` + s1.Function + s2.Function + `</script>`,
		},
		{
			name:     "if one is ignored, it's not rendered",
			toIgnore: []ghtmx.ComponentScript{s1},
			toRender: []ghtmx.ComponentScript{s1, s2},
			expected: `<script>` + s2.Function + `</script>`,
		},
		{
			name: "if all are ignored, not even style tags are rendered",
			toIgnore: []ghtmx.ComponentScript{
				s1,
				s2,
				{
					Name:     "s3",
					Function: "function s3() { return 'hello3'; }",
				},
			},
			toRender: []ghtmx.ComponentScript{s1, s2},
			expected: ``,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			b := new(bytes.Buffer)

			// Render twice, reusing the same context so that there's a memory of which classes have been rendered.
			ctx = ghtmx.InitializeContext(ctx)
			err := ghtmx.RenderScriptItems(ctx, b, tt.toIgnore...)
			if err != nil {
				t.Fatalf("failed to render initial scripts: %v", err)
			}

			// Now render again to check that only the expected classes were rendered.
			b.Reset()
			err = ghtmx.RenderScriptItems(ctx, b, tt.toRender...)
			if err != nil {
				t.Fatalf("failed to render scripts: %v", err)
			}

			if diff := cmp.Diff(tt.expected, b.String()); diff != "" {
				t.Error(diff)
			}
		})
	}
}

func TestJSExpression(t *testing.T) {
	expected := "myJSFunction(\"StringValue\",123,event,1 + 2)"
	actual := ghtmx.SafeScriptInline("myJSFunction", "StringValue", 123, ghtmx.JSExpression("event"), ghtmx.JSExpression("1 + 2"))

	if actual != expected {
		t.Fatalf("TestJSExpression: expected %q, got %q", expected, actual)
	}
}
