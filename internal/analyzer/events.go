package analyzer

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/go-monolith/ghtmx/internal/diag"
	parser "github.com/go-monolith/ghtmx/internal/parser"
)

// Server-driven event registry (FR-037). Event declarations are global to
// the compiled set — the emission symbols share one generated package — so
// wire names must be unique everywhere (GHTMX-E0305). Template-side
// references to the event namespace resolve against the registry
// (GHTMX-E0304), and declared events nobody references warn (GHTMX-W0102).
//
// The compiler owns the kebab-case event namespace: an hx-on listener or
// hx-trigger event token whose name contains a dash and no ':' or '.'
// namespace qualifier is a reference to a declared event. Dash-less names
// (DOM events such as click) and qualified names (htmx:..., sse:...) are
// outside the contract and pass untouched.

// EventInfo describes one declared event in the compiled set.
type EventInfo struct {
	Name     string // Go-side name, e.g. "UserCreated"
	WireName string // HX-Trigger name, e.g. "user-created"
	Params   string // declared payload parameter list, e.g. "(id string)"
	Pos      diag.Position
}

// eventRef is one template-side reference to the event namespace.
type eventRef struct {
	wire string
	attr string // the attribute the reference appeared in
	pos  diag.Position
}

// WireName converts a declared event name to its kebab-case HX-Trigger
// wire name: UserCreated becomes user-created, UserID becomes user-id,
// HTMLLoaded becomes html-loaded. Attribute names are lowercased by HTML,
// so only a lower-case wire name can be listened to with hx-on. Trailing
// letters after an acronym split unhelpfully (UserIDs becomes user-i-ds)
// — prefer names without embedded plural acronyms.
func WireName(name string) string {
	var b strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			boundary := i > 0 && (!unicode.IsUpper(runes[i-1]) ||
				(i+1 < len(runes) && unicode.IsLower(runes[i+1])))
			if boundary {
				b.WriteByte('-')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// collectEventDecl records a declaration into the file's facts.
func collectEventDecl(e *parser.EventDeclaration, filePath string, facts *fileFacts) {
	params := ""
	if i := strings.IndexByte(e.Expression.Value, '('); i >= 0 {
		params = e.Expression.Value[i:]
	}
	facts.events = append(facts.events, EventInfo{
		Name:     e.Name,
		WireName: WireName(e.Name),
		Params:   params,
		Pos: diag.Position{
			File:  filePath,
			Line:  e.Range.From.Line + 1,
			Col:   e.Range.From.Col + 1,
			Index: e.Range.From.Index,
		},
	})
}

// EventNameFromAttr extracts the event a constant hx-on attribute listens
// to, if it is in the compiler-owned namespace. Exported so the LSP layer
// resolves references under exactly the rules diagnostics enforce.
func EventNameFromAttr(attrName string) (wire string, ok bool) {
	suffix := ""
	for _, prefix := range []string{"hx-on:", "hx-on-"} {
		if s, cut := strings.CutPrefix(attrName, prefix); cut {
			suffix = s
			break
		}
	}
	switch {
	case suffix == "":
		return "", false // Not an hx-on attribute.
	case strings.HasPrefix(suffix, "-"),
		strings.HasPrefix(suffix, "htmx-"):
		// The htmx namespace shorthands: hx-on::x, hx-on--x, hx-on-htmx-x.
		return "", false
	case strings.ContainsAny(suffix, ":."):
		// Any qualified name (hx-on:htmx:x, hx-on:my.custom-event) is
		// outside the contract.
		return "", false
	case !strings.Contains(suffix, "-"):
		return "", false // Dash-less names are DOM events.
	}
	return suffix, true
}

// EventNamesFromTrigger extracts the compiler-owned event names of a
// constant hx-trigger value. Filter brackets are stripped verbatim first;
// each comma-separated spec's first token is its event name. Exported so
// the LSP layer resolves references under exactly the rules diagnostics
// enforce.
func EventNamesFromTrigger(value string) []string {
	var out []string
	for _, spec := range strings.Split(stripBrackets(value), ",") {
		fields := strings.Fields(spec)
		if len(fields) == 0 {
			continue
		}
		ev := fields[0]
		if ev == "every" {
			continue // Polling form: no event name.
		}
		if strings.ContainsAny(ev, ":.") || !strings.Contains(ev, "-") {
			continue // Namespaced, or a DOM event.
		}
		out = append(out, ev)
	}
	return out
}

// stripBrackets removes [...] filter spans so their contents (which may
// hold commas and arbitrary JS) never look like event names.
func stripBrackets(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '[':
			depth++
		case r == ']' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Events returns the declared events sorted by wire name, for event code
// generation. On duplicates the first declaration in sorted file order
// wins, mirroring the diagnostic.
func (s *SetAnalysis) Events() []EventInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	var fileNames []string
	for name := range s.files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	seen := map[string]bool{}
	var out []EventInfo
	for _, fn := range fileNames {
		for _, e := range s.files[fn].events {
			if seen[e.WireName] {
				continue
			}
			seen[e.WireName] = true
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].WireName < out[j].WireName })
	return out
}

// checkEvents emits the whole-set event diagnostics.
func (s *SetAnalysis) checkEvents(sink *diag.Sink) {
	s.mu.Lock()
	var fileNames []string
	for name := range s.files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	var decls []EventInfo
	var refs []eventRef
	for _, fn := range fileNames {
		decls = append(decls, s.files[fn].events...)
		refs = append(refs, s.files[fn].eventRefs...)
	}
	s.mu.Unlock()

	// GHTMX-E0305: wire names are global — two declarations that collide
	// on the wire would be indistinguishable in HX-Trigger and in
	// listeners. The diagnostic names both declarations.
	registry := map[string]EventInfo{}
	for _, d := range decls {
		first, dup := registry[d.WireName]
		if !dup {
			registry[d.WireName] = d
			continue
		}
		detail := fmt.Sprintf("duplicate event %q: first declared at %s", d.Name, first.Pos)
		if first.Name != d.Name {
			detail = fmt.Sprintf("event %q collides with event %q (declared at %s): both have wire name %q", d.Name, first.Name, first.Pos, d.WireName)
		}
		sink.Add(diag.DuplicateEvent, d.Pos, detail,
			"event names are global to the compiled set; rename one of the declarations")
	}

	// GHTMX-E0304: references to the compiler-owned namespace must
	// resolve to a declaration.
	referenced := map[string]bool{}
	for _, r := range refs {
		if _, ok := registry[r.wire]; ok {
			referenced[r.wire] = true
			continue
		}
		sink.Add(diag.UndeclaredEvent, r.pos,
			fmt.Sprintf("event %q in %s is not declared by any event declaration", r.wire, r.attr),
			"declare it with `event Name(...)`, or use a ':'-qualified name for events outside the ghtmx contract")
	}

	// GHTMX-W0102: declared events nobody listens for. Emission happens
	// in Go handlers, which syntax-only analysis does not inspect, so a
	// template-side reference is the only visible use.
	var wires []string
	for w := range registry {
		wires = append(wires, w)
	}
	sort.Strings(wires)
	for _, w := range wires {
		if referenced[w] {
			continue
		}
		d := registry[w]
		sink.Add(diag.UnemittedEvent, d.Pos,
			fmt.Sprintf("event %q (wire name %q) is never referenced by any template", d.Name, d.WireName),
			"listen for it with hx-on:"+d.WireName+" or hx-trigger, or silence GHTMX-W0102 if only handler-side emission is intended")
	}
}

// DependencyFacts is the per-file summary the dependency graph consumes
// (FR-061). Names are package-path-qualified ("pkg.Name").
type DependencyFacts struct {
	// Decls are the templates and fragments the file declares.
	Decls []string
	// Refs are the file's resolved template and fragment references.
	Refs []string
	// BindsRoutes reports whether generated output embeds route state.
	BindsRoutes bool
	// EventDecls and EventRefs are wire names declared and referenced.
	EventDecls []string
	EventRefs  []string
}

// FileDependencyFacts snapshots every collected file's dependency facts.
func (s *SetAnalysis) FileDependencyFacts() map[string]DependencyFacts {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]DependencyFacts, len(s.files))
	collect := func(file string) DependencyFacts {
		var f DependencyFacts
		if facts, ok := s.files[file]; ok {
			f.BindsRoutes = facts.bindsRoutes
			for _, e := range facts.events {
				f.EventDecls = append(f.EventDecls, e.WireName)
			}
			for _, r := range facts.eventRefs {
				f.EventRefs = append(f.EventRefs, r.wire)
			}
		}
		if frags, ok := s.fragments[file]; ok {
			for _, n := range frags.nodes {
				// Method templates are unaddressable (no Decls entry) but
				// their references still create dependency edges.
				if n.name != "" {
					f.Decls = append(f.Decls, n.pkgPath+"."+n.name)
				}
				for _, e := range n.edges {
					f.Refs = append(f.Refs, e.targetPkg+"."+e.name)
				}
			}
		}
		sort.Strings(f.Decls)
		sort.Strings(f.Refs)
		return f
	}
	for file := range s.files {
		out[file] = collect(file)
	}
	for file := range s.fragments {
		if _, done := out[file]; !done {
			out[file] = collect(file)
		}
	}
	return out
}
