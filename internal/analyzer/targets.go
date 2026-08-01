package analyzer

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/go-monolith/ghtmx/internal/diag"
	parser "github.com/go-monolith/ghtmx/internal/parser"
	"github.com/go-monolith/ghtmx/internal/routes"
)

// SetAnalysis accumulates whole-compiled-set facts that no single file can
// decide alone: literal emitted IDs, literal ID-selector targets (FR-042),
// and which routes templates actually bind (FR-043). It is safe for
// concurrent per-file collection; re-collecting a file replaces its
// contribution, so watch-mode re-analysis stays correct.
type SetAnalysis struct {
	mu        sync.Mutex
	files     map[string]*fileFacts
	bound     map[string]bool // verb+" "+path
	fragments map[string]*fileFragments
}

type fileFacts struct {
	emittedIDs  map[string]bool
	targets     []targetRef
	events      []EventInfo
	eventRefs   []eventRef
	bindsRoutes bool
}

type targetRef struct {
	attr     string
	selector string
	pos      diag.Position
}

// NewSetAnalysis returns an empty whole-set collector.
func NewSetAnalysis() *SetAnalysis {
	return &SetAnalysis{files: map[string]*fileFacts{}, bound: map[string]bool{}}
}

// literalIDSelector matches a fully-literal CSS ID selector: exactly one
// #id with no combinators, classes, or extended-selector prefixes.
// Anything else is exempt from analysis entirely (FR-042).
var literalIDSelector = regexp.MustCompile(`^#[A-Za-z][A-Za-z0-9_.:-]*$`)

// CollectFile records the file's literal emitted IDs and literal ID-only
// target selectors. Selectors and IDs containing any interpolated
// expression are never collected: the check is deliberately conservative.
func (s *SetAnalysis) CollectFile(file *parser.TemplateFile) {
	facts := computeFileFacts(file)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[file.Filepath] = facts
}

// Collect records the file's facts and fragment data in one step, so a
// concurrent snapshot never sees one map updated and the other not.
func (s *SetAnalysis) Collect(file *parser.TemplateFile, pkgPath string) {
	facts := computeFileFacts(file)
	frags := computeFragmentFacts(file, pkgPath)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[file.Filepath] = facts
	if s.fragments == nil {
		s.fragments = map[string]*fileFragments{}
	}
	s.fragments[file.Filepath] = frags
}

// RemoveFile drops a deleted file's contribution to the whole-set state,
// so the dependency graph and checks stop seeing it (FR-061).
func (s *SetAnalysis) RemoveFile(file string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.files, file)
	delete(s.fragments, file)
}

func computeFileFacts(file *parser.TemplateFile) *fileFacts {
	facts := &fileFacts{emittedIDs: map[string]bool{}}
	var children [][]parser.Node
	for _, node := range file.Nodes {
		switch t := node.(type) {
		case *parser.HTMLTemplate:
			children = append(children, t.Children)
		case *parser.FragmentDeclaration:
			children = append(children, t.Children)
		case *parser.EventDeclaration:
			collectEventDecl(t, file.Filepath, facts)
		}
	}
	for _, nodes := range children {
		walkNodes(nodes, func(n parser.Node) {
			var attrs []parser.Attribute
			switch e := n.(type) {
			case *parser.Element:
				attrs = e.Attributes
			case *parser.ScriptElement:
				attrs = e.Attributes
			case *parser.RawElement:
				attrs = e.Attributes
			default:
				return
			}
			collectAttrFacts(attrs, file.Filepath, facts)
		})
	}
	return facts
}

func collectAttrFacts(attrs []parser.Attribute, filePath string, facts *fileFacts) {
	for _, a := range attrs {
		switch attr := a.(type) {
		case *parser.ConstantAttribute:
			name, rng, ok := constantKey(attr.Key)
			if !ok {
				continue
			}
			refPos := func() diag.Position {
				return diag.Position{
					File:  filePath,
					Line:  rng.From.Line + 1,
					Col:   rng.From.Col + 1,
					Index: rng.From.Index,
				}
			}
			if wire, isEvent := EventNameFromAttr(name); isEvent {
				facts.eventRefs = append(facts.eventRefs, eventRef{wire: wire, attr: name, pos: refPos()})
			}
			if name == "hx-trigger" {
				for _, wire := range EventNamesFromTrigger(attr.Value) {
					facts.eventRefs = append(facts.eventRefs, eventRef{wire: wire, attr: name, pos: refPos()})
				}
			}
			switch name {
			case "id":
				facts.emittedIDs[attr.Value] = true
			case "hx-target", "hx-select":
				sel := strings.TrimSpace(attr.Value)
				if literalIDSelector.MatchString(sel) {
					facts.targets = append(facts.targets, targetRef{
						attr:     name,
						selector: sel,
						pos: diag.Position{
							File:  filePath,
							Line:  rng.From.Line + 1,
							Col:   rng.From.Col + 1,
							Index: rng.From.Index,
						},
					})
				}
			}
		case *parser.ExpressionAttribute:
			// The value is dynamic but the key is static: an hx-on key
			// still names the event it listens for.
			if name, rng, ok := constantKey(attr.Key); ok {
				if wire, isEvent := EventNameFromAttr(name); isEvent {
					facts.eventRefs = append(facts.eventRefs, eventRef{wire: wire, attr: name, pos: diag.Position{
						File:  filePath,
						Line:  rng.From.Line + 1,
						Col:   rng.From.Col + 1,
						Index: rng.From.Index,
					}})
				}
			}
		case *parser.ConditionalAttribute:
			// IDs in conditional branches count as emitted (conservative);
			// targets in branches are still statically-literal and checked.
			collectAttrFacts(attr.Then, filePath, facts)
			collectAttrFacts(attr.Else, filePath, facts)
		}
	}
}

// MarkBound records that a route was bound from a template.
func (s *SetAnalysis) MarkBound(r routes.Route) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bound[string(r.Verb)+" "+r.Path] = true
}

// MarkBindingFile records that the file holds at least one route binding:
// its generated output embeds route state, so a route change invalidates
// it (FR-061). The file must already be collected.
func (s *SetAnalysis) MarkBindingFile(file string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.files[file]; ok {
		f.bindsRoutes = true
	}
}

// Check emits the whole-set diagnostics: GHTMX-W0201 for a fully-literal
// ID-selector target matching no literal ID emitted anywhere in the
// compiled set (warning by default, error under strict mode via the sink's
// severity overrides), and GHTMX-W0104 for a route never bound from any
// template.
func (s *SetAnalysis) Check(table *routes.Table, sink *diag.Sink) {
	s.mu.Lock()
	allIDs := map[string]bool{}
	var targets []targetRef
	var fileNames []string
	for name := range s.files {
		fileNames = append(fileNames, name)
	}
	sort.Strings(fileNames)
	for _, name := range fileNames {
		f := s.files[name]
		for id := range f.emittedIDs {
			allIDs[id] = true
		}
		targets = append(targets, f.targets...)
	}
	bound := make(map[string]bool, len(s.bound))
	for k := range s.bound {
		bound[k] = true
	}
	s.mu.Unlock()

	// Each check snapshots under s.mu separately; a concurrent re-collect
	// in watch mode could interleave, but every watch pass re-runs the
	// whole Check, so the final report is always single-generation.
	s.checkFragmentRefs(sink)
	s.checkGraph(sink)
	s.checkEvents(sink)

	for _, t := range targets {
		if allIDs[strings.TrimPrefix(t.selector, "#")] {
			continue
		}
		sink.Add(diag.DanglingTarget, t.pos,
			fmt.Sprintf("%s %q matches no literal id in the compiled template set (the check covers statically-analyzable literal IDs only)", t.attr, t.selector),
			"add the target element, fix the selector, or compute the target dynamically to exempt it")
	}

	if table == nil {
		return
	}
	for _, r := range table.All() {
		if bound[string(r.Verb)+" "+r.Path] {
			continue
		}
		verb := string(r.Verb)
		if r.Verb == routes.AnyVerb {
			verb = "ANY"
		}
		sink.Add(diag.UnboundRoute,
			diag.Position{File: r.Pos.File, Line: max(r.Pos.Line, 1), Col: max(r.Pos.Col, 1)},
			fmt.Sprintf("route %s %s (%s) is never bound from any template", verb, r.Path, r.Handler),
			"bind it with an hx-* attribute, or silence this check with GHTMX-W0104=off")
	}
}
