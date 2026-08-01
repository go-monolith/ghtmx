// Package central emits the central generated package (solution design
// D5): path constants for non-parameterised routes and typed constructor
// functions for parameterised routes (FR-021). Routes are global by
// nature, so one package holds every generated route symbol; a renamed or
// re-pathed route regenerates the package and breaks every call site at
// compile time (D9).
package central

import (
	"fmt"
	goast "go/ast"
	"go/format"
	goparser "go/parser"
	"go/token"
	gotypes "go/types"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/go-monolith/ghtmx/internal/routes"
)

// Options configures emission.
type Options struct {
	// PackageName is the generated package's name (ghtmxgen by default).
	PackageName string
	// Version is included as a comment when non-empty.
	Version string
	// ModRoot, when set, relativizes registration-site paths in doc
	// comments so output is byte-identical across machines (NFR-004).
	ModRoot string
	// Events are the declared events to emit emission symbols for
	// (FR-037), in registry order.
	Events []Event
	// HtmxVersion, when set, emits HTMXScript() baking the configured
	// version into the script tag (FR-091), so tag and configuration
	// cannot diverge.
	HtmxVersion string
}

// Event is one declared event to generate emission symbols for.
type Event struct {
	// Name is the declared Go-side name, e.g. "UserCreated".
	Name string
	// WireName is the HX-Trigger name, e.g. "user-created".
	WireName string
	// Params is the declared payload parameter list source, e.g.
	// "(id string, name string)"; "()" for payload-less events.
	Params string
	// DeclaredAt is the declaration site for doc comments, already
	// module-relative.
	DeclaredAt string
}

// eventSymbols lists the Go symbols an event generates: the three timing
// emitters, plus the payload type when the event declares parameters.
func eventSymbols(e Event) []string {
	symbols := []string{"Emit" + e.Name, "Emit" + e.Name + "AfterSettle", "Emit" + e.Name + "AfterSwap"}
	if fields, err := eventFields(e.Params); err == nil && len(fields) > 0 {
		symbols = append(symbols, e.Name+"Payload")
	}
	return symbols
}

// EventCollisions reports events whose generated symbols collide with a
// route symbol or with an earlier event's symbols. Colliding events are
// excluded from emission by Generate; each message is a GHTMX-E0404.
func EventCollisions(byName map[string]Constructor, events []Event) []string {
	symbols := map[string]string{}
	for name, c := range byName {
		symbol := name
		if len(c.Route.Params) == 0 {
			symbol = name + "Path"
		}
		symbols[symbol] = fmt.Sprintf("route %s %s (%s)", c.Route.Verb, c.Route.Path, c.Route.Handler)
	}
	var out []string
	for _, e := range events {
		clashed := false
		for _, symbol := range eventSymbols(e) {
			if site, clash := symbols[symbol]; clash {
				out = append(out, fmt.Sprintf("event %s (declared at %s) generates symbol %s, which collides with %s", e.Name, e.DeclaredAt, symbol, site))
				clashed = true
			}
		}
		if clashed {
			continue
		}
		for _, symbol := range eventSymbols(e) {
			symbols[symbol] = fmt.Sprintf("event %s (declared at %s)", e.Name, e.DeclaredAt)
		}
	}
	return out
}

// Constructor describes one generated route symbol.
type Constructor struct {
	// Name of the generated constructor function (or, for routes without
	// parameters, the base name of the <Name>Path constant).
	Name  string
	Route routes.Route
}

// Naming assigns a deterministic, unique Go identifier to every route.
// The base name is the handler symbol's name; cross-package collisions are
// disambiguated with the handler's package base name, multi-verb
// collisions with the verb. A name that still collides is returned in
// conflicts and excluded from emission (GHTMX-E0404).
func Naming(table *routes.Table) (byName map[string]Constructor, conflicts [][]Constructor) {
	all := table.All()

	// Pass 1: base handler names, exported: generated symbols must be
	// callable from any package.
	base := make([]string, len(all))
	counts := map[string]int{}
	for i, r := range all {
		base[i] = exportIdent(sanitizeIdent(r.Handler.Name))
		counts[base[i]]++
	}
	// Pass 2: package-prefix cross-package collisions. Decide against the
	// unmutated name set first, then apply, so prefixing one route does not
	// hide the collision from its siblings.
	needsPrefix := map[string]bool{}
	for name, count := range counts {
		if count > 1 && !samePackage(all, base, name) {
			needsPrefix[name] = true
		}
	}
	for i, r := range all {
		if needsPrefix[base[i]] {
			base[i] = sanitizeIdent(exportedPkgBase(r.Handler.PkgPath)) + base[i]
		}
	}
	// Pass 3: verb-suffix remaining collisions.
	counts = map[string]int{}
	for i := range all {
		counts[base[i]]++
	}
	for i, r := range all {
		if counts[base[i]] > 1 {
			base[i] += verbSuffix(r.Verb)
		}
	}

	byName = map[string]Constructor{}
	grouped := map[string][]Constructor{}
	for i, r := range all {
		grouped[base[i]] = append(grouped[base[i]], Constructor{Name: base[i], Route: r})
	}
	var names []string
	for name := range grouped {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		g := grouped[name]
		if len(g) > 1 {
			conflicts = append(conflicts, g)
			continue
		}
		byName[name] = g[0]
	}
	return byName, conflicts
}

func samePackage(all []routes.Route, base []string, name string) bool {
	pkg := ""
	for i, r := range all {
		if base[i] != name {
			continue
		}
		if pkg == "" {
			pkg = r.Handler.PkgPath
			continue
		}
		if r.Handler.PkgPath != pkg {
			return false
		}
	}
	return true
}

func verbSuffix(v routes.Verb) string {
	if v == routes.AnyVerb {
		return "Any"
	}
	s := strings.ToLower(string(v))
	return strings.ToUpper(s[:1]) + s[1:]
}

// exportedPkgBase derives an exported identifier prefix from an import
// path's base element (my-handlers -> MyHandlers).
func exportedPkgBase(pkgPath string) string {
	parts := strings.Split(pkgPath, "/")
	baseName := parts[len(parts)-1]
	var sb strings.Builder
	upper := true
	for _, r := range baseName {
		switch {
		case r == '-' || r == '_' || r == '.':
			upper = true
		case upper:
			sb.WriteString(strings.ToUpper(string(r)))
			upper = false
		default:
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// exportIdent capitalizes the first rune so the symbol is exported.
func exportIdent(s string) string {
	if s == "" {
		return s
	}
	first, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(first)) + s[size:]
}

func sanitizeIdent(s string) string {
	var sb strings.Builder
	for _, r := range s {
		if r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			sb.WriteRune(r)
		}
	}
	out := sb.String()
	if out == "" || !token.IsIdentifier(out) {
		return "Route"
	}
	return out
}

// Generate emits the routes file of the central generated package. The
// output is deterministic, gofmt-formatted Go (NFR-004, NFR-005).
func Generate(table *routes.Table, opts Options) ([]byte, error) {
	byName, _ := Naming(table)

	var names []string
	for name := range byName {
		names = append(names, name)
	}
	sort.Strings(names)

	var sb strings.Builder
	sb.WriteString("// Code generated by ghtmx - DO NOT EDIT.\n\n")
	if opts.Version != "" {
		fmt.Fprintf(&sb, "// ghtmx: version: %s\n\n", opts.Version)
	}
	fmt.Fprintf(&sb, "// Package %s is the central generated package: route path\n", opts.PackageName)
	sb.WriteString("// constants and typed route constructors, regenerated by ghtmx generate\n")
	sb.WriteString("// from the application's own route registrations.\n")
	fmt.Fprintf(&sb, "package %s\n\n", opts.PackageName)

	// Events whose symbols collide with a route symbol or an earlier
	// event are excluded from emission (GHTMX-E0404, reported by the
	// caller via EventCollisions — the exclusion logic mirrors it).
	symbolTaken := map[string]bool{}
	for name, c := range byName {
		if len(c.Route.Params) == 0 {
			symbolTaken[name+"Path"] = true
			continue
		}
		symbolTaken[name] = true
	}
	var events []Event
	for _, e := range opts.Events {
		clashed := false
		for _, symbol := range eventSymbols(e) {
			if symbolTaken[symbol] {
				clashed = true
				break
			}
		}
		if clashed {
			continue
		}
		for _, symbol := range eventSymbols(e) {
			symbolTaken[symbol] = true
		}
		events = append(events, e)
	}

	// HTMXScript is skipped when a route or event claims the name; the
	// caller reports that as a GHTMX-E0404.
	emitScript := opts.HtmxVersion != "" && !symbolTaken["HTMXScript"]

	needsGhtmx := emitScript
	for _, name := range names {
		if len(byName[name].Route.Params) > 0 {
			needsGhtmx = true
			break
		}
	}
	var imports []string
	if needsGhtmx {
		imports = append(imports, "\"github.com/go-monolith/ghtmx\"")
	}
	if len(events) > 0 {
		imports = append(imports, "\"net/http\"", "ghtmxruntime \"github.com/go-monolith/ghtmx/runtime\"")
	}
	if len(imports) > 0 {
		sb.WriteString("import (\n")
		for _, imp := range imports {
			sb.WriteString("\t" + imp + "\n")
		}
		sb.WriteString(")\n\n")
	}

	for _, name := range names {
		c := byName[name]
		r := c.Route
		r.Pos.File = relativePos(opts.ModRoot, r.Pos.File)
		verb := string(r.Verb)
		if r.Verb == routes.AnyVerb {
			verb = "ANY"
		}
		if len(r.Params) == 0 {
			fmt.Fprintf(&sb, "// %sPath is the path of %s %s -> %s (%s).\n", name, verb, r.Path, r.Handler, r.Pos)
			fmt.Fprintf(&sb, "const %sPath = %q\n\n", name, r.Path)
			continue
		}
		writeConstructor(&sb, name, verb, r)
	}

	for _, e := range events {
		if err := writeEvent(&sb, e); err != nil {
			return nil, err
		}
	}

	if emitScript {
		fmt.Fprintf(&sb, "// HTMXScript renders the script tag for the configured htmx version\n// (%s) with its subresource-integrity hash (FR-091): the served asset\n// must be that exact published build or the browser refuses it (FR-052).\n", opts.HtmxVersion)
		sb.WriteString("func HTMXScript(opts ...ghtmx.ScriptOption) ghtmx.Component {\n")
		fmt.Fprintf(&sb, "\treturn ghtmx.HTMXScriptTag(%q, opts...)\n", opts.HtmxVersion)
		sb.WriteString("}\n\n")
	}

	out, err := format.Source([]byte(sb.String()))
	if err != nil {
		return nil, fmt.Errorf("central package generation produced invalid Go (internal defect): %w", err)
	}
	return out, nil
}

// relativePos makes a registration-site path relative to the module root
// for portable doc comments.
func relativePos(modRoot, file string) string {
	if modRoot == "" || file == "" {
		return file
	}
	rel, err := filepath.Rel(modRoot, file)
	if err != nil || strings.HasPrefix(rel, "..") {
		return file
	}
	return filepath.ToSlash(rel)
}

// writeConstructor emits a typed constructor: parameters follow the
// route's path parameters in order, typed string (syntax-only analysis
// cannot infer types), and every value is percent-encoded for its position
// before substitution (FR-023, S1.1).
func writeConstructor(sb *strings.Builder, name, verb string, r routes.Route) {
	params := make([]string, len(r.Params))
	for i, p := range r.Params {
		params[i] = sanitizeParam(p.Name) + " string"
	}
	fmt.Fprintf(sb, "// %s builds the URL for %s %s -> %s (%s).\n", name, verb, r.Path, r.Handler, r.Pos)
	fmt.Fprintf(sb, "func %s(%s) ghtmx.SafeURL {\n", name, strings.Join(params, ", "))
	sb.WriteString("\treturn ghtmx.SafeURL(")

	// Split the normalized pattern into literal and parameter parts.
	rest := r.Path
	paramIndex := 0
	var parts []string
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 || paramIndex >= len(r.Params) {
			if rest != "" {
				parts = append(parts, fmt.Sprintf("%q", rest))
			}
			break
		}
		closing := strings.IndexByte(rest[open:], '}')
		if closing < 0 {
			parts = append(parts, fmt.Sprintf("%q", rest))
			break
		}
		closing += open
		if open > 0 {
			parts = append(parts, fmt.Sprintf("%q", rest[:open]))
		}
		p := r.Params[paramIndex]
		escaper := "EscapePathSegment"
		if p.Wildcard {
			escaper = "EscapePathWildcard"
		}
		parts = append(parts, fmt.Sprintf("ghtmx.%s(%s)", escaper, sanitizeParam(p.Name)))
		paramIndex++
		rest = rest[closing+1:]
	}
	sb.WriteString(strings.Join(parts, " + "))
	sb.WriteString(")\n}\n\n")
}

// sanitizeParam makes a path parameter name usable as a Go parameter.
func sanitizeParam(name string) string {
	var sb strings.Builder
	for _, r := range name {
		if r == '_' || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') {
			sb.WriteRune(r)
		}
	}
	s := sb.String()
	if s == "" || ('0' <= s[0] && s[0] <= '9') {
		return "v"
	}
	// Lower the first rune: parameters are unexported locals.
	s = strings.ToLower(s[:1]) + s[1:]
	if token.IsKeyword(s) {
		return s + "Param"
	}
	return s
}

// writeEvent emits one event's payload type (when the event declares
// parameters) and its emission symbols (FR-037): the sole way to put the
// event on the wire, so an undeclared event fails Go compilation at the
// handler call site and a mismatched payload is a type error. Each event
// gets three timing emitters, one per HX-Trigger header variant.
func writeEvent(sb *strings.Builder, e Event) error {
	fields, err := eventFields(e.Params)
	if err != nil {
		return fmt.Errorf("event %s: %w", e.Name, err)
	}

	timings := []struct{ suffix, fn, header string }{
		{"", "AppendTrigger", "HX-Trigger"},
		{"AfterSettle", "AppendTriggerAfterSettle", "HX-Trigger-After-Settle"},
		{"AfterSwap", "AppendTriggerAfterSwap", "HX-Trigger-After-Swap"},
	}

	if len(fields) > 0 {
		fmt.Fprintf(sb, "// %sPayload is the payload of event %q (declared at %s).\n", e.Name, e.WireName, e.DeclaredAt)
		fmt.Fprintf(sb, "type %sPayload struct {\n", e.Name)
		for _, f := range fields {
			fmt.Fprintf(sb, "\t%s %s `json:%q`\n", exportIdent(f.name), f.typ, f.name)
		}
		sb.WriteString("}\n\n")
		for _, t := range timings {
			fmt.Fprintf(sb, "// Emit%s%s appends event %q to the response's single %s\n// header; multiple emissions in one response merge (FR-037). Call it\n// before writing the response status or body, from one goroutine.\n// Declared at %s.\n", e.Name, t.suffix, e.WireName, t.header, e.DeclaredAt)
			fmt.Fprintf(sb, "func Emit%s%s(w http.ResponseWriter, p %sPayload) error {\n", e.Name, t.suffix, e.Name)
			fmt.Fprintf(sb, "\treturn ghtmxruntime.%s(w, %q, p)\n", t.fn, e.WireName)
			sb.WriteString("}\n\n")
		}
		return nil
	}

	for _, t := range timings {
		fmt.Fprintf(sb, "// Emit%s%s appends the payload-less event %q to the response's single\n// %s header; multiple emissions in one response merge (FR-037).\n// Call it before writing the response status or body, from one\n// goroutine. Declared at %s.\n", e.Name, t.suffix, e.WireName, t.header, e.DeclaredAt)
		fmt.Fprintf(sb, "func Emit%s%s(w http.ResponseWriter) error {\n", e.Name, t.suffix)
		fmt.Fprintf(sb, "\treturn ghtmxruntime.%s(w, %q, nil)\n", t.fn, e.WireName)
		sb.WriteString("}\n\n")
	}
	return nil
}

type eventField struct {
	name string
	typ  string
}

// eventFields expands a declared parameter list into payload struct
// fields; grouped parameters ("a, b string") expand individually.
func eventFields(params string) ([]eventField, error) {
	expr, err := goparser.ParseExpr("func" + params)
	if err != nil {
		return nil, fmt.Errorf("invalid payload parameter list %q: %w", params, err)
	}
	ft, ok := expr.(*goast.FuncType)
	if !ok || ft.Params == nil {
		return nil, fmt.Errorf("invalid payload parameter list %q", params)
	}
	var out []eventField
	for _, field := range ft.Params.List {
		if len(field.Names) == 0 {
			return nil, fmt.Errorf("payload parameters must be named in %q", params)
		}
		typ := gotypes.ExprString(field.Type)
		for _, n := range field.Names {
			if n.Name == "_" {
				return nil, fmt.Errorf("payload parameters must be named in %q", params)
			}
			out = append(out, eventField{name: n.Name, typ: typ})
		}
	}
	return out, nil
}
