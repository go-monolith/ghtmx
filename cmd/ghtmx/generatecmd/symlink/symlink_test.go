package symlink

import (
	"context"
	"io"
	"os"
	"path"
	"testing"

	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/generatecmd/modcheck"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/testproject"
)

func TestSymlink(t *testing.T) {
	t.Run("can generate if root is symlink", func(t *testing.T) {
		// The fixture's replace directive needs the real module directory:
		// generation resolves the project's hx-* bindings through route
		// discovery, which loads packages.
		wd, err := os.Getwd()
		if err != nil {
			t.Fatalf("could not find working dir: %v", err)
		}
		moduleRoot, err := modcheck.WalkUp(wd)
		if err != nil {
			t.Fatalf("could not find the ghtmx go.mod: %v", err)
		}

		// ghtmx generate -f templates.ghtmx
		dir, err := testproject.Create(moduleRoot)
		if err != nil {
			t.Fatalf("failed to create test project: %v", err)
		}
		defer func() {
			if err := os.RemoveAll(dir); err != nil {
				t.Errorf("failed to remove test project directory: %v", err)
			}
		}()

		symlinkPath := dir + "-symlink"
		err = os.Symlink(dir, symlinkPath)
		if err != nil {
			t.Fatalf("failed to create dir symlink: %v", err)
		}
		defer func() {
			if err = os.Remove(symlinkPath); err != nil {
				t.Errorf("failed to remove symlink directory: %v", err)
			}
		}()

		// Delete the templates_ghtmx.go file to ensure it is generated.
		err = os.Remove(path.Join(symlinkPath, "templates_ghtmx.go"))
		if err != nil {
			t.Fatalf("failed to remove templates_ghtmx.go: %v", err)
		}

		// Run the generate command.
		err = generatecmd.Run(context.Background(), io.Discard, io.Discard, []string{"-path", symlinkPath})
		if err != nil {
			t.Fatalf("failed to run generate command: %v", err)
		}

		// Check the templates_ghtmx.go file was created.
		_, err = os.Stat(path.Join(symlinkPath, "templates_ghtmx.go"))
		if err != nil {
			t.Fatalf("templates_ghtmx.go was not created: %v", err)
		}
	})
}
