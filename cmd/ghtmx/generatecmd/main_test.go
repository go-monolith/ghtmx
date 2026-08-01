package generatecmd

import (
	"os"
	"testing"
)

// TestMain isolates every test's build cache: without this, test runs on
// stamped builds (CI) would write per-TempDir entries into the user's
// real cache directory and never clean them up. GHTMX_CACHE_DIR scopes
// the isolation to ghtmx — overriding XDG_CACHE_HOME would also blow
// away the Go toolchain's build cache and slow every spawned `go list`
// (route discovery) by seconds.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ghtmx-test-cache-*")
	if err != nil {
		panic(err)
	}
	os.Setenv("GHTMX_CACHE_DIR", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
