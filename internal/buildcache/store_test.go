package buildcache

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The build cache decides whether generation is skipped, so a stale hit
// serves output from a template the user has already changed. The salt
// is what prevents that across toolchain changes: a cache written by a
// different compiler has to be discarded, not reused.

func TestOpenDiscardsACacheWithADifferentSalt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")

	first, err := Open(dir, Salt("v1"))
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte("key"))
	if err := first.Put(key, []byte("output from v1")); err != nil {
		t.Fatal(err)
	}
	if _, ok := first.Get(key); !ok {
		t.Fatal("the entry just written is not readable")
	}

	// A different salt means a different generator: everything written
	// by the old one has to go, or the next build serves its output.
	second, err := Open(dir, Salt("v2"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := second.Get(key); ok {
		t.Error("an entry written under a different salt survived; a toolchain change would serve stale output")
	}
}

func TestOpenKeepsACacheWithTheSameSalt(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	salt := Salt("v1")

	first, err := Open(dir, salt)
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte("key"))
	if err := first.Put(key, []byte("output")); err != nil {
		t.Fatal(err)
	}

	// Reopening with the same salt is the ordinary case: the whole point
	// of the cache is that it survives between runs.
	second, err := Open(dir, salt)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := second.Get(key)
	if !ok {
		t.Fatal("the cache was discarded despite an unchanged salt")
	}
	if string(got) != "output" {
		t.Errorf("Get returned %q, want %q", got, "output")
	}
}

func TestPutAndGetRoundTrip(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "cache"), Salt("v1"))
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		payload []byte
	}{
		{"small", []byte("x")},
		{"empty", []byte{}},
		{"large", bytes.Repeat([]byte("abc"), 10000)},
		{"binary", []byte{0, 1, 2, 255, 254}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := sha256.Sum256([]byte(tt.name))
			if err := store.Put(key, tt.payload); err != nil {
				t.Fatalf("Put: %v", err)
			}
			got, ok := store.Get(key)
			if !ok {
				t.Fatal("Get reported a miss for an entry just written")
			}
			if !bytes.Equal(got, tt.payload) {
				t.Errorf("Get returned %d bytes, want %d", len(got), len(tt.payload))
			}
		})
	}
}

func TestGetReportsAMissForAnUnknownKey(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "cache"), Salt("v1"))
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := store.Get(sha256.Sum256([]byte("never written"))); ok {
		t.Error("Get reported a hit for a key that was never written")
	}
}

// TestGetRejectsACorruptedEntry pins the checksum: a truncated or
// tampered cache file must read as a miss so generation runs again,
// rather than being handed to the compiler as if it were valid Go.
func TestGetRejectsACorruptedEntry(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache")
	store, err := Open(dir, Salt("v1"))
	if err != nil {
		t.Fatal(err)
	}
	key := sha256.Sum256([]byte("key"))
	if err := store.Put(key, []byte("good output")); err != nil {
		t.Fatal(err)
	}

	// Corrupt every entry file on disk.
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(path) == "salt" {
			return err
		}
		return os.WriteFile(path, []byte("corrupted"), 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := store.Get(key); ok {
		t.Error("a corrupted entry was served as a cache hit")
	}
}

// TestNilStoreIsUsable pins that a disabled cache is a nil *Store the
// caller can keep using, which is how -cache=false is implemented.
func TestNilStoreIsUsable(t *testing.T) {
	var store *Store
	key := sha256.Sum256([]byte("key"))

	if err := store.Put(key, []byte("x")); err != nil {
		t.Errorf("Put on a nil store returned %v, want nil", err)
	}
	if _, ok := store.Get(key); ok {
		t.Error("Get on a nil store reported a hit")
	}
}

// TestSaltDistinguishesPartLists pins the length-prefixing: without it
// Salt("ab","c") and Salt("a","bc") would collide, and two different
// toolchains would share a cache.
func TestSaltDistinguishesPartLists(t *testing.T) {
	if bytes.Equal(Salt("ab", "c"), Salt("a", "bc")) {
		t.Error("two different part lists produced the same salt")
	}
	if !bytes.Equal(Salt("a", "b"), Salt("a", "b")) {
		t.Error("the same part list produced different salts")
	}
	if bytes.Equal(Salt("a"), Salt()) {
		t.Error("an empty part list collided with a non-empty one")
	}
}

// TestOpenReportsAnUnusableDirectory pins the failure the caller has to
// tolerate: generation must not fail because the cache cannot be
// created, so Open reports and the caller continues with a nil store.
func TestOpenReportsAnUnusableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o755) })

	if _, err := Open(filepath.Join(parent, "cache"), Salt("v1")); err == nil {
		t.Error("Open succeeded under a read-only parent directory")
	}
}

// TestPutReportsAnUnwritableStore covers the same tolerance on the write
// side: a full disk or a revoked permission is reported for logging, not
// treated as a generation failure.
func TestPutReportsAnUnwritableStore(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: permission bits are not enforced")
	}
	dir := filepath.Join(t.TempDir(), "cache")
	store, err := Open(dir, Salt("v1"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := store.Put(sha256.Sum256([]byte("key")), []byte("payload")); err == nil {
		t.Error("Put succeeded into a read-only cache directory")
	}
}

func TestDefaultDir(t *testing.T) {
	t.Run("honours the override", func(t *testing.T) {
		base := t.TempDir()
		t.Setenv("GHTMX_CACHE_DIR", base)

		dir, ok := DefaultDir("/project")
		if !ok {
			t.Fatal("DefaultDir reported not-ok with an override set")
		}
		if !strings.HasPrefix(dir, base) {
			t.Errorf("dir = %q, want it under the override %q", dir, base)
		}
	})

	t.Run("separates modules", func(t *testing.T) {
		t.Setenv("GHTMX_CACHE_DIR", t.TempDir())

		// Two projects must not share a cache directory, or one
		// project's entries slow the other's lookups.
		a, _ := DefaultDir("/project/a")
		b, _ := DefaultDir("/project/b")
		if a == b {
			t.Errorf("two modules resolved to the same cache directory %q", a)
		}
	})

	t.Run("is stable for one module", func(t *testing.T) {
		t.Setenv("GHTMX_CACHE_DIR", t.TempDir())

		first, _ := DefaultDir("/project")
		second, _ := DefaultDir("/project")
		if first != second {
			t.Errorf("two calls disagreed: %q then %q", first, second)
		}
	})
}
