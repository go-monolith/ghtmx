package proxy

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/go-monolith/ghtmx/internal/analyzer"
	"github.com/go-monolith/ghtmx/internal/generator/central"
	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
	"github.com/go-monolith/ghtmx/internal/routes"
)

// ghtmx completion providers (FR-081, FR-082): route-aware handler and
// constructor completion inside verb attributes, hx-* attribute name and
// value completion for the configured version, and event-name completion
// from the event registry. Context is detected from the text before the
// cursor on the current line.

var (
	// <div hx-p| — an hx-* attribute name being typed inside a tag, or on
	// its own indented line (the one-attribute-per-line style).
	attrNameContext     = regexp.MustCompile(`<[^>]*\s(hx-[\w:-]*)$`)
	attrNameLineContext = regexp.MustCompile(`^\s+(hx-[\w:-]*)$`)
	// hx-post={ handlers.Cre| — the expression of a verb attribute.
	verbExprContext = regexp.MustCompile(`(hx-(get|post|put|patch|delete))=\{\s*([\w.]*)$`)
	// hx-swap="inner| — a quoted attribute value being typed.
	attrValueContext       = regexp.MustCompile(`(hx-[\w:-]+)="([^"]*)$`)
	attrValueSingleContext = regexp.MustCompile(`(hx-[\w:-]+)='([^']*)$`)
)

// completionContext describes what the cursor position is completing.
type completionContext struct {
	kind    string // "attr-name", "verb-expr", "attr-value", ""
	attr    string // the attribute involved
	verb    routes.Verb
	partial string // text already typed for the item
}

func detectCompletionContext(linePrefix string) completionContext {
	if m := verbExprContext.FindStringSubmatch(linePrefix); m != nil {
		return completionContext{kind: "verb-expr", attr: m[1], verb: routes.Verb(strings.ToUpper(m[2])), partial: m[3]}
	}
	if m := attrValueContext.FindStringSubmatch(linePrefix); m != nil {
		return completionContext{kind: "attr-value", attr: m[1], partial: m[2]}
	}
	if m := attrValueSingleContext.FindStringSubmatch(linePrefix); m != nil {
		return completionContext{kind: "attr-value", attr: m[1], partial: m[2]}
	}
	if m := attrNameContext.FindStringSubmatch(linePrefix); m != nil {
		return completionContext{kind: "attr-name", partial: m[1]}
	}
	if m := attrNameLineContext.FindStringSubmatch(linePrefix); m != nil {
		return completionContext{kind: "attr-name", partial: m[1]}
	}
	return completionContext{}
}

// ghtmxCompletions computes ghtmx-specific completion items for the given
// position. exclusive reports whether the context belongs to the engine
// alone: verb expressions are Go code too, so their items merge with the
// gopls result instead of replacing it.
func (p *Server) ghtmxCompletions(templURI string, position lsp.Position) (items []lsp.CompletionItem, exclusive bool) {
	doc, ok := p.TemplSource.Get(templURI)
	if !ok || int(position.Line) >= len(doc.Lines) {
		return nil, false
	}
	line := doc.Lines[position.Line]
	if int(position.Character) > len(line) {
		return nil, false
	}
	cctx := detectCompletionContext(line[:position.Character])
	switch cctx.kind {
	case "attr-name":
		return p.attributeNameCompletions(cctx), true
	case "verb-expr":
		return p.routeBindingCompletions(cctx), false
	case "attr-value":
		return p.attributeValueCompletions(cctx), true
	}
	return nil, false
}

// attributeNameCompletions offers hx-* names valid for the configured
// version, plus event listeners on an hx-on: prefix (FR-082).
func (p *Server) attributeNameCompletions(cctx completionContext) []lsp.CompletionItem {
	if suffix, ok := strings.CutPrefix(cctx.partial, "hx-on:"); ok {
		if strings.HasPrefix(suffix, ":") || strings.HasPrefix(suffix, "htmx:") {
			return nil // The htmx namespace is not the declared-event registry.
		}
		return p.eventListenerCompletions(suffix)
	}
	if suffix, ok := strings.CutPrefix(cctx.partial, "hx-on-"); ok && cctx.partial != "hx-only" {
		if strings.HasPrefix(suffix, "-") || strings.HasPrefix(suffix, "htmx-") {
			return nil // hx-on--x / hx-on-htmx-x are the htmx namespace.
		}
		return p.eventListenerCompletions(suffix)
	}
	if p.surface == nil {
		return nil
	}
	var items []lsp.CompletionItem
	for _, name := range p.surface.AttributeNames() {
		if !strings.HasPrefix(name, cctx.partial) {
			continue
		}
		items = append(items, lsp.CompletionItem{
			Label:  name,
			Kind:   lsp.CompletionItemKindProperty,
			Detail: "htmx " + p.surface.Version() + " attribute",
		})
	}
	// hx-on: opens the event-listener namespace.
	if strings.HasPrefix("hx-on:", cctx.partial) {
		items = append(items, lsp.CompletionItem{
			Label:  "hx-on:",
			Kind:   lsp.CompletionItemKindProperty,
			Detail: "event listener (DOM event or declared ghtmx event)",
		})
	}
	return items
}

// eventListenerCompletions offers only declared events (FR-082).
func (p *Server) eventListenerCompletions(partial string) []lsp.CompletionItem {
	var items []lsp.CompletionItem
	for _, e := range p.declaredEvents() {
		if !strings.HasPrefix(e.WireName, partial) {
			continue
		}
		items = append(items, lsp.CompletionItem{
			Label:  e.WireName,
			Kind:   lsp.CompletionItemKindEvent,
			Detail: fmt.Sprintf("ghtmx event %s%s", e.Name, e.Params),
		})
	}
	return items
}

// routeBindingCompletions offers handlers registered for the attribute's
// verb, and typed constructors with parameter placeholders (FR-081).
func (p *Server) routeBindingCompletions(cctx completionContext) []lsp.CompletionItem {
	table, constructors, generatedPkg := p.routeState()
	if table == nil {
		return nil
	}
	var items []lsp.CompletionItem
	for _, r := range table.All() {
		if r.Verb != cctx.verb && r.Verb != routes.AnyVerb {
			continue
		}
		if len(r.Params) == 0 {
			// Zero-parameter routes bind by handler symbol.
			label := symbolLabel(r.Handler)
			if !strings.HasPrefix(label, cctx.partial) && !strings.HasPrefix(r.Handler.Name, cctx.partial) {
				continue
			}
			items = append(items, lsp.CompletionItem{
				Label:  label,
				Kind:   lsp.CompletionItemKindFunction,
				Detail: fmt.Sprintf("%s %s", r.Verb, r.Path),
			})
		}
	}
	// Parameterised routes bind through their generated constructors,
	// inserted with parameter placeholders.
	names := make([]string, 0, len(constructors))
	for name := range constructors {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		c := constructors[name]
		if (c.Route.Verb != cctx.verb && c.Route.Verb != routes.AnyVerb) || len(c.Route.Params) == 0 {
			continue
		}
		qualified := generatedPkg + "." + name
		if !strings.HasPrefix(qualified, cctx.partial) && !strings.HasPrefix(name, cctx.partial) {
			continue
		}
		placeholders := make([]string, len(c.Route.Params))
		for i, param := range c.Route.Params {
			placeholders[i] = fmt.Sprintf("${%d:%s}", i+1, param.Name)
		}
		items = append(items, lsp.CompletionItem{
			Label:            qualified + "(…)",
			FilterText:       qualified,
			InsertText:       fmt.Sprintf("%s.%s(%s)", generatedPkg, name, strings.Join(placeholders, ", ")),
			InsertTextFormat: lsp.InsertTextFormatSnippet,
			Kind:             lsp.CompletionItemKindFunction,
			Detail:           fmt.Sprintf("%s %s", c.Route.Verb, c.Route.Path),
		})
	}
	return items
}

// attributeValueCompletions offers values valid for the configured
// version: swap styles for hx-swap, declared events for hx-trigger, and
// surface-enumerated values for everything else (FR-082).
func (p *Server) attributeValueCompletions(cctx completionContext) []lsp.CompletionItem {
	if p.surface == nil {
		return nil
	}
	partial := cctx.partial
	if i := strings.LastIndexAny(partial, " ,"); i >= 0 {
		partial = partial[i+1:]
	}
	var candidates []lsp.CompletionItem
	switch cctx.attr {
	case "hx-swap":
		for _, style := range p.surface.SwapStyles() {
			candidates = append(candidates, lsp.CompletionItem{
				Label:  style,
				Kind:   lsp.CompletionItemKindEnumMember,
				Detail: "hx-swap style",
			})
		}
	case "hx-trigger":
		// Declared events only (FR-082): the compiler owns this namespace.
		for _, e := range p.declaredEvents() {
			candidates = append(candidates, lsp.CompletionItem{
				Label:  e.WireName,
				Kind:   lsp.CompletionItemKindEvent,
				Detail: fmt.Sprintf("ghtmx event %s%s", e.Name, e.Params),
			})
		}
	default:
		// Everything else is data-driven from the configured surface:
		// enumerated values where the surface lists them.
		def, ok := p.surface.Attribute(cctx.attr)
		if !ok || len(def.Values) == 0 {
			return nil
		}
		for _, v := range def.Values {
			candidates = append(candidates, lsp.CompletionItem{
				Label:  v,
				Kind:   lsp.CompletionItemKindEnumMember,
				Detail: string(def.Kind) + " value",
			})
		}
	}
	var items []lsp.CompletionItem
	for _, c := range candidates {
		if strings.HasPrefix(c.Label, partial) {
			items = append(items, c)
		}
	}
	return items
}

// TODO: derive the qualifier from the open template's import block (the
// AST is in astCache) and add an import edit when absent, like the gopls
// path does; the package-base guess breaks on aliased imports.
func symbolLabel(s routes.SymbolRef) string {
	base := pkgBase(s.PkgPath)
	if base == "" || base == "main" {
		return s.Name
	}
	return base + "." + s.Name
}

// routeState snapshots the discovered route table, constructor naming,
// and the generated package name under one lock discipline.
func (p *Server) routeState() (*routes.Table, map[string]central.Constructor, string) {
	p.routeMu.RLock()
	defer p.routeMu.RUnlock()
	return p.routeTable, p.constructors, p.generatedPkgName
}

// declaredEvents snapshots the event registry.
func (p *Server) declaredEvents() []analyzer.EventInfo {
	if p.setAnalysis == nil {
		return nil
	}
	return p.setAnalysis.Events()
}
