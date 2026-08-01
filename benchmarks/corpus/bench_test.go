package corpus

import (
	"context"
	"strings"
	"testing"
)

// The four benchmark workloads the NFR-002 gate measures. Item counts
// keep a render in the tens of microseconds — large enough to dominate
// harness noise, small enough for tight iteration.

const workloadItems = 100

func benchmarkRender(b *testing.B, render func(w *strings.Builder) error) {
	b.Helper()
	b.ReportAllocs()
	w := new(strings.Builder)
	b.ResetTimer()
	for range b.N {
		if err := render(w); err != nil {
			b.Fatalf("render failed: %v", err)
		}
		w.Reset()
	}
}

func BenchmarkPage(b *testing.B) {
	items := MakeItems(workloadItems)
	component := PageWorkload("Catalogue", items)
	benchmarkRender(b, func(w *strings.Builder) error {
		return component.Render(context.Background(), w)
	})
}

func BenchmarkFragmentInline(b *testing.B) {
	items := MakeItems(workloadItems)
	component := FragmentWorkload(items)
	benchmarkRender(b, func(w *strings.Builder) error {
		return component.Render(context.Background(), w)
	})
}

func BenchmarkFragmentStandalone(b *testing.B) {
	item := MakeItems(1)[0]
	fragment := BenchRowFragment(item)
	benchmarkRender(b, func(w *strings.Builder) error {
		return fragment.RenderFragment(context.Background(), w)
	})
}

func BenchmarkNestedLayout(b *testing.B) {
	items := MakeItems(workloadItems)
	component := NestedWorkload(items)
	benchmarkRender(b, func(w *strings.Builder) error {
		return component.Render(context.Background(), w)
	})
}

func BenchmarkBindings(b *testing.B) {
	items := MakeItems(workloadItems)
	component := BindingWorkload(items)
	benchmarkRender(b, func(w *strings.Builder) error {
		return component.Render(context.Background(), w)
	})
}
