// Package vulncheck is the NFR-008 release gate: govulncheck runs over
// every module in the repository on every CI build, and a known
// vulnerability with a reachable call path fails the build — blocking
// release — unless it is explicitly accepted in accepted.json with a
// recorded rationale. Findings in required-but-uncalled code are
// govulncheck's informational tier: they are logged, but do not gate,
// matching the tool's own reporting.
//
// The gate is env-gated (GHTMX_VULN_GATE=1) because it needs the
// network for the vulnerability database and the govulncheck binary on
// PATH; the dedicated CI job provides both.
package vulncheck

import (
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

type acceptedRecord struct {
	Doc      string `json:"_doc"`
	Accepted []struct {
		ID         string `json:"id"`
		Module     string `json:"module"`
		Rationale  string `json:"rationale"`
		AcceptedOn string `json:"accepted_on"`
	} `json:"accepted"`
}

// finding is the subset of govulncheck's streaming JSON the gate reads.
// Trace frames are ordered from the vulnerable symbol to the entry
// point; a frame with an empty Function is a package- or module-level
// (informational) finding.
type finding struct {
	OSV   string `json:"osv"`
	Trace []struct {
		Module   string `json:"module"`
		Function string `json:"function"`
	} `json:"trace"`
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the vulncheck source file")
	}
	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// modules discovers every Go module in the repository, so a future
// nested module cannot be silently unscanned.
func modules(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == "go.mod" {
			out = append(out, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	if len(out) < 6 {
		t.Fatalf("only %d modules discovered — the layout moved?", len(out))
	}
	return out
}

// scan runs govulncheck over one module. It returns the reachable
// findings (OSV id keyed to the vulnerable module) and the
// informational OSV ids (required but not called).
func scan(t *testing.T, dir string) (called map[string]string, informational []string) {
	t.Helper()
	cmd := exec.Command("govulncheck", "-format", "json", "./...")
	cmd.Dir = dir
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("govulncheck failed to start (is it installed?): %v", err)
	}
	called = map[string]string{}
	seenInformational := map[string]bool{}
	decoder := json.NewDecoder(stdout)
	for {
		var message struct {
			Finding *finding `json:"finding"`
		}
		if err := decoder.Decode(&message); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("cannot parse govulncheck output: %v", err)
		}
		f := message.Finding
		if f == nil || len(f.Trace) == 0 {
			continue
		}
		// Trace[0] is the vulnerable frame; symbol-level findings (a
		// reachable call path) carry its function name.
		if f.Trace[0].Function == "" {
			if !seenInformational[f.OSV] {
				seenInformational[f.OSV] = true
				informational = append(informational, f.OSV)
			}
			continue
		}
		called[f.OSV] = f.Trace[0].Module
	}
	if err := cmd.Wait(); err != nil {
		// govulncheck's JSON handler exits zero even with findings; a
		// failure here is an execution problem, not a vulnerability.
		t.Fatalf("govulncheck failed: %v", err)
	}
	sort.Strings(informational)
	return called, informational
}

// TestKnownVulnerabilitiesGateTheRelease: NFR-008 — every module scans
// clean, or every reachable finding has an explicit, module-scoped
// acceptance with rationale; acceptances that match nothing must be
// pruned.
func TestKnownVulnerabilitiesGateTheRelease(t *testing.T) {
	if os.Getenv("GHTMX_VULN_GATE") == "" {
		t.Skip("set GHTMX_VULN_GATE=1 to run the NFR-008 govulncheck gate (needs network + govulncheck)")
	}
	root := moduleRoot(t)

	raw, err := os.ReadFile(filepath.Join(root, "internal", "vulncheck", "accepted.json"))
	if err != nil {
		t.Fatalf("the acceptance record is missing: %v", err)
	}
	var record acceptedRecord
	if err := json.Unmarshal(raw, &record); err != nil {
		t.Fatalf("accepted.json is not valid JSON: %v", err)
	}
	for _, entry := range record.Accepted {
		if strings.TrimSpace(entry.ID) == "" {
			t.Errorf("an acceptance entry has no id — every waiver names its OSV id")
		}
		if strings.TrimSpace(entry.Rationale) == "" {
			t.Errorf("acceptance %s has no rationale — a waiver must say why", entry.ID)
		}
	}
	// An acceptance is scoped to the vulnerable module it names; an
	// empty module is an explicit repository-wide waiver.
	acceptedFor := func(id, module string) (index int, ok bool) {
		for i, entry := range record.Accepted {
			if entry.ID == id && (entry.Module == "" || entry.Module == module) {
				return i, true
			}
		}
		return 0, false
	}

	matched := map[int]bool{}
	for _, dir := range modules(t, root) {
		rel, relErr := filepath.Rel(root, dir)
		if relErr != nil {
			rel = dir
		}
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			called, informational := scan(t, dir)
			for _, id := range informational {
				t.Logf("informational: %s is required but not called (https://pkg.go.dev/vuln/%s)", id, id)
			}
			for id, module := range called {
				if index, ok := acceptedFor(id, module); ok {
					matched[index] = true
					t.Logf("accepted finding %s in %s — see accepted.json for the rationale", id, module)
					continue
				}
				t.Errorf("known vulnerability %s reachable in %s — fix the dependency or accept it with rationale in accepted.json (https://pkg.go.dev/vuln/%s)",
					id, module, id)
			}
		})
	}

	if t.Failed() {
		// A failed scan leaves matched incomplete; reporting stale
		// acceptances on top would bury the real failure.
		return
	}
	for i, entry := range record.Accepted {
		if !matched[i] {
			t.Errorf("acceptance %s (%s) matches no current finding — prune it so the record stays honest", entry.ID, entry.Module)
		}
	}
}
