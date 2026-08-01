package buildcache

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCacheSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	salt := Salt("ghtmx-0.1", "confighash")

	first, err := Open(dir, salt)
	if err != nil {
		t.Fatal(err)
	}
	key := first.Key("app/page.ghtmx", []byte("templ page() {}"))
	if _, ok := first.Get(key); ok {
		t.Fatal("a fresh cache must miss")
	}
	if err := first.Put(key, []byte("generated go code")); err != nil {
		t.Fatal(err)
	}

	// A new Store on the same directory models a process restart.
	second, err := Open(dir, salt)
	if err != nil {
		t.Fatal(err)
	}
	payload, ok := second.Get(second.Key("app/page.ghtmx", []byte("templ page() {}")))
	if !ok || string(payload) != "generated go code" {
		t.Fatalf("an unchanged unit must be served across restarts, got %q ok=%v", payload, ok)
	}
}

func TestSaltChangeInvalidatesEverything(t *testing.T) {
	dir := t.TempDir()
	old, err := Open(dir, Salt("ghtmx-0.1", "config-a"))
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("templ page() {}")
	if err := old.Put(old.Key("p.ghtmx", content), []byte("out")); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name string
		salt []byte
	}{
		{"toolchain upgrade", Salt("ghtmx-0.2", "config-a")},
		{"config change", Salt("ghtmx-0.1", "config-b")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Open(dir, tt.salt)
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := s.Get(s.Key("p.ghtmx", content)); ok {
				t.Error("a salt change must invalidate the whole cache")
			}
		})
	}
}

func TestUnitIdentityInKey(t *testing.T) {
	s, err := Open(t.TempDir(), Salt("v"))
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("identical content")
	if s.Key("a.ghtmx", content) == s.Key("b.ghtmx", content) {
		t.Error("identical content in different units must key differently: generated output embeds the file name")
	}
}

func TestCorruptEntryDiscardedSilently(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir, Salt("v"))
	if err != nil {
		t.Fatal(err)
	}
	key := s.Key("p.ghtmx", []byte("src"))
	if err := s.Put(key, []byte("good output")); err != nil {
		t.Fatal(err)
	}
	path := s.entryPath(key)

	for _, tt := range []struct {
		name    string
		corrupt []byte
	}{
		{"truncated", []byte("GHTMXC1\nshort")},
		{"bad magic", append([]byte("BOGUS!!\n"), make([]byte, 64)...)},
		{"checksum mismatch", func() []byte {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			raw[len(raw)-1] ^= 0xFF
			return raw
		}()},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := s.Put(key, []byte("good output")); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tt.corrupt, 0o644); err != nil {
				t.Fatal(err)
			}
			if _, ok := s.Get(key); ok {
				t.Fatal("a corrupt entry must miss")
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Error("a corrupt entry must be deleted")
			}
			// The unit rebuilds and caches again.
			if err := s.Put(key, []byte("rebuilt")); err != nil {
				t.Fatal(err)
			}
			if payload, ok := s.Get(key); !ok || string(payload) != "rebuilt" {
				t.Errorf("rebuild after corruption must work, got %q ok=%v", payload, ok)
			}
		})
	}
}

func TestNilStoreAlwaysMisses(t *testing.T) {
	var s *Store
	key := s.Key("p.ghtmx", []byte("src"))
	if _, ok := s.Get(key); ok {
		t.Error("a nil store must miss")
	}
	if err := s.Put(key, []byte("x")); err != nil {
		t.Errorf("a nil store must accept puts as no-ops, got %v", err)
	}
	if h, m, p := s.Stats(); h != 0 || m != 0 || p != 0 {
		t.Error("a nil store has zero stats")
	}
}

func TestStatsCount(t *testing.T) {
	s, err := Open(t.TempDir(), Salt("v"))
	if err != nil {
		t.Fatal(err)
	}
	key := s.Key("p.ghtmx", []byte("src"))
	s.Get(key)                        // miss
	_ = s.Put(key, []byte("payload")) // put
	s.Get(key)                        // hit
	if h, m, p := s.Stats(); h != 1 || m != 1 || p != 1 {
		t.Errorf("stats = %d hits, %d misses, %d puts; want 1, 1, 1", h, m, p)
	}
}

func TestDefaultDirPerModule(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	a, ok := DefaultDir("/work/project-a")
	if !ok {
		t.Fatal("expected a cache dir")
	}
	b, _ := DefaultDir("/work/project-b")
	if a == b {
		t.Error("modules must not share cache directories")
	}
	if filepath.Base(filepath.Dir(a)) != "build" {
		t.Errorf("unexpected layout: %s", a)
	}
}

func TestSaltMismatchWipesDirectory(t *testing.T) {
	dir := t.TempDir()
	old, err := Open(dir, Salt("old"))
	if err != nil {
		t.Fatal(err)
	}
	key := old.Key("p.ghtmx", []byte("src"))
	if err := old.Put(key, []byte("out")); err != nil {
		t.Fatal(err)
	}
	// Simulate a crashed writer's leftover temp file.
	litter := filepath.Join(dir, "ab", "put-123")
	if err := os.MkdirAll(filepath.Dir(litter), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(litter, []byte("junk"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(dir, Salt("new")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "salt" {
		t.Errorf("a salt change must wipe the directory, got %v", entries)
	}
}

func TestSaltPartsCannotCollide(t *testing.T) {
	if string(Salt("ab", "c")) == string(Salt("a", "bc")) {
		t.Error("length prefixing must keep part boundaries distinct")
	}
}
