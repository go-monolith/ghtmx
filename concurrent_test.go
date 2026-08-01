package ghtmx_test

import (
	"context"
	"io"
	"sync"
	"testing"

	"github.com/go-monolith/ghtmx"
)

func TestWithChildrenConcurrentSafety(t *testing.T) {
	ctx := ghtmx.InitializeContext(context.Background())
	child := ghtmx.ComponentFunc(func(ctx context.Context, w io.Writer) error { return nil })

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			newCtx := ghtmx.WithChildren(ctx, child)
			_ = ghtmx.GetChildren(newCtx)
		})
	}
	wg.Wait()
}
