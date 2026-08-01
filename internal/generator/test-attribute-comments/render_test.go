package testattributecomments

import (
	_ "embed"
	"testing"

	"github.com/go-monolith/ghtmx/internal/htmldiff"
)

//go:embed expected.html
var expected string

func Test(t *testing.T) {
	component := TestAttributeComments()

	_, diff, err := htmldiff.Diff(component, expected)
	if err != nil {
		t.Fatal(err)
	}
	if diff != "" {
		t.Error(diff)
	}
}
