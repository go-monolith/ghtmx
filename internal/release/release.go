// Package release builds the versioned, checksummed release artifacts
// (NFR-010): one ghtmx binary per supported platform, archived with the
// license and README, plus a sha256 checksums file. The version comes
// from the embedded .version file — the release workflow stamps it from
// the tag before building — so the binary, the module tag, and every
// component in the single module carry one version.
package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Target is one platform build.
type Target struct {
	GOOS   string
	GOARCH string
}

// Targets is the supported platform matrix.
var Targets = []Target{
	{"linux", "amd64"},
	{"linux", "arm64"},
	{"darwin", "amd64"},
	{"darwin", "arm64"},
	{"windows", "amd64"},
	{"windows", "arm64"},
}

// extras are repository files shipped inside every archive.
var extras = []string{"LICENSE", "NOTICE", "README.md"}

// Build cross-compiles cmd/ghtmx for every target, packages each into
// an archive named ghtmx_<version>_<os>_<arch>, and writes
// checksums.txt (sha256sum format) over the archives. version is the
// tag form ("v1.2.3"); archive names carry it without the prefix.
func Build(root, version, dst string) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if entries, err := os.ReadDir(dst); err == nil && len(entries) > 0 {
		// A stale artifact from a prior run would be uploaded by the
		// release glob but not covered by checksums.txt.
		return fmt.Errorf("output directory %s is not empty", dst)
	}
	bare := strings.TrimPrefix(version, "v")
	var archives []string
	for _, target := range Targets {
		archive, err := BuildOne(root, dst, bare, target)
		if err != nil {
			return err
		}
		archives = append(archives, archive)
	}
	return writeChecksums(dst, archives)
}

// BuildOne builds and archives a single target, returning the archive
// name. The install-path check uses it for the running platform.
func BuildOne(root, dst, bare string, target Target) (string, error) {
	binary := "ghtmx"
	if target.GOOS == "windows" {
		binary += ".exe"
	}
	buildDir, err := os.MkdirTemp("", "ghtmx-release-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(buildDir)
	binaryPath := filepath.Join(buildDir, binary)
	cmd := exec.Command("go", "build", "-trimpath", "-o", binaryPath, "./cmd/ghtmx")
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"CGO_ENABLED=0",
		"GOOS="+target.GOOS,
		"GOARCH="+target.GOARCH,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s/%s: %w\n%s", target.GOOS, target.GOARCH, err, out)
	}

	name := fmt.Sprintf("ghtmx_%s_%s_%s", bare, target.GOOS, target.GOARCH)
	var archive string
	if target.GOOS == "windows" {
		archive = name + ".zip"
		err = zipArchive(filepath.Join(dst, archive), root, binaryPath, binary)
	} else {
		archive = name + ".tar.gz"
		err = tarArchive(filepath.Join(dst, archive), root, binaryPath, binary)
	}
	if err != nil {
		return "", fmt.Errorf("archive %s: %w", archive, err)
	}
	return archive, nil
}

// Extract unpacks a release archive (tar.gz or zip) into dst,
// restoring the binary's executable bit — the same steps an installing
// user performs. Entries are flattened into dst (which also makes the
// extraction zip-slip-safe by construction); non-regular entries are
// skipped. The archives this package builds are flat regular files.
func Extract(archive, dst string) error {
	if strings.HasSuffix(archive, ".zip") {
		r, err := zip.OpenReader(archive)
		if err != nil {
			return err
		}
		defer r.Close()
		for _, f := range r.File {
			if !f.Mode().IsRegular() {
				continue
			}
			src, err := f.Open()
			if err != nil {
				return err
			}
			data, err := io.ReadAll(src)
			_ = src.Close()
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dst, filepath.Base(f.Name)), data, f.Mode()); err != nil {
				return err
			}
		}
		return nil
	}
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		mode := os.FileMode(header.Mode) & 0o777
		if err := os.WriteFile(filepath.Join(dst, filepath.Base(header.Name)), data, mode); err != nil {
			return err
		}
	}
}

func writeChecksums(dst string, archives []string) error {
	sort.Strings(archives)
	var sums strings.Builder
	for _, name := range archives {
		f, err := os.Open(filepath.Join(dst, name))
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return err
		}
		fmt.Fprintf(&sums, "%x  %s\n", h.Sum(nil), name)
	}
	return os.WriteFile(filepath.Join(dst, "checksums.txt"), []byte(sums.String()), 0o644)
}

func tarArchive(dst, root, binaryPath, binaryName string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	add := func(path, name string, mode int64) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(data))}); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	}
	if err := add(binaryPath, binaryName, 0o755); err != nil {
		return err
	}
	for _, extra := range extras {
		if err := add(filepath.Join(root, extra), extra, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func zipArchive(dst, root, binaryPath, binaryName string) error {
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	zw := zip.NewWriter(out)
	defer zw.Close()

	add := func(path, name string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		// Mode bits matter when the archive is unpacked on Unix.
		header.SetMode(0o755)
		w, err := zw.CreateHeader(header)
		if err != nil {
			return err
		}
		_, err = w.Write(data)
		return err
	}
	if err := add(binaryPath, binaryName); err != nil {
		return err
	}
	for _, extra := range extras {
		if err := add(filepath.Join(root, extra), extra); err != nil {
			return err
		}
	}
	return nil
}
