package testcssexpression

import (
	"testing"

	"github.com/go-monolith/ghtmx"
	"github.com/google/go-cmp/cmp"
)

var expected = ghtmx.ComponentCSSClass{
	ID:    "className_34fc0328",
	Class: ghtmx.SafeCSS(`.className_34fc0328{background-color:#ffffff;max-height:calc(100vh - 170px);color:#ff0000;}`),
}

func TestCSSExpression(t *testing.T) {
	if diff := cmp.Diff(expected, className()); diff != "" {
		t.Error(diff)
	}
}
