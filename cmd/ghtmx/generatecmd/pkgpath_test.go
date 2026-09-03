package generatecmd

import (
	"path/filepath"
	"testing"
)

// TestPkgPathFor: a template's package path is the module path plus its
// directory relative to the MODULE root, not the generate root — a
// -path below the module (a nested project with its own ghtmx.json)
// must still resolve bare handler identifiers to their real package.
func TestPkgPathFor(t *testing.T) {
	mod := filepath.Join(t.TempDir(), "m")
	cases := []struct {
		name    string
		dir     string
		modRoot string
		file    string
		want    string
	}{
		{name: "generate root is the module root", dir: mod, modRoot: mod, file: "a.ghtmx", want: "example.com/m"},
		{name: "subpackage under the module root", dir: mod, modRoot: mod, file: "site/a.ghtmx", want: "example.com/m/site"},
		{name: "generate root below the module root", dir: filepath.Join(mod, "examples", "x"), modRoot: mod, file: "examples/x/a.ghtmx", want: "example.com/m/examples/x"},
		{name: "subpackage of a nested generate root", dir: filepath.Join(mod, "examples", "x"), modRoot: mod, file: "examples/x/handlers/a.ghtmx", want: "example.com/m/examples/x/handlers"},
		{name: "no module root falls back to the generate root", dir: filepath.Join(mod, "examples", "x"), modRoot: "", file: "examples/x/a.ghtmx", want: "example.com/m"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &FSEventHandler{dir: tc.dir, modRoot: tc.modRoot, modulePath: "example.com/m"}
			if got := h.pkgPathFor(filepath.Join(mod, filepath.FromSlash(tc.file))); got != tc.want {
				t.Errorf("pkgPathFor = %q, want %q", got, tc.want)
			}
		})
	}
	if got := (&FSEventHandler{dir: mod, modRoot: mod}).pkgPathFor(filepath.Join(mod, "a.ghtmx")); got != "" {
		t.Errorf("without a module path pkgPathFor = %q, want empty", got)
	}
}
