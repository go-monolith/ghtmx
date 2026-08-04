package generatecmd

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/fsnotify/fsnotify"
)

// A generated file is orphaned when its template is gone, and the handler
// deletes it. The template is found by swapping the generated suffix for
// the template extension, so a handler using the wrong extension looks for
// a file that never existed and deletes output it just wrote. That is a
// self-destruct loop in watch mode, not a cosmetic mismatch.
func TestOrphanCheckUsesTheConfiguredExtension(t *testing.T) {
	for _, tc := range []struct {
		name         string
		extension    string
		templateFile string
		wantDeleted  bool
	}{{
		name:         "a .htmx template keeps its generated file",
		extension:    ".htmx",
		templateFile: "page.htmx",
		wantDeleted:  false,
	}, {
		name:         "a .ghtmx template keeps its generated file",
		extension:    ".ghtmx",
		templateFile: "page.ghtmx",
		wantDeleted:  false,
	}, {
		// The configured extension is .htmx, so a leftover .ghtmx file is
		// not a template and its output really is orphaned.
		name:         "a template with the other extension does not count",
		extension:    ".htmx",
		templateFile: "page.ghtmx",
		wantDeleted:  true,
	}, {
		name:         "no template at all is still orphaned",
		extension:    ".htmx",
		templateFile: "",
		wantDeleted:  true,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			generated := filepath.Join(dir, "page_ghtmx.go")
			if err := os.WriteFile(generated, []byte("package p\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if tc.templateFile != "" {
				if err := os.WriteFile(filepath.Join(dir, tc.templateFile), []byte("package p\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			h := NewFSEventHandler(
				slog.New(slog.DiscardHandler),
				dir,
				false, nil, false,
				false, // keepOrphanedFiles: deletion must be allowed to observe it
				nil,
				false,
				WithTemplateExtension(tc.extension),
			)

			if _, err := h.HandleEvent(context.Background(), fsnotify.Event{
				Name: generated,
				Op:   fsnotify.Write,
			}); err != nil {
				t.Fatalf("HandleEvent: %v", err)
			}

			_, err := os.Stat(generated)
			deleted := os.IsNotExist(err)
			if deleted != tc.wantDeleted {
				t.Errorf("generated file deleted = %v, want %v", deleted, tc.wantDeleted)
			}
		})
	}
}
