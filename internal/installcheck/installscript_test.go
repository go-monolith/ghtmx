package installcheck

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync/atomic"
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
	// noVsix publishes a release with no VS Code extension attached —
	// what a release looks like when its packaging job failed.
	noVsix bool
	// omitBinary publishes a well-formed, correctly-checksummed archive
	// with no ghtmx member — a release that passes verification and
	// still cannot be installed.
	omitBinary bool
	// checksumOtherPlatform lists a different platform's archive in
	// checksums.txt, so ours is absent from it.
	checksumOtherPlatform bool
	// assetPageHits, when set, counts requests for the asset list. The
	// script must not ask for it before the user has agreed to install
	// the extension, and only a count can show that.
	assetPageHits *atomic.Int64
	// extraVsix are further extension assets to publish alongside the
	// canonical one, for the case where a release carries more than one.
	extraVsix []string
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

	// GitHub serves a release's asset list as an HTML fragment of its
	// own, which is where the script reads the .vsix name from. The
	// markup below is the shape that matters: an anchor per asset whose
	// text is the file name. checksums.txt is listed first on the real
	// thing too, so a script that took the first name rather than the
	// first *matching* one would fail here.
	mux.HandleFunc("/expanded_assets/"+f.tag, func(w http.ResponseWriter, r *http.Request) {
		if f.assetPageHits != nil {
			f.assetPageHits.Add(1)
		}
		fmt.Fprintf(w, "<ul>\n<li><a href=%q>checksums.txt</a></li>\n",
			"/download/"+f.tag+"/checksums.txt")
		if !f.noVsix {
			for _, name := range append([]string{vsixName}, f.extraVsix...) {
				fmt.Fprintf(w, "<li><a href=%q>%s</a></li>\n",
					"/download/"+f.tag+"/"+name, name)
			}
		}
		fmt.Fprint(w, "</ul>\n")
	})
	for _, name := range append([]string{vsixName}, f.extraVsix...) {
		body := vsixBody
		if name != vsixName {
			body = "payload of " + name
		}
		mux.HandleFunc("/download/"+f.tag+"/"+name, func(w http.ResponseWriter, r *http.Request) {
			if f.noVsix {
				http.NotFound(w, r)
				return
			}
			fmt.Fprint(w, body)
		})
	}
	return server
}

// The extension artifact, named after the EXTENSION version rather than
// the release tag — editors/README.md's versioning policy, and the
// reason the script has to read the name off the release instead of
// computing it. Its contents are never opened by the script: it hands
// the file to VS Code, which is faked here.
const (
	vsixName = "ghtmx-vscode-0.1.0.vsix"
	vsixBody = "stub vsix payload"
)

// vscodeExtensionID is editors/vscode/package.json's publisher and name.
const vscodeExtensionID = "go-monolith.ghtmx-vscode"

// fakeVSCode is a `code` on PATH that records what it was asked to do,
// answers --list-extensions with the ids given, and keeps a copy of any
// .vsix handed to it — the only way to check that what the script
// downloaded is what the editor was given.
type fakeVSCode struct {
	dir       string // prepend to PATH
	log       string // one argv per invocation
	installed string // the .vsix contents, if one was installed
}

func newFakeVSCode(t *testing.T, extensions ...string) fakeVSCode {
	t.Helper()
	code := fakeVSCode{dir: t.TempDir()}
	code.log = filepath.Join(code.dir, "invocations.log")
	code.installed = filepath.Join(code.dir, "installed.vsix")
	script := fmt.Sprintf(`#!/bin/sh
echo "$*" >> %q
if [ "$1" = --list-extensions ]; then
  %s
  exit 0
fi
if [ "$1" = --install-extension ]; then
  cat "$2" > %q
fi
exit 0
`, code.log, listExtensionsBody(extensions), code.installed)
	if err := os.WriteFile(filepath.Join(code.dir, "code"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return code
}

func listExtensionsBody(installed []string) string {
	if len(installed) == 0 {
		return ":" // a no-op: an editor with no extensions prints nothing
	}
	return "printf '%s\\n' " + strings.Join(installed, " ")
}

// vscodeEnv is installEnv with the VS Code step let back in — installEnv
// turns it off for every test that is not about it — and the fake editor
// first on PATH.
func vscodeEnv(server *httptest.Server, binDir, codeDir string, extra ...string) []string {
	var env []string
	for _, entry := range installEnv(server, binDir) {
		if strings.HasPrefix(entry, "GHTMX_SKIP_VSCODE=") {
			continue
		}
		env = append(env, entry)
	}
	env = append(env, "PATH="+codeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return append(env, extra...)
}

func readLog(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
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
// module proxy, and the gopls branches have tests of their own. VS Code
// is off for the same reason and one more — a developer machine running
// these tests may well have `code` on its PATH, and the script would
// then go looking for an extension on the fake release server in every
// test that is not about extensions.
func installEnv(server *httptest.Server, binDir string, extra ...string) []string {
	env := append(baseEnv(),
		"GHTMX_RELEASES_URL="+server.URL,
		"GHTMX_BIN_DIR="+binDir,
		"GHTMX_SKIP_GOPLS=1",
		"GHTMX_SKIP_VSCODE=1",
	)
	return append(env, extra...)
}

func runInstallScript(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	root := repoRoot(t)
	cmd := exec.Command("bash", append([]string{filepath.Join(root, "scripts", "install.sh")}, args...)...)
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
		// This test keeps the real PATH for the fake `go`, so it would
		// otherwise reach a developer machine's actual VS Code.
		"GHTMX_SKIP_VSCODE=1",
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
				// The withGo cases keep the real PATH, which on a
				// developer machine may hold a real `code`.
				"GHTMX_SKIP_VSCODE=1",
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
		// A clean PATH is not enough to hide VS Code: the macOS lookup
		// names /Applications outright.
		"GHTMX_SKIP_VSCODE=1",
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

// TestInstallScriptInstallsVSCodeExtension: the assume-yes path. The
// .vsix name is not derivable from the tag (it carries the extension
// version), so the script has to read it off the release's asset list —
// and what it downloads has to be what VS Code is handed.
func TestInstallScriptInstallsVSCodeExtension(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9"}.start(t)
	binDir := t.TempDir()
	code := newFakeVSCode(t)

	env := vscodeEnv(server, binDir, code.dir, "GHTMX_INSTALL_VSCODE=1")
	out, err := runInstallScript(t, env)
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "installed the ghtmx VS Code extension") {
		t.Errorf("output must report the extension install:\n%s", out)
	}
	log := readLog(t, code.log)
	if !strings.Contains(log, "--install-extension") {
		t.Errorf("VS Code must be asked to install the extension, got:\n%s", log)
	}
	if !strings.Contains(log, vsixName) {
		t.Errorf("the install must name %s, got:\n%s", vsixName, log)
	}
	body, err := os.ReadFile(code.installed)
	if err != nil {
		t.Fatalf("no .vsix reached VS Code: %v", err)
	}
	if got := strings.TrimSpace(string(body)); got != vsixBody {
		t.Errorf("VS Code was handed %q, want the downloaded asset %q", got, vsixBody)
	}
}

// TestInstallScriptVSCodeAlreadyInstalled: re-running the script stays
// the no-op it advertises. The check happens before the download, so a
// second run costs no request either.
func TestInstallScriptVSCodeAlreadyInstalled(t *testing.T) {
	skipUnlessPOSIX(t)
	hits := new(atomic.Int64)
	server := fakeRelease{tag: "v9.9.9", assetPageHits: hits}.start(t)
	// VS Code compares extension ids case-insensitively, so a listing
	// that differs only in case is the same extension.
	for _, listed := range []string{vscodeExtensionID, "go-monolith.Ghtmx-VSCode"} {
		t.Run(listed, func(t *testing.T) {
			binDir := t.TempDir()
			code := newFakeVSCode(t, "golang.go", listed)

			env := vscodeEnv(server, binDir, code.dir, "GHTMX_INSTALL_VSCODE=1")
			out, err := runInstallScript(t, env)
			if err != nil {
				t.Fatalf("install failed: %v\n%s", err, out)
			}
			if !strings.Contains(out, "already installed") {
				t.Errorf("output must say the extension is already there:\n%s", out)
			}
			if log := readLog(t, code.log); strings.Contains(log, "--install-extension") {
				t.Errorf("an installed extension must not be installed again, got:\n%s", log)
			}
		})
	}
	if got := hits.Load(); got != 0 {
		t.Errorf("an already-installed extension must cost no request; the asset list was fetched %d times", got)
	}
}

// TestInstallScriptVSCodeNewestAsset: a release carrying more than one
// .vsix — a re-upload, or a second packaged build — gets the highest
// version installed rather than whichever GitHub happens to render
// first. Ordering here is deliberately wrong for a first-match reader.
func TestInstallScriptVSCodeNewestAsset(t *testing.T) {
	skipUnlessPOSIX(t)
	// vsixName (0.1.0) is listed first; 0.2.10 is the newest, and beats
	// 0.2.2 only if the versions are compared as numbers.
	server := fakeRelease{
		tag:       "v9.9.9",
		extraVsix: []string{"ghtmx-vscode-0.2.10.vsix", "ghtmx-vscode-0.2.2.vsix"},
	}.start(t)
	binDir := t.TempDir()
	code := newFakeVSCode(t)

	env := vscodeEnv(server, binDir, code.dir, "GHTMX_INSTALL_VSCODE=1")
	out, err := runInstallScript(t, env)
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	body, err := os.ReadFile(code.installed)
	if err != nil {
		t.Fatalf("no .vsix reached VS Code: %v\n%s", err, out)
	}
	if got := strings.TrimSpace(string(body)); got != "payload of ghtmx-vscode-0.2.10.vsix" {
		t.Errorf("VS Code was handed %q, want the newest extension asset", got)
	}
}

// TestInstallScriptExtensionID: the id the script looks for is
// editors/vscode/package.json's publisher and name. Rename either and
// the "already installed" check silently stops matching, which shows up
// as a prompt on every run rather than as a failure.
func TestInstallScriptExtensionID(t *testing.T) {
	// No skip: this one only reads two files, so it holds the script and
	// the manifest together on every platform.
	root := repoRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, "editors", "vscode", "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Name      string `json:"name"`
		Publisher string `json:"publisher"`
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	want := manifest.Publisher + "." + manifest.Name
	if want != vscodeExtensionID {
		t.Errorf("this test's id is %q, but the manifest says %q", vscodeExtensionID, want)
	}
	script, err := os.ReadFile(filepath.Join(root, "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(script), want) {
		t.Errorf("install.sh must look for the extension id %q", want)
	}
}

// TestInstallScriptVSCodeNoInteractive: the flag the VS Code extension
// passes. It must not prompt and must not install — the extension asking
// for the binaries is already installed by definition — but it should
// still say how to get one, since the flag can also come from a user.
func TestInstallScriptVSCodeNoInteractive(t *testing.T) {
	skipUnlessPOSIX(t)
	hits := new(atomic.Int64)
	server := fakeRelease{tag: "v9.9.9", assetPageHits: hits}.start(t)
	binDir := t.TempDir()
	code := newFakeVSCode(t)

	env := vscodeEnv(server, binDir, code.dir)
	out, err := runInstallScript(t, env, "--no-interactive")
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "--no-interactive") {
		t.Errorf("output must explain why nothing was installed:\n%s", out)
	}
	if !strings.Contains(out, "code --install-extension") {
		t.Errorf("output must give the manual command:\n%s", out)
	}
	if log := readLog(t, code.log); strings.Contains(log, "--install-extension") {
		t.Errorf("--no-interactive must install nothing, got:\n%s", log)
	}
	if got := hits.Load(); got != 0 {
		t.Errorf("nothing may be fetched without consent; the asset list was fetched %d times", got)
	}
}

// TestInstallScriptVSCodeWithoutATerminal: the offer is a question, so
// with no terminal to ask it on — a pipe, CI, a captured run like this
// one — the answer is no. Nothing may block waiting for input.
func TestInstallScriptVSCodeWithoutATerminal(t *testing.T) {
	skipUnlessPOSIX(t)
	hits := new(atomic.Int64)
	server := fakeRelease{tag: "v9.9.9", assetPageHits: hits}.start(t)
	binDir := t.TempDir()
	code := newFakeVSCode(t)

	out, err := runInstallScript(t, vscodeEnv(server, binDir, code.dir))
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "skipping the VS Code extension") {
		t.Errorf("output must report the skip:\n%s", out)
	}
	if log := readLog(t, code.log); strings.Contains(log, "--install-extension") {
		t.Errorf("an unanswerable question must install nothing, got:\n%s", log)
	}
	if got := hits.Load(); got != 0 {
		t.Errorf("a declined offer must cost no request; the asset list was fetched %d times", got)
	}
}

// TestInstallScriptVSCodeOptOut: GHTMX_SKIP_VSCODE keeps the script away
// from the editor entirely — not even the "is it installed?" call. Every
// other test in this file leans on that, so it gets its own check.
func TestInstallScriptVSCodeOptOut(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9"}.start(t)
	binDir := t.TempDir()
	code := newFakeVSCode(t)

	env := append(vscodeEnv(server, binDir, code.dir, "GHTMX_INSTALL_VSCODE=1"),
		"GHTMX_SKIP_VSCODE=1")
	out, err := runInstallScript(t, env)
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	// Not a bare "VS Code": the closing advice on macOS mentions the
	// editor for an unrelated reason (PATH inheritance from Finder).
	if strings.Contains(out, "VS Code extension") {
		t.Errorf("the opt-out must silence the whole step:\n%s", out)
	}
	if log := readLog(t, code.log); log != "" {
		t.Errorf("VS Code must not be run at all, got:\n%s", log)
	}
}

// TestInstallScriptVSCodeAssetMissing: a release whose extension
// packaging job failed carries no .vsix. That costs the extension and
// nothing else — the binaries are installed by then.
func TestInstallScriptVSCodeAssetMissing(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9", noVsix: true}.start(t)
	binDir := t.TempDir()
	code := newFakeVSCode(t)

	env := vscodeEnv(server, binDir, code.dir, "GHTMX_INSTALL_VSCODE=1")
	out, err := runInstallScript(t, env)
	if err != nil {
		t.Fatalf("a missing extension asset must not fail the run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "could be found among release") {
		t.Errorf("output must say the asset is absent:\n%s", out)
	}
	// The message must not blame the packaging job outright: the same
	// branch fires for an asset named in a way the script cannot read.
	if !strings.Contains(out, "does not recognize") {
		t.Errorf("output must allow for the other cause:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(binDir, "ghtmx")); err != nil {
		t.Errorf("ghtmx must stay installed: %v", err)
	}
}

// TestInstallScriptVSCodeInstallFails: the editor rejecting the .vsix is
// the same class of problem as gopls failing to build — it costs the
// extension, not the run.
func TestInstallScriptVSCodeInstallFails(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9"}.start(t)
	binDir := t.TempDir()

	// A `code` that lists nothing and refuses the install.
	dir := t.TempDir()
	script := "#!/bin/sh\nif [ \"$1\" = --list-extensions ]; then exit 0; fi\necho 'corrupt vsix' >&2\nexit 1\n"
	if err := os.WriteFile(filepath.Join(dir, "code"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	env := vscodeEnv(server, binDir, dir, "GHTMX_INSTALL_VSCODE=1")
	out, err := runInstallScript(t, env)
	if err != nil {
		t.Fatalf("a rejected extension must not fail the run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "VS Code rejected") {
		t.Errorf("output must report the rejection:\n%s", out)
	}
	if !strings.Contains(out, "ghtmx version: v9.9.9") {
		t.Errorf("the run must still finish with its summary:\n%s", out)
	}
}

// TestInstallScriptWithoutVSCode: most people installing a command-line
// tool have no VS Code. They get no message about one.
func TestInstallScriptWithoutVSCode(t *testing.T) {
	skipUnlessPOSIX(t)
	// The script also looks inside /Applications, which no environment
	// variable can hide. On a Mac that has VS Code there, "without" is
	// not a state this test can create.
	if _, err := os.Stat("/Applications/Visual Studio Code.app"); err == nil {
		t.Skip("VS Code is installed system-wide; the absent case cannot be staged")
	}
	server := fakeRelease{tag: "v9.9.9"}.start(t)
	binDir := t.TempDir()

	// shimPath holds what the script shells out to and nothing else, so
	// `code` is genuinely absent however this machine is set up.
	env := append(baseEnv(),
		"GHTMX_RELEASES_URL="+server.URL,
		"GHTMX_BIN_DIR="+binDir,
		"GHTMX_SKIP_GOPLS=1",
		"PATH="+shimPath(t),
		// HOME is where find_vscode looks for a macOS app bundle; a temp
		// directory guarantees the developer's own does not answer.
		"HOME="+t.TempDir(),
	)
	out, err := runInstallScript(t, env)
	if err != nil {
		t.Fatalf("install failed: %v\n%s", err, out)
	}
	if strings.Contains(out, "VS Code extension") {
		t.Errorf("no editor, no extension talk:\n%s", out)
	}
}

// TestInstallScriptUnknownFlag: the flag list is short and closed, so a
// typo has to fail rather than install something silently different.
func TestInstallScriptUnknownFlag(t *testing.T) {
	skipUnlessPOSIX(t)
	server := fakeRelease{tag: "v9.9.9"}.start(t)
	binDir := t.TempDir()

	out, err := runInstallScript(t, installEnv(server, binDir), "--interactive")
	if err == nil {
		t.Fatalf("an unknown flag must fail the run:\n%s", out)
	}
	if !strings.Contains(out, "unknown option --interactive") {
		t.Errorf("output must name the bad flag:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(binDir, "ghtmx")); !os.IsNotExist(err) {
		t.Errorf("arguments are checked before anything is installed (stat: %v)", err)
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
