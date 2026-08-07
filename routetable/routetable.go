// Package routetable exposes the route table ghtmx derives from Go
// source, so a consuming project can assert in its own tests that the
// routes the toolchain believes in are the routes its framework really
// serves (FR-064).
//
// `ghtmx generate -check` verifies template-versus-generated-code drift.
// It does not — and cannot — verify annotation-versus-reality drift: once
// a project takes the //ghtmx:route escape hatch and registers routes
// outside routeScope, nothing checks that the annotated paths still match
// what the framework serves. Load reads the same table `ghtmx routes`
// prints, and Diff compares it against a list the consumer dumps from
// their own router, which turns that invariant back into a test.
//
// This package is for build- and test-time tooling. Application code
// renders through the root package and an adapter and never needs it.
package routetable

import (
	"fmt"
	"sort"
	"strings"

	"github.com/go-monolith/ghtmx/internal/config"
	"github.com/go-monolith/ghtmx/internal/diag"
	"github.com/go-monolith/ghtmx/internal/routes"
)

// AnyVerb is the verb of a route registered without one — a net/http
// pattern carrying no method, which serves every verb. It is rendered
// as "*", matching `ghtmx routes -json`.
const AnyVerb = "*"

// Route is one entry of the route table. The JSON tags are the schema
// `ghtmx routes -json` emits, so its output unmarshals into []Route
// directly.
type Route struct {
	// Verb is the HTTP method, or "*" for a route serving every verb.
	Verb string `json:"verb"`
	// Path is the normalized pattern, using {name} and {name...}
	// regardless of the source router's flavour.
	Path string `json:"path"`
	// OriginalPath preserves the registering router's own syntax.
	OriginalPath string `json:"originalPath"`
	// Params are the ordered path parameter names, wildcards suffixed
	// with "...".
	Params []string `json:"params,omitempty"`
	// HandlerPkg is the import path of the handler's package.
	HandlerPkg string `json:"handlerPackage"`
	// HandlerName is the handler symbol's name.
	HandlerName string `json:"handlerName"`
	// Origin is "discovered" or "declared" (by a //ghtmx:route
	// annotation).
	Origin string `json:"origin"`
	// Recognizer names the router flavour that produced the route:
	// nethttp, chi, echo, gin, fiber, or annotation.
	Recognizer string `json:"recognizer"`
	// Source is the registration or declaration site, "file:line:col".
	Source string `json:"source"`
	// NavOnly reports the route as navigation-only (the annotation's
	// trailing "nav" marker), exempting it from GHTMX-W0104.
	NavOnly bool `json:"navOnly,omitempty"`
}

// Handler returns the fully qualified handler symbol, matching the text
// output of `ghtmx routes`.
func (r Route) Handler() string {
	if r.HandlerPkg == "" {
		return r.HandlerName
	}
	return r.HandlerPkg + "." + r.HandlerName
}

// Key is the identity Diff compares on: verb and normalized path.
func (r Route) Key() string { return r.Verb + " " + r.Path }

// Load runs route discovery over the module rooted at dir — the same
// analysis `ghtmx routes` performs, honouring that project's ghtmx.json
// (routeScope in particular) — and returns the table in the order the
// command prints it: by path, then verb.
//
// Discovery diagnostics are not returned: a project whose table this
// reports on has already seen them from `ghtmx generate`. An error means
// the packages could not be loaded at all.
func Load(dir string) ([]Route, error) {
	cfg, err := config.Load(dir)
	if err != nil {
		return nil, err
	}
	sink := diag.NewSink(cfg.SeverityOverrides())
	pkgs, err := routes.Load(dir, cfg.RouteScope, sink)
	if err != nil {
		return nil, err
	}
	return FromTable(routes.Discover(pkgs, sink)), nil
}

// FromTable converts an internal route table. It is the single place the
// public shape is built, so `ghtmx routes -json` and this package cannot
// describe the same route differently.
func FromTable(table *routes.Table) []Route {
	if table == nil {
		return []Route{}
	}
	all := table.All()
	out := make([]Route, 0, len(all))
	for _, r := range all {
		verb := string(r.Verb)
		if r.Verb == routes.AnyVerb {
			verb = AnyVerb
		}
		route := Route{
			Verb:         verb,
			Path:         r.Path,
			OriginalPath: r.OriginalPath,
			HandlerPkg:   r.Handler.PkgPath,
			HandlerName:  r.Handler.Name,
			Origin:       string(r.Origin),
			Recognizer:   r.Recognizer,
			Source:       r.Pos.String(),
			NavOnly:      r.NavOnly,
		}
		for _, p := range r.Params {
			name := p.Name
			if p.Wildcard {
				name += "..."
			}
			route.Params = append(route.Params, name)
		}
		out = append(out, route)
	}
	return out
}

// ParamStyle is a router flavour's path-parameter syntax.
type ParamStyle int

const (
	// BraceStyle is net/http and chi: /users/{id}, /files/{path...}.
	BraceStyle ParamStyle = ParamStyle(routes.BraceStyle)
	// ColonStyle is echo, gin, and fiber: /users/:id, /files/*.
	ColonStyle ParamStyle = ParamStyle(routes.ColonStyle)
)

// NormalizePath converts a router-flavoured pattern to the normalized
// form the table uses, returning the path and its ordered parameter
// names (wildcards suffixed with "..."). It is the toolchain's own
// normalizer, so a consumer comparing framework paths against Load's
// output cannot drift from it.
func NormalizePath(path string, style ParamStyle) (string, []string) {
	normalized, params := routes.NormalizePath(path, routes.ParamStyle(style))
	names := make([]string, 0, len(params))
	for _, p := range params {
		name := p.Name
		if p.Wildcard {
			name += "..."
		}
		names = append(names, name)
	}
	return normalized, names
}

// Normalize returns a copy of rs with every Path normalized from style,
// keeping the input pattern in OriginalPath. Use it on a list dumped
// from a framework router — whose paths are in that router's own syntax
// — before handing it to Diff.
func Normalize(rs []Route, style ParamStyle) []Route {
	out := make([]Route, len(rs))
	for i, r := range rs {
		if r.OriginalPath == "" {
			r.OriginalPath = r.Path
		}
		r.Path, r.Params = NormalizePath(r.Path, style)
		out[i] = r
	}
	return out
}

// MismatchKind classifies one disagreement between two route lists.
type MismatchKind string

const (
	// KindMissing: declared holds a route actual does not — the
	// toolchain believes in a route the router never serves, so its
	// template bindings generate URLs that 404.
	KindMissing MismatchKind = "missing"
	// KindUnexpected: actual holds a route declared does not — the
	// router serves a route the toolchain cannot see, so no template can
	// bind it.
	KindUnexpected MismatchKind = "unexpected"
	// KindHandler: both hold the verb and path, but disagree on the
	// handler symbol.
	KindHandler MismatchKind = "handler"
)

// Mismatch is one disagreement, carrying whichever side(s) exist.
type Mismatch struct {
	Kind MismatchKind
	Verb string
	Path string
	// Declared and Actual are the routes from each side; the one absent
	// for the kind is nil.
	Declared *Route
	Actual   *Route
}

// String renders a mismatch as a single reviewable line.
func (m Mismatch) String() string {
	switch m.Kind {
	case KindMissing:
		return fmt.Sprintf("%s %s is declared at %s (%s) but the router does not serve it",
			m.Verb, m.Path, m.Declared.Source, m.Declared.Handler())
	case KindUnexpected:
		return fmt.Sprintf("%s %s is served by the router but no ghtmx route declares it", m.Verb, m.Path)
	case KindHandler:
		return fmt.Sprintf("%s %s is declared for %s at %s but the router serves %s",
			m.Verb, m.Path, m.Declared.Handler(), m.Declared.Source, m.Actual.Handler())
	}
	return fmt.Sprintf("%s %s: %s", m.Verb, m.Path, m.Kind)
}

// Diff compares the route table ghtmx derived (declared — from Load, or
// from `ghtmx routes -json`) against the routes a framework really
// serves (actual — dumped by the consumer from its own router, then
// passed through Normalize). Routes are matched on verb and normalized
// path.
//
// The result is sorted by path then verb, so a failing test prints the
// same report every run. An empty result means the two agree.
//
// A handler comparison is only made when the actual side names one:
// most routers report paths and not Go symbols, so an empty HandlerName
// on the actual side means "not reported", never "different".
func Diff(declared, actual []Route) []Mismatch {
	declaredBy := index(declared)
	actualBy := index(actual)

	var out []Mismatch
	for key, d := range declaredBy {
		a, ok := actualBy[key]
		if !ok {
			out = append(out, Mismatch{Kind: KindMissing, Verb: d.Verb, Path: d.Path, Declared: routePtr(d)})
			continue
		}
		if a.HandlerName != "" && !sameHandler(d, a) {
			out = append(out, Mismatch{Kind: KindHandler, Verb: d.Verb, Path: d.Path, Declared: routePtr(d), Actual: routePtr(a)})
		}
	}
	for key, a := range actualBy {
		if _, ok := declaredBy[key]; !ok {
			out = append(out, Mismatch{Kind: KindUnexpected, Verb: a.Verb, Path: a.Path, Actual: routePtr(a)})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Verb < out[j].Verb
	})
	return out
}

// sameHandler compares handler symbols, tolerating an actual side that
// reports only the bare name (a router that knows the function but not
// its package).
func sameHandler(declared, actual Route) bool {
	if actual.HandlerPkg == "" {
		return actual.HandlerName == declared.HandlerName
	}
	return actual.Handler() == declared.Handler()
}

// index keys routes by verb and path, keeping the first of any
// duplicates so the report names a stable side.
func index(rs []Route) map[string]Route {
	out := make(map[string]Route, len(rs))
	for _, r := range rs {
		if _, seen := out[r.Key()]; seen {
			continue
		}
		out[r.Key()] = r
	}
	return out
}

func routePtr(r Route) *Route { return &r }

// Report renders mismatches as a newline-terminated block suitable for a
// test failure message. It returns "" when there are none.
func Report(ms []Mismatch) string {
	if len(ms) == 0 {
		return ""
	}
	var sb strings.Builder
	for _, m := range ms {
		sb.WriteString(m.String())
		sb.WriteString("\n")
	}
	return sb.String()
}
