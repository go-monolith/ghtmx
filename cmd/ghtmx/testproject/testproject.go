package testproject

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

//go:embed testdata/*
var testdata embed.FS

func Create(moduleRoot string) (dir string, err error) {
	dir, err = os.MkdirTemp("", "ghtmx_test_*")
	if err != nil {
		return dir, fmt.Errorf("failed to make test dir: %w", err)
	}
	// Canonicalize: Windows can hand out 8.3 short names (RUNNER~1) and
	// macOS symlinks /var to /private/var; gopls resolves both, and URIs
	// built from the raw form then match none of its workspace views.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	files, err := testdata.ReadDir("testdata")
	if err != nil {
		return dir, fmt.Errorf("failed to read embedded dir: %w", err)
	}
	for _, file := range files {
		if file.IsDir() {
			if err = os.MkdirAll(filepath.Join(dir, file.Name()), 0777); err != nil {
				return dir, fmt.Errorf("failed to create dir: %w", err)
			}
			continue
		}
		src := "testdata/" + file.Name() // embed.FS paths always use forward slashes
		data, err := testdata.ReadFile(src)
		if err != nil {
			return dir, fmt.Errorf("failed to read file: %w", err)
		}

		target := filepath.Join(dir, file.Name())
		if file.Name() == "go.mod.embed" {
			data = bytes.ReplaceAll(data, []byte("{moduleRoot}"), []byte(moduleRoot))
			target = filepath.Join(dir, "go.mod")
		}
		if file.Name() == "formatting.ghtmx.txt" {
			target = filepath.Join(dir, "formatting.ghtmx")
		}
		err = os.WriteFile(target, data, 0660)
		if err != nil {
			return dir, fmt.Errorf("failed to copy file: %w", err)
		}
	}
	files, err = testdata.ReadDir("testdata/css-classes")
	if err != nil {
		return dir, fmt.Errorf("failed to read embedded dir: %w", err)
	}
	for _, file := range files {
		src := "testdata/css-classes/" + file.Name() // embed.FS paths always use forward slashes
		data, err := testdata.ReadFile(src)
		if err != nil {
			return dir, fmt.Errorf("failed to read file: %w", err)
		}
		target := filepath.Join(dir, "css-classes", file.Name())
		err = os.WriteFile(target, data, 0660)
		if err != nil {
			return dir, fmt.Errorf("failed to copy file: %w", err)
		}
	}
	return dir, nil
}

func MustReplaceLine(file string, line int, replacement string) string {
	lines := strings.Split(file, "\n")
	lines[line-1] = replacement
	return strings.Join(lines, "\n")
}
