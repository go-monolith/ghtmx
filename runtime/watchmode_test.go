package runtime

import (
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
)

func TestGetWatchedString(t *testing.T) {
	tests := []struct {
		name      string
		watchRoot string
		fileName  string
		expected  string
	}{
		{
			name:      "returns default value when file is outside watch root",
			watchRoot: "/root",
			fileName:  "/other/fileoutside_ghtmx.go",
			expected:  "ghtmx_file_value",
		},
		{
			name:      "uses cache when file is inside watch root",
			watchRoot: "/root",
			fileName:  "/root/fileinside_ghtmx.go",
			expected:  "txt_file_value",
		},
		{
			name:      "uses cache when watch root is not set (legacy behaviour)",
			watchRoot: "",
			fileName:  "/root/file_ghtmx.go",
			expected:  "txt_file_value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange.
			tmpDir := t.TempDir()

			// We have to actually make the file because GetWatchedString checks
			// the file's mod time to determine whether to use the cache or read
			// from disk.
			testFile := filepath.Join(tmpDir, tt.fileName)
			if err := os.MkdirAll(filepath.Dir(testFile), 0755); err != nil {
				t.Fatalf("failed to create directory: %v", err)
			}
			if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
				t.Fatalf("failed to write test file: %v", err)
			}

			resolvedPath, err := filepath.EvalSymlinks(testFile)
			if err != nil {
				t.Fatalf("failed to eval symlinks for test file: %v", err)
			}
			txtFile := GetDevModeTextFileName(resolvedPath)
			if err := os.WriteFile(txtFile, []byte("txt_file_value"), 0644); err != nil {
				t.Fatalf("failed to write txt file: %v", err)
			}

			watchRootPath := filepath.Join(tmpDir, tt.watchRoot)
			if err := os.MkdirAll(watchRootPath, 0755); err != nil {
				t.Fatalf("failed to create watch root directory: %v", err)
			}
			loader := NewStringLoader(watchRootPath)

			// Act.
			actual, err := loader.GetWatchedString(testFile, 1, "ghtmx_file_value")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			// Assert.
			if actual != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, actual)
			}
		})
	}
}

func TestWatchMode(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("expected file names are Unix style; dev mode paths are exercised on Unix hosts")
	}
	t.Setenv("GHTMX_DEV_MODE_ROOT", "/tmp")

	t.Run("GetDevModeTextFileName respects the GHTMX_DEV_MODE_ROOT environment variable", func(t *testing.T) {
		expected := "/tmp/ghtmx_d9f29fa4fc4f7fdd50daad5542139169b2221e7138326aad1ea15b057be23543.txt"
		actual := GetDevModeTextFileName("test.ghtmx")
		if actual != expected {
			t.Errorf("got %q, want %q", actual, expected)
		}
	})
	t.Run("GetDevModeTextFileName replaces _ghtmx.go with .ghtmx", func(t *testing.T) {
		expected := "/tmp/ghtmx_d9f29fa4fc4f7fdd50daad5542139169b2221e7138326aad1ea15b057be23543.txt"
		actual := GetDevModeTextFileName("test_ghtmx.go")
		if actual != expected {
			t.Errorf("got %q, want %q", actual, expected)
		}
	})
	t.Run("GetDevModeTextFileName accepts absolute Linux paths", func(t *testing.T) {
		expected := "/tmp/ghtmx_fb9f23c285403c98287ac17c68f6571f5a035aff3d99f4518463311968b277e7.txt"
		actual := GetDevModeTextFileName("/home/user/test.ghtmx")
		if actual != expected {
			t.Errorf("got %q, want %q", actual, expected)
		}
	})
	t.Run("GetDevModeTextFileName accepts absolute Windows paths, which are normalized to Unix style before hashing", func(t *testing.T) {
		expected := "/tmp/ghtmx_1bfc4b065ae5ccd2b4c4d93168a1966861933ef56c36c1cba247b7698294b5a0.txt"
		actual := GetDevModeTextFileName(`C:\Windows\System32\test.ghtmx`)
		if actual != expected {
			t.Errorf("got %q, want %q", actual, expected)
		}
	})
}
