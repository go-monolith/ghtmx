package modcheck

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-monolith/ghtmx"
	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// WalkUp the directory tree, starting at dir, until we find a directory containing
// a go.mod file.
func WalkUp(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("failed to get absolute path: %w", err)
	}

	// found, not modFile: the previous guard tested modFile == "", which
	// is never true after the first iteration, so a tree with no go.mod
	// anywhere returned the filesystem root and a nil error. The caller
	// then failed further along, reading "/go.mod", with a message that
	// named the wrong problem.
	var found bool
	for {
		_, err := os.Stat(filepath.Join(dir, "go.mod"))
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("failed to stat go.mod file: %w", err)
		}
		if os.IsNotExist(err) {
			// Move up.
			prev := dir
			dir = filepath.Dir(dir)
			if dir == prev {
				break
			}
			continue
		}
		found = true
		break
	}

	if !found {
		return dir, fmt.Errorf("could not find go.mod file")
	}
	return dir, nil
}

func Check(dir string) error {
	dir, err := WalkUp(dir)
	if err != nil {
		return err
	}

	// Found a go.mod file.
	// Read it and find the ghtmx version.
	modFile := filepath.Join(dir, "go.mod")
	m, err := os.ReadFile(modFile)
	if err != nil {
		return fmt.Errorf("failed to read go.mod file: %w", err)
	}

	mf, err := modfile.Parse(modFile, m, nil)
	if err != nil {
		return fmt.Errorf("failed to parse go.mod file: %w", err)
	}
	if mf.Module.Mod.Path == "github.com/go-monolith/ghtmx" {
		// The go.mod file is for templ itself.
		return nil
	}
	for _, r := range mf.Require {
		if r.Mod.Path == "github.com/go-monolith/ghtmx" {
			cmp := semver.Compare(r.Mod.Version, ghtmx.Version())
			if cmp < 0 {
				return fmt.Errorf("generator %v is newer than ghtmx version %v found in go.mod file, consider running `go get -u github.com/go-monolith/ghtmx` to upgrade", ghtmx.Version(), r.Mod.Version)
			}
			if cmp > 0 {
				return fmt.Errorf("generator %v is older than ghtmx version %v found in go.mod file, consider upgrading the ghtmx CLI", ghtmx.Version(), r.Mod.Version)
			}
			return nil
		}
	}
	return fmt.Errorf("ghtmx not found in go.mod file, run `go get github.com/go-monolith/ghtmx` to install it")
}
