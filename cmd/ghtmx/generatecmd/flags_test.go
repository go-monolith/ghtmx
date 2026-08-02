package generatecmd

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-monolith/ghtmx/internal/config"
	"github.com/go-monolith/ghtmx/internal/diag"
)

func TestStringSliceFlag(t *testing.T) {
	var f stringSliceFlag

	if got := f.String(); got != "" {
		t.Errorf("String() on an empty flag = %q, want empty", got)
	}
	for _, v := range []string{"a", "b", "c"} {
		if err := f.Set(v); err != nil {
			t.Fatalf("Set(%q): %v", v, err)
		}
	}
	// Repeatable: each -flag adds, rather than replacing.
	if got, want := f.String(), "a,b,c"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	if len(f) != 3 {
		t.Errorf("collected %d values, want 3", len(f))
	}
}

func TestSeverityMapFlag(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantID  string
		wantSev diag.Severity
		wantErr bool
	}{
		{name: "id and severity", input: "GHTMX-W0101=off", wantID: "GHTMX-W0101", wantSev: "off"},
		{name: "severity with a value", input: "GHTMX-W0102=warning", wantID: "GHTMX-W0102", wantSev: "warning"},
		// Each of these would otherwise be accepted and silently
		// configure nothing, so a user's -check override would appear to
		// work while doing nothing at all.
		{name: "no separator", input: "GHTMX-W0101", wantErr: true},
		{name: "empty id", input: "=off", wantErr: true},
		{name: "empty severity", input: "GHTMX-W0101=", wantErr: true},
		{name: "empty", input: "", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := severityMapFlag{}

			err := f.Set(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Set(%q) succeeded, want an error", tt.input)
				}
				// The message has to show the expected shape, or the
				// user has nothing to correct against.
				if !strings.Contains(err.Error(), "ID=severity") {
					t.Errorf("error %q does not show the expected form", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Set(%q): %v", tt.input, err)
			}
			if got := f[tt.wantID]; got != tt.wantSev {
				t.Errorf("f[%q] = %q, want %q", tt.wantID, got, tt.wantSev)
			}
		})
	}
}

func TestSeverityMapFlagStringIsEmpty(t *testing.T) {
	// The flag package prints this as the default value; an empty string
	// is deliberate, since a map has no useful default rendering.
	if got := (severityMapFlag{"a": "off"}).String(); got != "" {
		t.Errorf("String() = %q, want empty", got)
	}
}

func TestArgumentError(t *testing.T) {
	err := &ArgumentError{Message: "bad flag"}

	if got := err.Error(); got != "bad flag" {
		t.Errorf("Error() = %q, want %q", got, "bad flag")
	}
	// 64 is EX_USAGE: scripts distinguish "you typed it wrong" from
	// "generation failed", and collapsing them would break that.
	if got := err.Code(); got != 64 {
		t.Errorf("Code() = %d, want 64 (EX_USAGE)", got)
	}
}

// writeCertificate emits a self-signed certificate, which is what the
// TLS transport is built from.
func writeCertificate(t *testing.T, path string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "ghtmx-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(path, pemBytes, 0o644); err != nil {
		t.Fatal(err)
	}
}

// createTLSTransport is how -proxy reaches an HTTPS backend using a
// certificate the system store does not know. Returning nil on failure
// means the proxy silently falls back to the default transport, so both
// outcomes are worth pinning.
func TestCreateTLSTransport(t *testing.T) {
	t.Run("a valid certificate produces a transport", func(t *testing.T) {
		certPath := filepath.Join(t.TempDir(), "cert.pem")
		writeCertificate(t, certPath)

		cmd := &Generate{Log: quietLog()}
		cmd.Args.ProxyTLSCrt = certPath

		transport := cmd.createTLSTransport()
		if transport == nil {
			t.Fatal("createTLSTransport returned nil for a valid certificate")
		}
		if transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
			t.Error("the transport carries no root CA pool, so the certificate would not be trusted")
		}
	})

	t.Run("a missing file yields no transport", func(t *testing.T) {
		cmd := &Generate{Log: quietLog()}
		cmd.Args.ProxyTLSCrt = filepath.Join(t.TempDir(), "absent.pem")

		if got := cmd.createTLSTransport(); got != nil {
			t.Error("createTLSTransport returned a transport for a missing certificate file")
		}
	})

	t.Run("a file that is not a certificate yields no transport", func(t *testing.T) {
		certPath := filepath.Join(t.TempDir(), "cert.pem")
		if err := os.WriteFile(certPath, []byte("not a certificate"), 0o644); err != nil {
			t.Fatal(err)
		}
		cmd := &Generate{Log: quietLog()}
		cmd.Args.ProxyTLSCrt = certPath

		if got := cmd.createTLSTransport(); got != nil {
			t.Error("createTLSTransport returned a transport for a file holding no certificate")
		}
	})
}

// deleteWatchModeTextFiles clears the sidecar text files watch mode
// writes. Leaving one behind makes the next non-watch build serve stale
// literals, which looks like an edit that did not take.
func TestDeleteWatchModeTextFiles(t *testing.T) {
	dir := t.TempDir()

	templPath := filepath.Join(dir, "page.ghtmx")
	if err := os.WriteFile(templPath, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goPath := filepath.Join(dir, "page_ghtmx.go")
	if err := os.WriteFile(goPath, []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A file the sweep must leave alone.
	otherPath := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(otherPath, []byte("notes"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := &Generate{Log: quietLog()}
	cmd.Args.Path = dir
	cmd.Args.Config = config.Config{}
	cmd.Args.Config.GeneratedSuffix = "_ghtmx.go"

	// Missing sidecar files are not an error: watch mode may never have
	// run here.
	if err := cmd.deleteWatchModeTextFiles(); err != nil {
		t.Fatalf("deleteWatchModeTextFiles on a clean tree: %v", err)
	}

	// The source files themselves must survive.
	for _, path := range []string{templPath, goPath, otherPath} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("%s was removed: %v", filepath.Base(path), err)
		}
	}
}

// TestDeleteWatchModeTextFilesHonoursShouldSkip pins that the ignore
// predicate is respected, so a directory the user excluded is not walked
// into.
func TestDeleteWatchModeTextFilesHonoursShouldSkip(t *testing.T) {
	dir := t.TempDir()
	skipped := filepath.Join(dir, "excluded")
	if err := os.MkdirAll(skipped, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skipped, "page.ghtmx"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var skippedPaths []string
	cmd := &Generate{
		Log: quietLog(),
		ShouldSkip: func(path string) bool {
			if strings.HasPrefix(path, "excluded") {
				skippedPaths = append(skippedPaths, path)
				return true
			}
			return false
		},
	}
	cmd.Args.Path = dir
	cmd.Args.Config.GeneratedSuffix = "_ghtmx.go"

	if err := cmd.deleteWatchModeTextFiles(); err != nil {
		t.Fatal(err)
	}
	if len(skippedPaths) == 0 {
		t.Error("ShouldSkip was never consulted; an excluded directory would be walked")
	}
}
