package teststringerrs

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"testing"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/internal/htmldiff"
)

//go:embed expected.html
var expected string

func Test(t *testing.T) {
	t.Run("can render without error", func(t *testing.T) {
		component := TestComponent(nil)

		_, _, err := htmldiff.Diff(component, expected)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("string expressions can return errors", func(t *testing.T) {
		errSomethingBad := errors.New("bad error")

		err := TestComponent(errSomethingBad).Render(context.Background(), &bytes.Buffer{})
		if err == nil {
			t.Fatalf("expected error, but got nil")
		}

		t.Run("the errors are templ errors", func(t *testing.T) {
			var templateErr ghtmx.Error
			if !errors.As(err, &templateErr) {
				t.Fatalf("expected error to be ghtmx.Error, but got %T", err)
			}
			if templateErr.FileName != `internal/generator/test-string-errors/template.ghtmx` {
				t.Errorf("expected error in `internal/generator/test-string-errors/template.ghtmx`, but got %v", templateErr.FileName)
			}
			if templateErr.Line != 18 {
				t.Errorf("expected error on line 18, but got %v", templateErr.Line)
			}
			if templateErr.Col != 26 {
				t.Errorf("expected error on column 26, but got %v", templateErr.Col)
			}
		})

		t.Run("the underlying error can be unwrapped", func(t *testing.T) {
			if !errors.Is(err, errSomethingBad) {
				t.Errorf("expected error: %v, but got %v", errSomethingBad, err)
			}
		})

	})
}
