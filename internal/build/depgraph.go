// Package build holds the dependency graph and two-tier invalidation
// (FR-061, solution design M5). Tier one covers Go source changes: a
// changed route registration invalidates the route table and every unit
// whose generated output embeds route state. Tier two covers .ghtmx
// changes: a changed unit invalidates itself plus every unit whose
// diagnostics depend on it — pages referencing its templates and
// fragments, and files referencing events it declares.
package build

import (
	"fmt"
	"sort"

	"github.com/go-monolith/ghtmx/internal/analyzer"
	"github.com/go-monolith/ghtmx/internal/routes"
)

// Graph is the whole-set dependency graph, built from the analyzer's
// per-file facts. It is immutable; rebuild it after re-analysis.
type Graph struct {
	// dependents maps a file to the files that reference one of its
	// declarations (reverse template/fragment edges).
	dependents map[string][]string
	// eventDeclFiles maps a wire name to the files declaring it;
	// eventRefFiles maps a wire name to the files referencing it.
	eventDeclFiles map[string][]string
	eventRefFiles  map[string][]string
	// boundFiles are the units with route bindings, sorted.
	boundFiles []string
}

// NewGraph builds the graph from the analyzer's dependency facts.
func NewGraph(facts map[string]analyzer.DependencyFacts) *Graph {
	g := &Graph{
		dependents:     map[string][]string{},
		eventDeclFiles: map[string][]string{},
		eventRefFiles:  map[string][]string{},
	}
	declFile := map[string]string{}
	var files []string
	for file := range facts {
		files = append(files, file)
	}
	sort.Strings(files)
	for _, file := range files {
		for _, d := range facts[file].Decls {
			// The first declaring file in sorted order wins, matching the
			// analyzer's duplicate handling.
			if _, taken := declFile[d]; !taken {
				declFile[d] = file
			}
		}
	}
	for _, file := range files {
		f := facts[file]
		seen := map[string]bool{}
		for _, ref := range f.Refs {
			target, ok := declFile[ref]
			if !ok || target == file || seen[target] {
				continue
			}
			seen[target] = true
			g.dependents[target] = append(g.dependents[target], file)
		}
		for _, wire := range f.EventDecls {
			g.eventDeclFiles[wire] = append(g.eventDeclFiles[wire], file)
		}
		for _, wire := range f.EventRefs {
			g.eventRefFiles[wire] = append(g.eventRefFiles[wire], file)
		}
		if f.BindsRoutes {
			g.boundFiles = append(g.boundFiles, file)
		}
	}
	return g
}

// OnTemplateChange returns every unit invalidated by a change to file:
// the file itself, the transitive closure of units referencing its
// declarations (a fragment edit invalidates every page rendering it, even
// through intermediate fragments), and units referencing events the
// changed file declares. The result is sorted; an unrelated template
// returns only itself.
func (g *Graph) OnTemplateChange(file string) []string {
	invalid := map[string]bool{file: true}
	queue := []string{file}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, dep := range g.dependents[current] {
			if invalid[dep] {
				continue
			}
			invalid[dep] = true
			queue = append(queue, dep)
		}
	}
	// Event coupling is one level, in both directions: a file listening
	// for an event declared in the changed file must re-diagnose
	// (E0304/W0102 can flip), and a changed listener re-diagnoses the
	// declaring file (removing the last listener flips W0102).
	for wire, declFiles := range g.eventDeclFiles {
		declaresHere := false
		for _, df := range declFiles {
			if invalid[df] {
				declaresHere = true
				break
			}
		}
		if !declaresHere {
			continue
		}
		for _, rf := range g.eventRefFiles[wire] {
			invalid[rf] = true
		}
	}
	for wire, refFiles := range g.eventRefFiles {
		referencedHere := false
		for _, rf := range refFiles {
			if invalid[rf] {
				referencedHere = true
				break
			}
		}
		if !referencedHere {
			continue
		}
		for _, df := range g.eventDeclFiles[wire] {
			invalid[df] = true
		}
	}
	out := make([]string, 0, len(invalid))
	for f := range invalid {
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// BoundFiles returns the units whose generated output embeds route state:
// tier-one invalidation regenerates exactly these when the route table
// changes.
func (g *Graph) BoundFiles() []string {
	out := make([]string, len(g.boundFiles))
	copy(out, g.boundFiles)
	return out
}

// RoutesChanged reports whether two route tables differ observably:
// verbs, paths, handlers, parameters, nav markers, or order-independent
// membership.
func RoutesChanged(old, updated *routes.Table) bool {
	return routesFingerprint(old) != routesFingerprint(updated)
}

func routesFingerprint(t *routes.Table) string {
	if t == nil {
		return ""
	}
	all := t.All()
	lines := make([]string, 0, len(all))
	for _, r := range all {
		// NavOnly never reaches generated code, but watch mode swaps in a
		// rediscovered table only when the fingerprint changes and the
		// whole-set diagnostics read that table — so a nav toggle must
		// count as a change or W0104 keeps reporting the stale state.
		line := fmt.Sprintf("%q %q %q.%q nav:%t", r.Verb, r.Path, r.Handler.PkgPath, r.Handler.Name, r.NavOnly)
		for _, p := range r.Params {
			line += fmt.Sprintf("/%q:%t", p.Name, p.Wildcard)
		}
		lines = append(lines, line)
	}
	sort.Strings(lines)
	var out string
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}
