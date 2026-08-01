package analyzer

import (
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	"sort"
	"strings"

	"github.com/go-monolith/ghtmx/internal/diag"
)

// The reference graph (FR-033, FR-053): one node per addressable top-level
// declaration (templ template or fragment), one edge per resolvable
// @Name(...) / @pkg.Name(...) reference and per nested fragment
// declaration site (a nested fragment renders where it is declared).
// Method templates are collected but not addressable by name, so they
// contribute usage edges without ever closing a cycle.

// declNode is one declaration and its outgoing references, in source order.
type declNode struct {
	name    string // "" when the declaration is not addressable by name
	kind    string // "templ" or "fragment"
	pkgPath string
	pos     diag.Position
	params  map[string]bool // declared parameter names, for shadow checks
	edges   []edgeRef
}

// edgeRef is one outgoing reference, resolved to a target package at
// collection time (imports are per-file). References through unknown
// aliases are dropped: the Go compiler reports those.
type edgeRef struct {
	targetPkg string
	name      string
	pos       diag.Position
}

// templateName extracts the addressable name of a template declaration
// expression like "Name(params)" or "Name[T any](params)". Method
// templates ("(r Recv) Name(params)") return "": they cannot be referenced
// by a bare identifier.
func templateName(exprValue string) string {
	s := strings.TrimSpace(exprValue)
	for i, r := range s {
		if r == '(' || r == '[' {
			return strings.TrimSpace(s[:i])
		}
	}
	return ""
}

// signatureParams returns the parameter names a declaration's body can
// see, including a method template's receiver name. A name in this set
// shadows any like-named template or fragment. Nil when unparseable —
// shadow checks are then skipped.
func signatureParams(exprValue string) map[string]bool {
	s := strings.TrimSpace(exprValue)
	out := map[string]bool{}
	if strings.HasPrefix(s, "(") {
		// Receiver group of a method template: "(r Recv) Name(params)".
		depth, end := 0, -1
		for i, r := range s {
			switch r {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return nil
		}
		if fields := strings.Fields(s[1:end]); len(fields) > 1 {
			out[fields[0]] = true
		}
		s = strings.TrimSpace(s[end+1:])
	}
	// The parameter list is the first paren group outside type brackets.
	depth, start := 0, -1
	for i, r := range s {
		switch r {
		case '[':
			depth++
		case ']':
			depth--
		case '(':
			if depth == 0 {
				start = i
			}
		}
		if start >= 0 {
			break
		}
	}
	if start < 0 {
		return nil
	}
	expr, err := goparser.ParseExpr("func" + s[start:])
	if err != nil {
		return nil
	}
	ft, ok := expr.(*goast.FuncType)
	if !ok || ft.Params == nil {
		return nil
	}
	for _, field := range ft.Params.List {
		for _, n := range field.Names {
			out[n.Name] = true
		}
	}
	return out
}

// checkGraph emits the whole-set reference-graph diagnostics: GHTMX-W0101
// for a fragment with no incoming reference (FR-033, a warning) and
// GHTMX-E0306 for a reference cycle, listing the full chain (FR-053). The
// traversal is an iterative depth-first search over sorted node keys with
// source-ordered edges, so it is deterministic and does not recurse.
func (s *SetAnalysis) checkGraph(sink *diag.Sink) {
	s.mu.Lock()
	var fileNames []string
	for name := range s.fragments {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	var nodes []*declNode
	for _, fn := range fileNames {
		nodes = append(nodes, s.fragments[fn].nodes...)
	}
	goRefs := s.goFragmentRefs // replaced wholesale, never mutated: safe to alias
	s.mu.Unlock()

	key := func(pkgPath, name string) string { return pkgPath + "\x00" + name }

	// Declared addressable nodes; on duplicates (reported elsewhere as
	// E0301 or by the Go compiler) the first declaration in sorted file
	// order wins, keeping the traversal deterministic.
	declared := map[string]*declNode{}
	for _, n := range nodes {
		if n.name == "" {
			continue
		}
		if _, exists := declared[key(n.pkgPath, n.name)]; !exists {
			declared[key(n.pkgPath, n.name)] = n
		}
	}

	type edge struct {
		to  string
		pos diag.Position
	}
	// Edges from a losing duplicate declaration are dropped entirely —
	// for both usage marking and cycles — so the two checks stay
	// symmetric; the duplicate itself is already an E0301 or Go compile
	// error. Unaddressable nodes (method templates) contribute usage
	// edges but can never be re-entered, so they cannot close a cycle.
	adj := map[string][]edge{}
	incoming := map[string]bool{}
	for _, n := range nodes {
		from := key(n.pkgPath, n.name)
		canonical := n.name == "" || declared[from] == n
		if !canonical {
			continue
		}
		for _, e := range n.edges {
			to := key(e.targetPkg, e.name)
			if _, ok := declared[to]; !ok {
				continue // Outside the compiled set: no static edge.
			}
			incoming[to] = true
			if n.name != "" {
				adj[from] = append(adj[from], edge{to: to, pos: e.pos})
			}
		}
	}

	var keys []string
	for k := range declared {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// GHTMX-W0101: fragments nobody references. Nested fragments always
	// have the declaration-site edge from their enclosing declaration, so
	// only genuinely unreferenced fragments warn. This is a zero-incoming-
	// edge check, not transitive reachability: there are no render roots —
	// any template or fragment can be rendered from a Go handler — so a
	// fragment referenced only by dead templates still counts as used.
	// Handler-rendered fragments count too: a Go-source call to the
	// generated <name>Fragment entry point (collected by route
	// discovery's package load, name-based across packages) marks the
	// fragment as used.
	for _, k := range keys {
		n := declared[k]
		if n.kind != "fragment" || incoming[k] || goRefs[n.name] {
			continue
		}
		sink.Add(diag.UnusedFragment, n.pos,
			fmt.Sprintf("fragment %q is never rendered or bound from any template or handler", n.name),
			fmt.Sprintf("reference it with @%s(...) in a template or render %sFragment(...) from a handler", n.name, n.name))
	}

	// GHTMX-E0306: reference cycles. Iterative three-color depth-first
	// search; each back edge reports one cycle with its full chain.
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	display := func(chain []string) string {
		samePkg := true
		firstPkg, _, _ := strings.Cut(chain[0], "\x00")
		for _, k := range chain {
			if pkg, _, _ := strings.Cut(k, "\x00"); pkg != firstPkg {
				samePkg = false
				break
			}
		}
		parts := make([]string, len(chain))
		for i, k := range chain {
			pkg, name, _ := strings.Cut(k, "\x00")
			if samePkg {
				parts[i] = name
				continue
			}
			parts[i] = pkg + "." + name
		}
		return strings.Join(parts, " -> ")
	}
	// canonicalCycle rotates the cycle (without its closing repeat) to
	// start at its smallest key, so the same cycle found through different
	// back edges dedupes to one report.
	canonicalCycle := func(chain []string) string {
		core := chain[:len(chain)-1]
		min := 0
		for i := range core {
			if core[i] < core[min] {
				min = i
			}
		}
		rotated := make([]string, 0, len(core))
		rotated = append(rotated, core[min:]...)
		rotated = append(rotated, core[:min]...)
		return strings.Join(rotated, "\x01")
	}
	reported := map[string]bool{}
	type frame struct {
		key  string
		next int
	}
	for _, root := range keys {
		if color[root] != white {
			continue
		}
		stack := []frame{{key: root}}
		path := []string{root}
		pathIndex := map[string]int{root: 0}
		color[root] = gray
		for len(stack) > 0 {
			f := &stack[len(stack)-1]
			edges := adj[f.key]
			if f.next >= len(edges) {
				color[f.key] = black
				stack = stack[:len(stack)-1]
				delete(pathIndex, path[len(path)-1])
				path = path[:len(path)-1]
				continue
			}
			e := edges[f.next]
			f.next++
			switch color[e.to] {
			case white:
				color[e.to] = gray
				stack = append(stack, frame{key: e.to})
				pathIndex[e.to] = len(path)
				path = append(path, e.to)
			case gray:
				// Back edge: e.to is on the current path by the gray
				// invariant, so pathIndex has it.
				start := pathIndex[e.to]
				chain := make([]string, 0, len(path)-start+1)
				chain = append(chain, path[start:]...)
				chain = append(chain, e.to)
				if canonical := canonicalCycle(chain); !reported[canonical] {
					reported[canonical] = true
					sink.Add(diag.CircularReference, e.pos,
						fmt.Sprintf("circular reference: %s — rendering would recurse forever", display(chain)),
						"break the cycle: pass the shared markup as a parameter or restructure the components")
				}
			}
		}
	}
}
