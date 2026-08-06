package installcheck

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/release"
)

// scripts/install.sh is bash-only by design, so these run on Linux and
// macOS. They never touch the network: GHTMX_RELEASES_URL points the
// script at a local server serving a fake release, which is the whole
// reason that variable exists.

func skipUnlessPOSIX(t *testing.T) {
	t.Helper()
	if goruntime.GOOS == "windows" {
		t.Skip("scripts/install.sh is bash-only; native Windows uses the manual steps")
	}
}

// hostTarget is what the script's uname detection resolves to, so the
// fake release must offer an archive for exactly this platform.
func hostTarget() release.Target {
	return release.Target{GOOS: goruntime.GOOS, GOARCH: goruntime.GOARCH}
}

// fakeRelease serves the three URLs the script asks for: the "latest"
// redirect it reads the tag from, the platform archive, and
// checksums.txt. corruptChecksum publishes a digest that does not match
// the archive, which is the only way to exercise the verify failure.
type fakeRelease struct {
	tag             string
	corruptChecksum bool
	// omitBinary publishes a well-formed, correctly-checksummed archive
	// with no ghtmx member — a release that passes verification and
	// still cannot be installed.
	omitBinary bool
	// checksumOtherPlatform lists a different platform's archive in
	// checksums.txt, so ours is absent from it.
	checksumOtherPlatform bool
	// target defaults to the host; set it to serve a cross-install.
	target release.Target
}

func (f fakeRelease) start(t *testing.T) *httptest.Server {
	t.Helper()
	target := f.target
	if target == (release.Target{}) {
		target = hostTarget()
	}
	bare := strings.TrimPrefix(f.tag, "v")
	name := release.ArchiveName(bare, target)
	member := "ghtmx"
	if f.omitBinary {
		member = "LICENSE"
	}
	archive := stubArchive(t, member, f.tag)

	sum := sha256.Sum256(archive)
	digest := hex.EncodeToString(sum[:])
	if f.corruptChecksum {
		digest = strings.Repeat("0", len(digest))
	}
	listed := name
	if f.checksumOtherPlatform {
		listed = release.ArchiveName(bare, crossTarget())
	}
	checksums := fmt.Sprintf("%s  %s\n", digest, listed)

	mux := http.NewServeMux()
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, server.URL+"/tag/"+f.tag, http.StatusFound)
	})
	mux.HandleFunc("/tag/"+f.tag, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "release page")
	})
	mux.HandleFunc("/download/"+f.tag+"/"+name, func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write(archive); err != nil {
			t.Errorf("serving the archive failed: %v", err)
		}
	})
	mux.HandleFunc("/download/"+f.tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, checksums)
	})
	return server
}

// stubArchive is a release tarball holding a shell script standing in
// for ghtmx. A real cross-compile would be correct but costs minutes;
// what is under test is the script, not the binary — TestReleaseBinaryPath
// already covers the genuine archive.
//
// The stub answers `version` and rejects everything else, so a test that
// claims the script runs `ghtmx version` fails if it stops doing so.
func stubArchive(t *testing.T, member, tag string) []byte {
	t.Helper()
	body := fmt.Sprintf(
		"#!/bin/sh\ncase \"$1\" in\nversion) echo %s ;;\n*) echo \"unexpected args: $*\" >&2; exit 1 ;;\nesac\n",
		tag,
	)

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	header := &tar.Header{
		Name:     member,
		Mode:     0o755,
		Size:     int64(len(body)),
		Typeflag: tar.TypeReg,
	}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(body)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// baseEnv is the developer's environment with every variable the script
// reads stripped out. Without this, someone who exports GHTMX_VERSION or
// GOPLS_VERSION gets failures that reproduce nowhere else.
func baseEnv() []string {
	var env []string
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "GHTMX_") || name == "GOPLS_VERSION" {
			continue
		}
		env = append(env, entry)
	}
	return env
}

// installEnv defaults gopls off: the real `go install` would reach the
// module proxy, and the gopls branches have tests of their own.
func installEnv(server *httptest.Server, binDir string, extra ...string) []string {
	env := append(baseEnv(),
		"GHTMX_RELEASES_URL="+server.URL,
		"GHTMX_BIN_DIR="+binDir,
		"GHTMX_SKIP_GOPLS=1",
	)
	return append(env, extra...)
}

func runInstallScript(t *testing.T, env []string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("bash", filepath.Join(root, "scripts", "install.sh"))
	cmd.Dir = root
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// TestInstallScriptHappyPath: the default run resolves the latest tag
// from the redirect, verifies the checksum, and leaves a runnable binary.
func TestInstallScriptHappyPath(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9"}.start(t)
	binDir := t.TempDir()

	out, err := runInstallScript(t, installEnv(server, binDir))
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "checksum verified") {
		t.Errorf("output must report the checksum check:\n%s", out)
	}

	installed := filepath.Join(binDir, "ghtmx")
	info, err := os.Stat(installed)
	if err != nil {
		t.Fatalf("binary not installed: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed binary is not executable: %v", info.Mode())
	}
	version, err := exec.Command(installed, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("installed binary failed: %v\n%s", err, version)
	}
	if got := strings.TrimSpace(string(version)); got != "v9.9.9" {
		t.Errorf("installed binary reports %q, want %q", got, "v9.9.9")
	}
}

// TestInstallScriptVersionResolution: an explicit version wins over the
// redirect, with or without the "v".
func TestInstallScriptVersionResolution(t *testing.T) {
	skipUnlessPOSIX(t)
	for _, requested := range []string{"v0.1.5", "0.1.5"} {
		t.Run(requested, func(t *testing.T) {
			server := fakeRelease{tag: "v0.1.5"}.start(t)
			binDir := t.TempDir()

			out, err := runInstallScript(t, installEnv(server, binDir, "GHTMX_VERSION="+requested))
			if err != nil {
				t.Fatalf("install failed: %v\n%s", err, out)
			}
			if !strings.Contains(out, "(v0.1.5)") {
				t.Errorf("output must name the normalized tag:\n%s", out)
			}
		})
	}
}

// TestInstallScriptChecksumMismatch: a bad digest fails the run, and
// nothing reaches the install directory — verification comes first.
func TestInstallScriptChecksumMismatch(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9", corruptChecksum: true}.start(t)
	binDir := t.TempDir()

	out, err := runInstallScript(t, installEnv(server, binDir))
	if err == nil {
		t.Fatalf("a checksum mismatch must fail the install:\n%s", out)
	}
	if !strings.Contains(out, "checksum mismatch") {
		t.Errorf("output must name the mismatch:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(binDir, "ghtmx")); !os.IsNotExist(err) {
		t.Errorf("nothing may be installed after a failed verification (stat: %v)", err)
	}
}

// TestInstallScriptArchiveWithoutBinary: a verified archive that does
// not contain ghtmx still fails, and leaves the install directory empty
// — a good checksum is not the same as a usable release.
func TestInstallScriptArchiveWithoutBinary(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9", omitBinary: true}.start(t)
	binDir := t.TempDir()

	out, err := runInstallScript(t, installEnv(server, binDir))
	if err == nil {
		t.Fatalf("an archive without the binary must fail the install:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(binDir, "ghtmx")); !os.IsNotExist(err) {
		t.Errorf("nothing may be installed from an archive with no binary (stat: %v)", err)
	}
}

// TestInstallScriptMissingRelease: a version with no archive fails with
// a message that names the tag rather than a bare curl error.
func TestInstallScriptMissingRelease(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9"}.start(t)
	binDir := t.TempDir()

	out, err := runInstallScript(t, installEnv(server, binDir, "GHTMX_VERSION=v0.0.0"))
	if err == nil {
		t.Fatalf("a missing release must fail the install:\n%s", out)
	}
	if !strings.Contains(out, "does release v0.0.0 exist?") {
		t.Errorf("output must point at the missing release:\n%s", out)
	}
}

// TestInstallScriptUnsupportedArch: the override goes through the same
// gate as uname detection, so an architecture with no archive is
// rejected before anything is downloaded.
func TestInstallScriptUnsupportedArch(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9"}.start(t)
	binDir := t.TempDir()

	out, err := runInstallScript(t, installEnv(server, binDir, "GHTMX_ARCH=mips"))
	if err == nil {
		t.Fatalf("an unsupported architecture must fail the install:\n%s", out)
	}
	if !strings.Contains(out, "no release archive is built for") {
		t.Errorf("output must explain the unsupported platform:\n%s", out)
	}
}

// TestInstallScriptPathAdvice: the install directory is where editors
// have to find ghtmx, so the script says whether it is reachable — and
// when it is not, prints the exact line to add. It never edits rc files.
func TestInstallScriptPathAdvice(t *testing.T) {
	skipUnlessPOSIX(t)
	t.Run("absent", func(t *testing.T) {
		server := fakeRelease{tag: "v9.9.9"}.start(t)
		binDir := t.TempDir()

		out, err := runInstallScript(t, installEnv(server, binDir))
		if err != nil {
			t.Fatalf("install failed: %v\n%s", err, out)
		}
		want := fmt.Sprintf("export PATH=%q", binDir+":$PATH")
		if !strings.Contains(out, want) {
			t.Errorf("output must contain %s:\n%s", want, out)
		}
	})

	t.Run("present", func(t *testing.T) {
		server := fakeRelease{tag: "v9.9.9"}.start(t)
		binDir := t.TempDir()

		env := installEnv(server, binDir, "PATH="+binDir+":"+os.Getenv("PATH"))
		out, err := runInstallScript(t, env)
		if err != nil {
			t.Fatalf("install failed: %v\n%s", err, out)
		}
		if !strings.Contains(out, "is on your PATH") {
			t.Errorf("output must confirm the directory is reachable:\n%s", out)
		}
		if strings.Contains(out, "export PATH=") {
			t.Errorf("no PATH advice belongs in the output:\n%s", out)
		}
	})
}

// TestInstallScriptSkipsGopls: the opt-out is honored and says so.
func TestInstallScriptSkipsGopls(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9"}.start(t)
	binDir := t.TempDir()

	out, err := runInstallScript(t, installEnv(server, binDir))
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skipping gopls") {
		t.Errorf("output must report the skip:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(binDir, "gopls")); !os.IsNotExist(err) {
		t.Errorf("gopls must not be installed when skipped (stat: %v)", err)
	}
}

// TestInstallScriptChecksumsMissingEntry: checksums.txt that covers some
// other platform is not coverage of ours. This pins the exact-match
// semantics of the awk lookup — a prefix match would wrongly succeed.
func TestInstallScriptChecksumsMissingEntry(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9", checksumOtherPlatform: true}.start(t)
	binDir := t.TempDir()

	out, err := runInstallScript(t, installEnv(server, binDir))
	if err == nil {
		t.Fatalf("an unlisted archive must fail the install:\n%s", out)
	}
	if !strings.Contains(out, "has no entry for") {
		t.Errorf("output must say the archive is unlisted:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(binDir, "ghtmx")); !os.IsNotExist(err) {
		t.Errorf("nothing may be installed without a checksum (stat: %v)", err)
	}
}

// TestInstallScriptGoplsInstallFails: gopls is optional, so a `go
// install` that fails must cost the user gopls and nothing else — ghtmx
// stays installed and the closing advice still prints. This is the
// likeliest real-world failure: a blocked module proxy.
func TestInstallScriptGoplsInstallFails(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9"}.start(t)
	binDir := t.TempDir()

	// A `go` that exists and fails. `go env` still has to work: the
	// script calls it while resolving the install directory.
	fakeGo := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = env ]; then echo; echo; exit 0; fi\necho 'proxy unreachable' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(fakeGo, "go"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	env := append(baseEnv(),
		"GHTMX_RELEASES_URL="+server.URL,
		"GHTMX_BIN_DIR="+binDir,
		"PATH="+fakeGo+string(os.PathListSeparator)+os.Getenv("PATH"),
	)

	out, err := runInstallScript(t, env)
	if err != nil {
		t.Fatalf("a failed gopls install must not fail the run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "installing gopls failed") {
		t.Errorf("output must report the gopls failure:\n%s", out)
	}
	if !strings.Contains(out, "ghtmx version: v9.9.9") {
		t.Errorf("the run must still finish with its summary:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(binDir, "ghtmx")); err != nil {
		t.Errorf("ghtmx must stay installed: %v", err)
	}
}

// TestInstallScriptDefaultBinDir: with GHTMX_BIN_DIR unset the script
// walks GOBIN, then GOPATH/bin, then ~/.local/bin. That ladder decides
// where every default install lands, and nothing else exercises it.
func TestInstallScriptDefaultBinDir(t *testing.T) {
	skipUnlessPOSIX(t)

	// A `go` that reports the GOBIN and GOPATH we choose. `go env A B`
	// prints one value per line, in order.
	fakeGoWith := func(t *testing.T, gobin, gopath string) string {
		t.Helper()
		dir := t.TempDir()
		script := fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = env ]; then\n  echo '%s'\n  echo '%s'\n  exit 0\nfi\nexit 1\n", gobin, gopath)
		if err := os.WriteFile(filepath.Join(dir, "go"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	for _, tc := range []struct {
		name      string
		withGo    bool
		gobin     string
		gopath    string
		wantUnder func(home string) string
	}{
		{
			name:   "GOBIN wins",
			withGo: true,
			gobin:  "GOBIN_DIR",
			gopath: "GOPATH_DIR",
			wantUnder: func(home string) string {
				return filepath.Join(home, "GOBIN_DIR")
			},
		},
		{
			name:   "GOPATH/bin is next",
			withGo: true,
			gopath: "GOPATH_DIR",
			wantUnder: func(home string) string {
				return filepath.Join(home, "GOPATH_DIR", "bin")
			},
		},
		{
			name:   "~/.local/bin is the floor",
			withGo: false,
			wantUnder: func(home string) string {
				return filepath.Join(home, ".local", "bin")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := fakeRelease{tag: "v9.9.9"}.start(t)
			home := t.TempDir()

			// The fake go reports absolute paths under this HOME so the
			// script's own normalization has something real to resolve.
			abs := func(rel string) string {
				if rel == "" {
					return ""
				}
				return filepath.Join(home, rel)
			}
			path := os.Getenv("PATH")
			if tc.withGo {
				path = fakeGoWith(t, abs(tc.gobin), abs(tc.gopath)) + string(os.PathListSeparator) + path
			} else {
				path = shimPath(t)
			}
			env := append(baseEnv(),
				"GHTMX_RELEASES_URL="+server.URL,
				"GHTMX_SKIP_GOPLS=1",
				"HOME="+home,
				"PATH="+path,
			)

			out, err := runInstallScript(t, env)
			if err != nil {
				t.Fatalf("install failed: %v\n%s", err, out)
			}
			want := filepath.Join(tc.wantUnder(home), "ghtmx")
			if _, err := os.Stat(want); err != nil {
				t.Errorf("expected the binary at %s: %v\n%s", want, err, out)
			}
		})
	}
}

// TestInstallScriptCrossInstall: asking for another platform's archive
// installs it and skips gopls, which `go install` would have built for
// the host and dropped next to a binary it does not match.
func TestInstallScriptCrossInstall(t *testing.T) {
	skipUnlessPOSIX(t)
	other := crossTarget()
	server := fakeRelease{tag: "v9.9.9", target: other}.start(t)
	binDir := t.TempDir()

	env := append(baseEnv(),
		"GHTMX_RELEASES_URL="+server.URL,
		"GHTMX_BIN_DIR="+binDir,
		"GHTMX_OS="+other.GOOS,
		"GHTMX_ARCH="+other.GOARCH,
	)
	out, err := runInstallScript(t, env)
	if err != nil {
		t.Fatalf("cross-install failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skipping gopls: this is a cross-install") {
		t.Errorf("a cross-install must skip gopls:\n%s", out)
	}
	// The foreign binary cannot run here, so the script must not try.
	if strings.Contains(out, "ghtmx version:") {
		t.Errorf("a cross-installed binary must not be executed:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(binDir, "ghtmx")); err != nil {
		t.Errorf("the cross-installed binary must exist: %v", err)
	}
}

// crossTarget is a supported platform that is not this one.
func crossTarget() release.Target {
	host := hostTarget()
	for _, target := range release.Targets {
		if target.GOOS == "windows" {
			continue // no tar.gz, and the script refuses Windows anyway
		}
		if target != host {
			return target
		}
	}
	return host
}

// TestInstallScriptWithoutGoToolchain: gopls needs `go install` — no
// prebuilt binaries exist — but ghtmx does not, so a machine with no Go
// still gets a working install and a warning rather than a failure.
func TestInstallScriptWithoutGoToolchain(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9"}.start(t)
	binDir := t.TempDir()

	// A PATH holding only the tools the script needs, so `go` is
	// genuinely absent no matter where the toolchain lives on this
	// machine. Trimming PATH by hand would depend on the host layout.
	shim := shimPath(t)
	env := append(baseEnv(),
		"GHTMX_RELEASES_URL="+server.URL,
		"GHTMX_BIN_DIR="+binDir,
		"PATH="+shim,
	)
	out, err := runInstallScript(t, env)
	if err != nil {
		t.Fatalf("a missing Go toolchain must not fail the install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no Go toolchain found") {
		t.Errorf("output must warn about the missing toolchain:\n%s", out)
	}
	if !strings.Contains(out, "go install golang.org/x/tools/gopls@") {
		t.Errorf("output must give the command to finish the job:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(binDir, "ghtmx")); err != nil {
		t.Errorf("ghtmx must still be installed: %v", err)
	}
}

// shimPath builds a directory of symlinks to everything install.sh
// shells out to — and nothing else — and returns it as a one-entry PATH.
func shimPath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// gzip is here because GNU tar shells out to it for -z rather than
	// decompressing in process; bsdtar on macOS does not, so leaving it
	// out fails on Linux only.
	required := []string{
		"bash", "uname", "mktemp", "rm", "mkdir", "mv",
		"awk", "tar", "gzip", "install",
	}
	// The script accepts either member of these pairs.
	either := [][]string{{"curl", "wget"}, {"sha256sum", "shasum"}}

	link := func(name string) bool {
		path, err := exec.LookPath(name)
		if err != nil {
			return false
		}
		if err := os.Symlink(path, filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		return true
	}
	for _, name := range required {
		if !link(name) {
			t.Skipf("%s is not installed; cannot build a minimal PATH", name)
		}
	}
	for _, pair := range either {
		found := false
		for _, name := range pair {
			if link(name) {
				found = true
			}
		}
		if !found {
			t.Skipf("none of %v is installed; cannot build a minimal PATH", pair)
		}
	}
	if _, err := exec.LookPath(filepath.Join(dir, "go")); err == nil {
		t.Fatal("the shim PATH must not contain go")
	}
	return dir
}
