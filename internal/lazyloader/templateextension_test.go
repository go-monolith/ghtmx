package lazyloader

import (
	"testing"
)

// The traverser decides whether a package's sibling file is a template by
// comparing extensions. A caller that omits TemplateExtension gets the
// canonical one rather than the empty string, which would match nothing
// and silently open no documents at all.
func TestNewCarriesTheTemplateExtension(t *testing.T) {
	for _, tc := range []struct{ name, given, want string }{
		{name: "configured", given: ".htmx", want: ".htmx"},
		{name: "omitted falls back", given: "", want: ".ghtmx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			loader, ok := New(NewParams{TemplateExtension: tc.given}).(*templDocLazyLoader)
			if !ok {
				t.Fatal("New must return the concrete loader")
			}
			traverser, ok := loader.pkgTraverser.(*goPkgTraverser)
			if !ok {
				t.Fatal("the loader must hold the Go package traverser")
			}
			if got := traverser.ext(); got != tc.want {
				t.Errorf("traverser extension = %q, want %q", got, tc.want)
			}
		})
	}
}
