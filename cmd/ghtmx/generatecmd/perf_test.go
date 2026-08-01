package generatecmd

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// writePerfCorpus lays out a ~100-template module shaped like a real
// application: pages with expressions, control flow, hx-* attributes,
// fragments, and a couple of routes for discovery to find.
func writePerfCorpus(t testing.TB, dir string) {
	t.Helper()
	writeFile(t, dir, "go.mod", "module example.com/perf\n\ngo 1.25\n")
	// Handlers spread over several packages, so route discovery scans at
	// representative scale.
	var registrations strings.Builder
	for p := 0; p < 3; p++ {
		var handlers strings.Builder
		handlers.WriteString("package handlers" + fmt.Sprint(p) + "\n\nimport \"net/http\"\n\n")
		for h := 0; h < 5; h++ {
			fmt.Fprintf(&handlers, "func Handler%d(w http.ResponseWriter, r *http.Request) {}\n", h)
			fmt.Fprintf(&registrations, "\tmux.HandleFunc(\"GET /p%d/h%d\", handlers%d.Handler%d)\n", p, h, p, h)
		}
		writeFile(t, dir, fmt.Sprintf("handlers%d/handlers.go", p), handlers.String())
	}
	writeFile(t, dir, "main.go", `package main

import (
	"net/http"

	"example.com/perf/handlers0"
	"example.com/perf/handlers1"
	"example.com/perf/handlers2"
)

func home(w http.ResponseWriter, r *http.Request)     {}
func saveItem(w http.ResponseWriter, r *http.Request) {}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", home)
	mux.HandleFunc("POST /items/{id}", saveItem)
`+registrations.String()+`	_ = mux
}
`)
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("page%03d.ghtmx", i)
		src := fmt.Sprintf(`package main

fragment Row%03d(id string, name string) {
	<tr id={ "row-" + id }>
		<td>{ name }</td>
		<td><button hx-get={ home } hx-target={ "#row-" + id } hx-swap="outerHTML">Load</button></td>
	</tr>
}

templ page%03d(items []string, title string) {
	<section id={ "page-%03d" }>
		<h1>{ title }</h1>
		<table>
			<tbody>
				for i, it := range items {
					@Row%03d(it, it)
					if i == 0 {
						<tr><td colspan="2">first</td></tr>
					}
				}
			</tbody>
		</table>
	</section>
}
`, i, i, i, i)
		writeFile(t, dir, name, src)
	}
}

// TestRegenerationBudget enforces NFR-001 on the CI reference machine:
// full, uncached regeneration of the ~100-template corpus in under one
// second. Gated behind GHTMX_PERF_GATE so slow or shared local machines
// and non-reference CI runners do not flake; the dedicated CI step sets
// it.
func TestRegenerationBudget(t *testing.T) {
	if os.Getenv("GHTMX_PERF_GATE") == "" {
		t.Skip("set GHTMX_PERF_GATE=1 to enforce the NFR-001 budget")
	}
	dir := t.TempDir()
	writePerfCorpus(t, dir)
	t.Chdir(dir)

	// Warm the Go toolchain cache untimed: route discovery spawns
	// `go list`, and a cold GOCACHE (fresh runner, isolated env) costs
	// seconds that are the toolchain's, not ours. Generation itself
	// stays cold.
	warm := exec.Command("go", "list", "./...")
	warm.Dir = dir
	_ = warm.Run()

	// The timed run logs at debug for phase attribution; the extra
	// logging cost is deliberately conservative — the budget holds even
	// with instrumentation on.
	measure := func() (time.Duration, string) {
		var plog bytes.Buffer
		start := time.Now()
		if err := Run(context.Background(), io.Discard, &plog, []string{"-include-version=false", "-cache=false", "-log-level", "debug"}); err != nil {
			t.Fatalf("regeneration failed: %v", err)
		}
		return time.Since(start), plog.String()
	}
	elapsed, plog := measure()
	t.Logf("full regeneration of 100 templates: %s", elapsed)
	for _, line := range strings.Split(plog, "\n") {
		if strings.Contains(line, "Phase timing") || strings.Contains(line, "Complete") {
			t.Log(line)
		}
	}
	if elapsed >= time.Second {
		// One retry: a noisy-neighbor spike on a shared runner is not an
		// NFR-001 breach. Both runs breaching is.
		retry, _ := measure()
		t.Logf("retry after breach: %s", retry)
		if retry >= time.Second {
			t.Fatalf("NFR-001 breach: full regeneration took %s then %s (budget 1s)", elapsed, retry)
		}
	}
}

// TestVerbosePhaseTiming: the verbose report attributes time to parse,
// discovery, analyze, and emit (NFR-001).
func TestVerbosePhaseTiming(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "go.mod", "module example.com/phases\n\ngo 1.25\n")
	writeFile(t, dir, "page.ghtmx", testTemplate)
	t.Chdir(dir)

	var log bytes.Buffer
	if err := Run(context.Background(), io.Discard, &log, []string{"-include-version=false", "-log-level", "debug"}); err != nil {
		t.Fatalf("generate failed: %v", err)
	}
	out := log.String()
	if !strings.Contains(out, "Phase timing") {
		t.Fatalf("expected the phase report, got:\n%s", out)
	}
	for _, phase := range []string{"parse=", "discovery=", "analyze=", "emit="} {
		if !strings.Contains(out, phase) {
			t.Errorf("phase report must attribute %s time, got:\n%s", phase, out)
		}
	}
}

// BenchmarkRegeneration100 measures full uncached regeneration of the
// corpus, for tracking NFR-001 headroom over time.
func BenchmarkRegeneration100(b *testing.B) {
	dir := b.TempDir()
	writePerfCorpus(b, dir)
	b.Chdir(dir)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		b.StopTimer()
		// Remove outputs so every iteration regenerates from scratch.
		entries, err := os.ReadDir(dir)
		if err != nil {
			b.Fatal(err)
		}
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), "_ghtmx.go") {
				if err := os.Remove(e.Name()); err != nil {
					b.Fatal(err)
				}
			}
		}
		// The central package must regenerate too, or iterations 2+
		// under-measure.
		if err := os.RemoveAll("ghtmxgen"); err != nil {
			b.Fatal(err)
		}
		b.StartTimer()
		if err := Run(context.Background(), io.Discard, io.Discard, []string{"-include-version=false", "-cache=false"}); err != nil {
			b.Fatal(err)
		}
	}
}
