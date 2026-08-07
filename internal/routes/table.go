// Package routes builds the route table from application Go source by
// syntax-only static analysis (constitution A3, A3.1). It never requests
// type information: a type error, missing generated file, or stale
// generated file anywhere in the module must not prevent the route table
// from being built. Handler identity is resolved by package-qualified
// identifier matching against each file's import declarations.
package routes

import (
	"fmt"
	"sort"
	"strings"
)

// Verb is an HTTP method. The empty Verb ("") records a net/http pattern
// registered without a method, which matches every method.
type Verb string

const (
	GET    Verb = "GET"
	POST   Verb = "POST"
	PUT    Verb = "PUT"
	PATCH  Verb = "PATCH"
	DELETE Verb = "DELETE"
	// AnyVerb matches all methods (a ServeMux pattern with no method).
	AnyVerb Verb = ""
)

// BindableVerbs are the verbs addressable from hx-* attributes (FR-020).
var BindableVerbs = []Verb{GET, POST, PUT, PATCH, DELETE}

// SymbolRef identifies a Go function by package import path and name,
// resolved syntactically (constitution A3.1): no type information exists or
// is assumed.
type SymbolRef struct {
	PkgPath string
	Name    string
}

func (s SymbolRef) String() string {
	if s.PkgPath == "" {
		return s.Name
	}
	return s.PkgPath + "." + s.Name
}

// RouteParam is one ordered path parameter.
type RouteParam struct {
	// Name of the parameter in the normalized pattern.
	Name string
	// Wildcard marks a rest-of-path parameter ({name...}).
	Wildcard bool
}

// Origin distinguishes discovered routes from escape-hatch declarations
// (FR-015).
type Origin string

const (
	Discovered Origin = "discovered"
	Declared   Origin = "declared"
)

// Position is a source location of a registration site.
type Position struct {
	File string
	Line uint32
	Col  uint32
}

func (p Position) String() string {
	return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Col)
}

// Route is one entry in the route table (DATA-003).
type Route struct {
	Verb Verb
	// Path is the normalized pattern using {name} and {name...} parameter
	// syntax regardless of the source router's flavour.
	Path string
	// OriginalPath preserves the router flavour's own syntax for the
	// routes command output.
	OriginalPath string
	// Params are the ordered path parameters.
	Params []RouteParam
	// Handler is the registered handler symbol.
	Handler SymbolRef
	// Pos is the registration or declaration site.
	Pos Position
	// Origin records whether the route was discovered or declared.
	Origin Origin
	// Recognizer names the router flavour that produced the route
	// (nethttp, chi, echo, gin, fiber, or annotation).
	Recognizer string
	// NavOnly marks a route reached by navigation (<a href>, native form
	// posts) rather than htmx, declared with the trailing "nav" marker on
	// its annotation. It exempts the route from GHTMX-W0104 without
	// silencing the check project-wide; error-class checks still apply.
	// Generated output never depends on it, but it participates in the
	// build fingerprint so watch mode sees a toggle immediately.
	NavOnly bool
}

type routeKey struct {
	verb Verb
	path string
}

// Table is the route table. It is constructible without type information
// (FR-010) and serializable for the routes command (FR-064).
type Table struct {
	routes    []Route
	byKey     map[routeKey]int
	byHandler map[SymbolRef][]int
}

// NewTable returns an empty route table.
func NewTable() *Table {
	return &Table{
		byKey:     map[routeKey]int{},
		byHandler: map[SymbolRef][]int{},
	}
}

// Add inserts a route. If a route with the same verb and path already
// exists, the existing route is returned with ok=false and the table is
// unchanged: the caller reports the conflict (FR-050) with both sites.
func (t *Table) Add(r Route) (existing Route, ok bool) {
	key := routeKey{r.Verb, r.Path}
	if i, dup := t.byKey[key]; dup {
		return t.routes[i], false
	}
	t.routes = append(t.routes, r)
	i := len(t.routes) - 1
	t.byKey[key] = i
	t.byHandler[r.Handler] = append(t.byHandler[r.Handler], i)
	return Route{}, true
}

// Lookup finds the route registered for the verb and handler symbol.
// A route registered with AnyVerb (a ServeMux pattern with no method)
// matches every bindable verb, but an exact-verb registration always wins
// over an AnyVerb one, matching ServeMux's most-specific-wins semantics.
func (t *Table) Lookup(verb Verb, sym SymbolRef) (Route, bool) {
	for _, i := range t.byHandler[sym] {
		if r := t.routes[i]; r.Verb == verb {
			return r, true
		}
	}
	for _, i := range t.byHandler[sym] {
		if r := t.routes[i]; r.Verb == AnyVerb {
			return r, true
		}
	}
	return Route{}, false
}

// ByHandler returns every route registered for the handler symbol.
func (t *Table) ByHandler(sym SymbolRef) []Route {
	out := make([]Route, 0, len(t.byHandler[sym]))
	for _, i := range t.byHandler[sym] {
		out = append(out, t.routes[i])
	}
	return out
}

// All returns every route sorted by path, then verb: deterministic output
// for code generation (NFR-004) and the routes command.
func (t *Table) All() []Route {
	out := make([]Route, len(t.routes))
	copy(out, t.routes)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Verb < out[j].Verb
	})
	return out
}

// ParamStyle is a router flavour's path parameter syntax.
type ParamStyle int

const (
	// BraceStyle is {id} / {rest...} (net/http, chi).
	BraceStyle ParamStyle = iota
	// ColonStyle is :id plus *wildcard forms (echo, gin, fiber).
	ColonStyle
)

// NormalizePath converts a router-flavour path into the normalized
// {name} / {name...} representation and extracts its ordered parameters.
func NormalizePath(path string, style ParamStyle) (normalized string, params []RouteParam) {
	segments := strings.Split(path, "/")
	for i, seg := range segments {
		switch style {
		case BraceStyle:
			if strings.Contains(seg, "{") {
				var norm string
				norm, params = normalizeBraceSegment(seg, params)
				segments[i] = norm
			}
		case ColonStyle:
			switch {
			case strings.HasPrefix(seg, ":"):
				name := strings.TrimSuffix(seg[1:], "?")
				params = append(params, RouteParam{Name: name})
				segments[i] = "{" + name + "}"
			case seg == "*" || seg == "+":
				params = append(params, RouteParam{Name: "rest", Wildcard: true})
				segments[i] = "{rest...}"
			case strings.HasPrefix(seg, "*"):
				name := seg[1:]
				params = append(params, RouteParam{Name: name, Wildcard: true})
				segments[i] = "{" + name + "...}"
			}
		}
	}
	return strings.Join(segments, "/"), params
}

// normalizeBraceSegment handles one path segment that may contain several
// {name} groups (chi allows /articles/{date}-{slug}), normalizing each
// group and stripping regex constraints ({id:[0-9]+} -> {id}).
func normalizeBraceSegment(seg string, params []RouteParam) (string, []RouteParam) {
	var sb strings.Builder
	for {
		open := strings.IndexByte(seg, '{')
		if open < 0 {
			sb.WriteString(seg)
			return sb.String(), params
		}
		closing := strings.IndexByte(seg[open:], '}')
		if closing < 0 {
			// Unbalanced brace: leave the remainder untouched.
			sb.WriteString(seg)
			return sb.String(), params
		}
		closing += open
		sb.WriteString(seg[:open])
		inner := seg[open+1 : closing]
		if inner == "$" {
			// net/http's end-of-path anchor ("GET /{$}" matches only
			// the exact path): not a parameter — strip it from the
			// normalized path so the route emits a constant, never a
			// constructor taking a "$" argument.
			seg = seg[closing+1:]
			continue
		}
		if name, ok := strings.CutSuffix(inner, "..."); ok {
			params = append(params, RouteParam{Name: name, Wildcard: true})
			sb.WriteString("{")
			sb.WriteString(name)
			sb.WriteString("...}")
		} else {
			name, _, _ := strings.Cut(inner, ":")
			params = append(params, RouteParam{Name: name})
			sb.WriteString("{")
			sb.WriteString(name)
			sb.WriteString("}")
		}
		seg = seg[closing+1:]
	}
}

// JoinPaths composes a group prefix with a registered path. The result
// always has a leading slash.
func JoinPaths(prefix, path string) string {
	if prefix == "" {
		if path != "" && !strings.HasPrefix(path, "/") {
			return "/" + path
		}
		return path
	}
	p := strings.TrimSuffix(prefix, "/")
	if path == "" || path == "/" {
		if p == "" {
			return "/"
		}
		return p
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return p + path
}
