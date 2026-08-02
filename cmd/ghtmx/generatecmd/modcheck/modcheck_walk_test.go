package modcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// WalkUp finds the module root, and Check verifies the project depends
// on a ghtmx version compatible with the compiler running against it. A
// version mismatch is the failure mode worth catching early: the
// generated code calls runtime functions that may not exist in the
// version the project pins, and the resulting error points at generated
// code rather than at the real cause.

// module writes a go.mod at dir and returns the directory.
func module(t *testing.T, dir, contents string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestWalkUpFindsTheModuleRoot(t *testing.T) {
	root := module(t, t.TempDir(), "module example.com/app\n\ngo 1.25\n")
	nested := filepath.Join(root, "internal", "deep", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		from string
	}{
		{"from the root", root},
		// Generation runs from wherever the user invoked it, so the walk
		// has to climb out of any subdirectory.
		{"from a subdirectory", filepath.Join(root, "internal")},
		{"from deep inside", nested},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := WalkUp(tt.from)
			if err != nil {
				t.Fatalf("WalkUp: %v", err)
			}
			// Compare resolved paths: the temp directory is a symlink on
			// macOS.
			wantResolved, err := filepath.EvalSymlinks(root)
			if err != nil {
				t.Fatal(err)
			}
			gotResolved, err := filepath.EvalSymlinks(got)
			if err != nil {
				t.Fatal(err)
			}
			if gotResolved != wantResolved {
				t.Errorf("WalkUp = %q, want %q", gotResolved, wantResolved)
			}
		})
	}
}

// skipIfModuleAbove skips when a go.mod exists at or above dir, which
// would make the no-module cases below meaningless. Temp directories
// live outside any module in practice, but a stray /tmp/go.mod on a
// developer's machine would otherwise turn these into silent passes.
func skipIfModuleAbove(t *testing.T, dir string) {
	t.Helper()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			t.Skipf("a go.mod exists at %s; the no-module case cannot be tested here", dir)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return
		}
		dir = parent
	}
}

// TestWalkUpWithNoModule pins the behaviour when the walk reaches the
// filesystem root without finding a go.mod: generation cannot proceed
// without a module, so this has to be distinguishable from success.
func TestWalkUpWithNoModule(t *testing.T) {
	dir := t.TempDir()
	skipIfModuleAbove(t, dir)

	if _, err := WalkUp(dir); err == nil {
		t.Error("WalkUp reported success with no go.mod anywhere above the directory")
	}
}

func TestCheck(t *testing.T) {
	tests := []struct {
		name     string
		contents string
		wantErr  bool
		wantMsg  string
	}{
		{
			// Generating against a module that does not depend on ghtmx
			// would emit code importing a package the project cannot
			// resolve, so this is caught up front with the command that
			// fixes it.
			name:     "no ghtmx dependency",
			contents: "module example.com/app\n\ngo 1.25\n",
			wantErr:  true,
			wantMsg:  "go get github.com/go-monolith/ghtmx",
		},
		{
			// An older pinned runtime is the mismatch that matters: the
			// generated code calls functions it may not have, and the
			// resulting error would point at generated code rather than
			// at the version pin.
			name: "pinned version older than the generator",
			contents: "module example.com/app\n\ngo 1.25\n\n" +
				"require github.com/go-monolith/ghtmx v0.0.1\n",
			wantErr: true,
			wantMsg: "is newer than ghtmx version",
		},
		{
			// The check is symmetric: a newer runtime than the CLI is
			// reported too, since generated code is written for the
			// version that produced it and the pair have to agree.
			name: "pinned version newer than the generator",
			contents: "module example.com/app\n\ngo 1.25\n\n" +
				"require github.com/go-monolith/ghtmx v1.99.0\n",
			wantErr: true,
			wantMsg: "is older than ghtmx version",
		},
		{
			name:     "unparseable go.mod",
			contents: "this is not a go.mod\n",
			wantErr:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := module(t, t.TempDir(), tt.contents)

			err := Check(dir)
			if tt.wantErr {
				if err == nil {
					t.Fatal("Check accepted a go.mod it cannot read")
				}
				if tt.wantMsg != "" && !strings.Contains(err.Error(), tt.wantMsg) {
					t.Errorf("error %q does not mention %q", err, tt.wantMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("Check: %v", err)
			}
		})
	}
}

// TestCheckWithNoGoMod pins the missing-module path: Check has to report
// rather than silently proceeding, since without a module there is no
// version to verify against.
func TestCheckWithNoGoMod(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	skipIfModuleAbove(t, dir)

	if err := Check(dir); err == nil {
		t.Error("Check succeeded with no module; there is no version to verify against")
	}
}
