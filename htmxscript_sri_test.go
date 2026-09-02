package ghtmx

import (
	"crypto/sha512"
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// TestPinnedIntegrityMatchesPublishedAssets re-hashes every pinned
// htmx build from the CDN and compares it with the embedded SRI value.
// Generation and the ordinary test suite perform no network I/O, so the
// check is opt-in: GHTMX_SRI_CHECK=1 go test -run PinnedIntegrity .
// Run it when adding a version, and at release time — a mistyped hash
// would otherwise surface only as a browser refusing the script.
func TestPinnedIntegrityMatchesPublishedAssets(t *testing.T) {
	if os.Getenv("GHTMX_SRI_CHECK") == "" {
		t.Skip("set GHTMX_SRI_CHECK=1 to verify the pinned hashes against the CDN")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	for _, version := range SupportedHtmxVersions() {
		url := "https://cdn.jsdelivr.net/npm/htmx.org@" + version + "/dist/htmx.min.js"
		resp, err := client.Get(url)
		if err != nil {
			t.Fatalf("%s: %v", url, err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s: status %d, err %v", url, resp.StatusCode, err)
		}
		sum := sha512.Sum384(body)
		got := "sha384-" + base64.StdEncoding.EncodeToString(sum[:])
		if got != htmxIntegrity[version] {
			t.Errorf("htmx %s: published asset hashes to %s, pinned %s", version, got, htmxIntegrity[version])
		}
	}
}
