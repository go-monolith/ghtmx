// Package buildcache is the on-disk content-hash build cache (solution
// design D6, NFR-001): generated output keyed by SHA-256 of the source
// content, salted with everything else that shapes the output — toolchain
// version, resolved configuration, and the route-binding state. The salt
// is folded into the key, so a toolchain or config change orphans every
// old entry at once. The cache is an optimization, never a correctness
// dependency: a corrupt or unreadable entry is discarded and the unit
// rebuilt, and a miss is never an error.
package buildcache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
)

// entryMagic frames every cache entry; the payload checksum follows it.
var entryMagic = []byte("GHTMXC1\n")

// Store is one on-disk cache. A nil *Store is a valid always-miss cache,
// so callers never branch on cache availability.
type Store struct {
	dir  string
	salt []byte

	hits, misses, puts atomic.Int64
}

// Open returns a store rooted at dir with the given salt. The directory
// is created if missing; when the recorded salt differs from the given
// one the whole directory is wiped first — old-generation entries can
// never match again, so keeping them only grows the cache (this also
// sweeps any crashed writer's temp files). On error the caller should log
// and continue with a nil store — generation must not fail because the
// cache cannot.
func Open(dir string, salt []byte) (*Store, error) {
	marker := filepath.Join(dir, "salt")
	if recorded, err := os.ReadFile(marker); err != nil || !bytes.Equal(recorded, salt) {
		if err := os.RemoveAll(dir); err != nil {
			return nil, fmt.Errorf("buildcache: %w", err)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("buildcache: %w", err)
	}
	if err := os.WriteFile(marker, salt, 0o644); err != nil {
		return nil, fmt.Errorf("buildcache: %w", err)
	}
	return &Store{dir: dir, salt: append([]byte(nil), salt...)}, nil
}

// Salt derives a cache salt from the parts that shape generated output.
// Parts are length-prefixed so no two part lists can collide.
func Salt(parts ...string) []byte {
	h := sha256.New()
	for _, p := range parts {
		fmt.Fprintf(h, "%d:", len(p))
		h.Write([]byte(p))
	}
	return h.Sum(nil)
}

// Key derives the cache key of one unit: the salt, the unit's identity
// (its path relative to the generation root, which itself participates in
// the salt — identical content in two files generates different output),
// and the source content.
func (s *Store) Key(unitID string, content []byte) [sha256.Size]byte {
	h := sha256.New()
	if s != nil {
		h.Write(s.salt)
	}
	h.Write([]byte(unitID))
	h.Write([]byte{0})
	h.Write(content)
	var key [sha256.Size]byte
	h.Sum(key[:0])
	return key
}

// Get returns the cached payload for key. A miss — absent, unreadable, or
// corrupt (which also deletes the entry) — returns ok false, never an
// error.
func (s *Store) Get(key [sha256.Size]byte) (payload []byte, ok bool) {
	if s == nil {
		return nil, false
	}
	path := s.entryPath(key)
	raw, err := os.ReadFile(path)
	if err != nil {
		s.misses.Add(1)
		return nil, false
	}
	if len(raw) < len(entryMagic)+sha256.Size || !bytes.HasPrefix(raw, entryMagic) {
		s.discard(path)
		return nil, false
	}
	sum := raw[len(entryMagic) : len(entryMagic)+sha256.Size]
	payload = raw[len(entryMagic)+sha256.Size:]
	if actual := sha256.Sum256(payload); !bytes.Equal(sum, actual[:]) {
		s.discard(path)
		return nil, false
	}
	s.hits.Add(1)
	return payload, true
}

// discard silently drops a corrupt entry; the unit is simply rebuilt.
func (s *Store) discard(path string) {
	_ = os.Remove(path)
	s.misses.Add(1)
}

// Put stores payload under key, atomically (temp file + rename), so a
// crashed writer can only ever leave a discardable temp file behind.
// Errors are returned for logging but the caller must treat them as
// non-fatal.
func (s *Store) Put(key [sha256.Size]byte, payload []byte) error {
	if s == nil {
		return nil
	}
	path := s.entryPath(key)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("buildcache: %w", err)
	}
	sum := sha256.Sum256(payload)
	entry := make([]byte, 0, len(entryMagic)+sha256.Size+len(payload))
	entry = append(entry, entryMagic...)
	entry = append(entry, sum[:]...)
	entry = append(entry, payload...)

	tmp, err := os.CreateTemp(filepath.Dir(path), "put-*")
	if err != nil {
		return fmt.Errorf("buildcache: %w", err)
	}
	if _, err := tmp.Write(entry); err != nil {
		tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("buildcache: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("buildcache: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("buildcache: %w", err)
	}
	s.puts.Add(1)
	return nil
}

// Stats reports the store's lifetime counters, for the timing report.
func (s *Store) Stats() (hits, misses, puts int64) {
	if s == nil {
		return 0, 0, 0
	}
	return s.hits.Load(), s.misses.Load(), s.puts.Load()
}

// entryPath shards entries over 256 subdirectories.
func (s *Store) entryPath(key [sha256.Size]byte) string {
	name := hex.EncodeToString(key[:])
	return filepath.Join(s.dir, name[:2], name)
}

// DefaultDir returns the per-module cache directory under the user cache
// root: modules do not share entries, so one project's cache growth never
// slows another's lookups. ok is false when no user cache dir exists.
// GHTMX_CACHE_DIR overrides the root — tests isolate through it without
// disturbing XDG_CACHE_HOME, which the Go toolchain's own caches share.
func DefaultDir(modRoot string) (dir string, ok bool) {
	base := os.Getenv("GHTMX_CACHE_DIR")
	if base == "" {
		var err error
		base, err = os.UserCacheDir()
		if err != nil {
			return "", false
		}
	}
	sum := sha256.Sum256([]byte(modRoot))
	return filepath.Join(base, "ghtmx", "build", hex.EncodeToString(sum[:16])), true
}
