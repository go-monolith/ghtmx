package testscriptusage

import (
	"context"
	_ "embed"
	"testing"

	"github.com/go-monolith/ghtmx"
	"github.com/go-monolith/ghtmx/internal/htmldiff"
)

//go:embed expected.html
var expected string

func Test(t *testing.T) {
	component := ThreeButtons()

	ctx := ghtmx.WithNonce(context.Background(), "nonce1")
	_, diff, err := htmldiff.DiffCtx(ctx, component, expected)
	if err != nil {
		t.Fatal(err)
	}
	if diff != "" {
		t.Error(diff)
	}
}
