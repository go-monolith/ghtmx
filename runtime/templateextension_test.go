package runtime

import (
	"os"
	"path/filepath"
	"testing"
)

// Dev-mode hot reload only works if both sides derive the same sidecar
// path. The generator hashes the template's real name; the generated code
// knows only its own _ghtmx.go name and has to recover the template's. When
// that recovery assumed ".ghtmx", a .htmx project hashed two different
// paths and the reader never found the file the writer had just produced.
func TestDevModeSidecarAgreesForEitherExtension(t *testing.T) {
	for _, ext := range []string{".ghtmx", ".htmx"} {
		t.Run(ext, func(t *testing.T) {
			dir := t.TempDir()
			template := filepath.Join(dir, "page"+ext)
			generated := filepath.Join(dir, "page_ghtmx.go")
			for _, f := range []string{template, generated} {
				if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			// The writer's view and the reader's view.
			fromTemplate := GetDevModeTextFileName(template)
			fromGenerated := GetDevModeTextFileName(generated)
			if fromTemplate != fromGenerated {
				t.Errorf("writer and reader disagree for %s:\n  template  -> %s\n  generated -> %s", ext, fromTemplate, fromGenerated)
			}
		})
	}
}

// Two projects differing only in extension must not collide on one
// sidecar, or their literals would overwrite each other.
func TestDevModeSidecarDiffersBetweenExtensions(t *testing.T) {
	ghtmxDir, htmxDir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(ghtmxDir, "page.ghtmx"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(htmxDir, "page.htmx"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := GetDevModeTextFileName(filepath.Join(ghtmxDir, "page_ghtmx.go"))
	b := GetDevModeTextFileName(filepath.Join(htmxDir, "page_ghtmx.go"))
	if a == b {
		t.Error("distinct templates must not share one sidecar")
	}
}

// With no template on disk the canonical extension still applies, which is
// what this function did before it looked at all. A generated file whose
// template has been deleted must keep resolving, not change behaviour.
func TestDevModeSidecarFallsBackToTheCanonicalExtension(t *testing.T) {
	dir := t.TempDir()
	orphan := filepath.Join(dir, "page_ghtmx.go")
	if err := os.WriteFile(orphan, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, want := GetDevModeTextFileName(orphan), GetDevModeTextFileName(filepath.Join(dir, "page.ghtmx")); got != want {
		t.Errorf("orphaned generated file = %s, want the canonical %s", got, want)
	}
}
