package release

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// These archives are what a user downloads and unpacks, so the contract
// is narrow and unforgiving: the binary has to be inside, it has to come
// out executable, the licence files have to ride along, and
// checksums.txt has to cover every archive the release glob will upload.
// A missing executable bit or an archive absent from checksums.txt is
// only discovered by the person installing it.

// fakeRoot builds a repository root holding the files every archive
// ships, plus a stand-in binary.
func fakeRoot(t *testing.T) (root, binaryPath string) {
	t.Helper()
	root = t.TempDir()
	for _, name := range extras {
		if err := os.WriteFile(filepath.Join(root, name), []byte("contents of "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	binaryPath = filepath.Join(t.TempDir(), "ghtmx")
	if err := os.WriteFile(binaryPath, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, binaryPath
}

func TestArchiveRoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		ext        string
		archive    func(dst, root, binaryPath, binaryName string) error
		binaryName string
	}{
		{"tar.gz", ".tar.gz", tarArchive, "ghtmx"},
		{"zip", ".zip", zipArchive, "ghtmx.exe"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, binaryPath := fakeRoot(t)
			archivePath := filepath.Join(t.TempDir(), "ghtmx_1.2.3_test"+tt.ext)

			if err := tt.archive(archivePath, root, binaryPath, tt.binaryName); err != nil {
				t.Fatalf("archive: %v", err)
			}

			dst := t.TempDir()
			if err := Extract(archivePath, dst); err != nil {
				t.Fatalf("extract: %v", err)
			}

			// The binary and every shipped extra must be present.
			for _, want := range append([]string{tt.binaryName}, extras...) {
				if _, err := os.Stat(filepath.Join(dst, want)); err != nil {
					t.Errorf("%s is missing from the extracted archive: %v", want, err)
				}
			}

			// The executable bit is the thing users notice when it is
			// wrong: the download runs on Unix or it does not.
			if runtime.GOOS != "windows" {
				info, err := os.Stat(filepath.Join(dst, tt.binaryName))
				if err != nil {
					t.Fatal(err)
				}
				if info.Mode().Perm()&0o111 == 0 {
					t.Errorf("the extracted binary is not executable (mode %v)", info.Mode().Perm())
				}
			}

			// Content must survive the round trip intact.
			got, err := os.ReadFile(filepath.Join(dst, "LICENSE"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != "contents of LICENSE" {
				t.Errorf("LICENSE = %q, want the original contents", got)
			}
		})
	}
}

// TestExtractRejectsAnUnknownArchive pins that a file which is neither a
// zip nor a tar.gz fails rather than silently extracting nothing, which
// would leave the install-path check passing over an empty directory.
func TestExtractRejectsAnUnknownArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-an-archive.tar.gz")
	if err := os.WriteFile(path, []byte("definitely not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := Extract(path, t.TempDir()); err == nil {
		t.Error("Extract succeeded on a corrupt archive, want an error")
	}
}

func TestExtractReportsAMissingArchive(t *testing.T) {
	for _, name := range []string{"absent.tar.gz", "absent.zip"} {
		t.Run(name, func(t *testing.T) {
			if err := Extract(filepath.Join(t.TempDir(), name), t.TempDir()); err == nil {
				t.Error("Extract succeeded on a nonexistent archive, want an error")
			}
		})
	}
}

func TestWriteChecksums(t *testing.T) {
	dst := t.TempDir()
	archives := []string{"a.tar.gz", "b.zip"}
	for _, name := range archives {
		if err := os.WriteFile(filepath.Join(dst, name), []byte("payload of "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	if err := writeChecksums(dst, archives); err != nil {
		t.Fatalf("writeChecksums: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(dst, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != len(archives) {
		t.Fatalf("checksums.txt has %d lines, want %d — an archive would ship uncovered", len(lines), len(archives))
	}
	for i, line := range lines {
		// sha256sum format: 64 hex digits, two spaces, filename.
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Errorf("line %d %q is not in sha256sum format", i+1, line)
			continue
		}
		if len(fields[0]) != 64 {
			t.Errorf("line %d has a %d-character digest, want 64", i+1, len(fields[0]))
		}
		if !strings.Contains(string(raw), archives[i]) {
			t.Errorf("checksums.txt does not mention %s", archives[i])
		}
	}
}

func TestWriteChecksumsReportsAMissingArchive(t *testing.T) {
	if err := writeChecksums(t.TempDir(), []string{"never-built.tar.gz"}); err == nil {
		t.Error("writeChecksums succeeded with a missing archive, want an error")
	}
}

// TestBuildRefusesANonEmptyOutputDirectory pins the guard that stops a
// stale artifact from a previous run being uploaded by the release glob
// while absent from checksums.txt.
func TestBuildRefusesANonEmptyOutputDirectory(t *testing.T) {
	dst := t.TempDir()
	if err := os.WriteFile(filepath.Join(dst, "ghtmx_0.0.1_linux_amd64.tar.gz"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := Build(t.TempDir(), "v1.2.3", dst)
	if err == nil {
		t.Fatal("Build succeeded into a non-empty directory, want an error")
	}
	if !strings.Contains(err.Error(), "not empty") {
		t.Errorf("error %q does not explain that the directory is not empty", err)
	}
}

// TestTargetsCoverTheSupportedMatrix pins the platform list: dropping a
// target silently ships a release missing a platform, and the only
// symptom is a user's download 404ing.
func TestTargetsCoverTheSupportedMatrix(t *testing.T) {
	want := map[string]bool{
		"linux/amd64": true, "linux/arm64": true,
		"darwin/amd64": true, "darwin/arm64": true,
		"windows/amd64": true, "windows/arm64": true,
	}
	got := map[string]bool{}
	for _, target := range Targets {
		got[target.GOOS+"/"+target.GOARCH] = true
	}
	for platform := range want {
		if !got[platform] {
			t.Errorf("the release matrix no longer covers %s", platform)
		}
	}
	if len(got) != len(want) {
		t.Errorf("the release matrix has %d targets, want %d: %v", len(got), len(want), got)
	}
}

// TestBuildOneForTheNativeTarget is the one case that actually compiles.
// Cross-compiling all six in a unit test would cost minutes; the release
// workflow covers the rest.
func TestBuildOneForTheNativeTarget(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles the CLI; skipped under -short")
	}
	root := repoRoot(t)
	dst := t.TempDir()

	archive, err := BuildOne(root, dst, "1.2.3", Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
	if err != nil {
		t.Fatalf("BuildOne: %v", err)
	}

	wantName := "ghtmx_1.2.3_" + runtime.GOOS + "_" + runtime.GOARCH
	if !strings.HasPrefix(archive, wantName) {
		t.Errorf("archive name = %q, want it to start with %q", archive, wantName)
	}
	if _, err := os.Stat(filepath.Join(dst, archive)); err != nil {
		t.Errorf("the archive BuildOne named was not written: %v", err)
	}

	// The archive must actually contain a runnable binary.
	unpacked := t.TempDir()
	if err := Extract(filepath.Join(dst, archive), unpacked); err != nil {
		t.Fatalf("extract: %v", err)
	}
	binary := "ghtmx"
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if _, err := os.Stat(filepath.Join(unpacked, binary)); err != nil {
		t.Errorf("the built archive has no %s: %v", binary, err)
	}
}

// TestBuildOneReportsACompileFailure pins that a broken build surfaces
// rather than producing an archive around a missing binary.
func TestBuildOneReportsACompileFailure(t *testing.T) {
	if testing.Short() {
		t.Skip("invokes the Go toolchain; skipped under -short")
	}
	// A directory with no cmd/ghtmx package: the build must fail.
	_, err := BuildOne(t.TempDir(), t.TempDir(), "1.2.3", Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH})
	if err == nil {
		t.Error("BuildOne succeeded against a root with no cmd/ghtmx, want an error")
	}
}
