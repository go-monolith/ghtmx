package protocol

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

// TestMain skips this package's tests on Windows: the suite is ported
// from templ, whose CI runs Linux-only, and its fixtures assume POSIX
// file URIs and paths (file:///path resolves against the working drive
// on Windows). The production code is exercised on Windows through the
// LSP integration suites; porting these fixtures is tracked follow-up
// work, not silent breakage.
func TestMain(m *testing.M) {
	if runtime.GOOS == "windows" {
		fmt.Println("skipping ported POSIX-path fixtures on windows")
		os.Exit(0)
	}
	os.Exit(m.Run())
}
