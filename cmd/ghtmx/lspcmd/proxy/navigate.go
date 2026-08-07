package proxy

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/go-monolith/ghtmx/internal/analyzer"
	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
	"github.com/go-monolith/ghtmx/internal/lsp/uri"
	"github.com/go-monolith/ghtmx/internal/routes"
)

// Hover and go-to-definition for ghtmx constructs (FR-083, FR-084):
// event references resolve to their .ghtmx declarations with payload
// hovers, and verb-attribute bindings resolve through the route table to
// their Go registration sites. Everything else — components, fragments,
// embedded Go — rides the existing gopls pipeline, whose results map back
// through the source map.
//
// Namespace rules come from the analyzer (EventNameFromAttr,
// EventNamesFromTrigger) so navigation resolves exactly the references
// diagnostics accept: DOM events, htmx-namespace listeners, and qualified
// names fall through untouched.
//
// Positions compare LSP UTF-16 character offsets against byte offsets —
// exact for the ASCII attribute grammar, and consistent with
// completions.go. Attribute values split across lines never match the
// single-line scans and fall through to gopls.

var (
	hxOnAttrName   = regexp.MustCompile(`hx-on[\w:.-]*`)
	hxTriggerValue = regexp.MustCompile(`hx-trigger="([^"]*)"|hx-trigger='([^']*)'`)
	verbExprAround = regexp.MustCompile(`hx-(get|post|put|patch|delete)=\{\s*([\w.]+)`)
)

// eventRefAt returns the declared-event wire name under the cursor, if
// the position sits on an hx-on listener suffix or an hx-trigger event
// token.
func eventRefAt(line string, character int) (wire string, ok bool) {
	for _, m := range hxOnAttrName.FindAllStringIndex(line, -1) {
		if character < m[0] || character > m[1] {
			continue
		}
		w, owned := analyzer.EventNameFromAttr(line[m[0]:m[1]])
		if !owned {
			return "", false
		}
		// The cursor must sit on the event-name suffix, not the hx-on
		// prefix.
		if character >= m[1]-len(w) {
			return w, true
		}
		return "", false
	}
	for _, m := range hxTriggerValue.FindAllStringSubmatchIndex(line, -1) {
		start, end := m[2], m[3]
		if start < 0 {
			start, end = m[4], m[5]
		}
		if character < start || character > end {
			continue
		}
		return triggerEventAt(line[start:end], character-start)
	}
	return "", false
}

// triggerEventAt returns the event token under the cursor within an
// hx-trigger value. Position tracking happens here — filter brackets are
// opaque, and only the first field of each comma-separated spec can name
// an event — but the token only counts when the analyzer's grammar
// extracts it too.
func triggerEventAt(value string, offset int) (wire string, ok bool) {
	depth, specStart := 0, 0
	for i := 0; i <= len(value); i++ {
		if i < len(value) {
			switch value[i] {
			case '[':
				depth++
				continue
			case ']':
				if depth > 0 {
					depth--
				}
				continue
			}
			if value[i] != ',' || depth > 0 {
				continue
			}
		}
		if offset >= specStart && offset <= i {
			ts := specStart
			for ts < i && (value[ts] == ' ' || value[ts] == '\t') {
				ts++
			}
			te := ts
			for te < i && !strings.ContainsRune(" \t[,", rune(value[te])) {
				te++
			}
			if offset < ts || offset > te {
				return "", false
			}
			token := value[ts:te]
			for _, ev := range analyzer.EventNamesFromTrigger(value) {
				if ev == token {
					return token, true
				}
			}
			return "", false
		}
		specStart = i + 1
	}
	return "", false
}

// boundSymbolAt returns the dotted handler or constructor symbol under
// the cursor inside a verb-attribute expression, plus the attribute's
// verb.
func boundSymbolAt(line string, character int) (symbol string, verb routes.Verb, ok bool) {
	for _, m := range verbExprAround.FindAllStringSubmatchIndex(line, -1) {
		if character >= m[4] && character <= m[5] {
			return line[m[4]:m[5]], routes.Verb(strings.ToUpper(line[m[2]:m[3]])), true
		}
	}
	return "", routes.AnyVerb, false
}

// lineAt fetches the document line for a position.
func (p *Server) lineAt(templURI string, position lsp.Position) (string, bool) {
	doc, ok := p.TemplSource.Get(templURI)
	if !ok || int(position.Line) >= len(doc.Lines) {
		return "", false
	}
	return doc.Lines[position.Line], true
}

// declaredEvent resolves a wire name against the registry.
func (p *Server) declaredEvent(wire string) (analyzer.EventInfo, bool) {
	for _, e := range p.declaredEvents() {
		if e.WireName == wire {
			return e, true
		}
	}
	return analyzer.EventInfo{}, false
}

// boundRoute resolves a symbol from a verb expression against the route
// table: constructors by generated name, handlers by package-qualified
// symbol filtered to the attribute's verb.
func (p *Server) boundRoute(symbol string, verb routes.Verb) (routes.Route, bool) {
	table, constructors, generatedPkg := p.routeState()
	if table == nil {
		return routes.Route{}, false
	}
	qualifier, name, qualified := strings.Cut(symbol, ".")
	if qualified && qualifier == generatedPkg {
		// Constructor references resolve regardless of the attribute's
		// verb: a mismatch is the analyzer's diagnostic, and landing on
		// the actual registration is what makes it explicable.
		if c, found := constructors[name]; found {
			return c.Route, true
		}
		// A route without parameters is bound through its generated
		// path constant, which is the constructor's name plus Path —
		// the only form available to a method handler, whose own symbol
		// is dotted.
		if base, isPath := strings.CutSuffix(name, "Path"); isPath {
			if c, found := constructors[base]; found {
				return c.Route, true
			}
		}
		return routes.Route{}, false
	}
	if !qualified {
		name = symbol
	}
	for _, r := range table.All() {
		if r.Handler.Name != name {
			continue
		}
		if r.Verb != verb && r.Verb != routes.AnyVerb {
			continue
		}
		// The qualifier must agree with the handler's package. The base
		// segment is a guess that breaks on aliased imports — the same
		// caveat symbolLabel documents.
		if qualified && qualifier != pkgBase(r.Handler.PkgPath) {
			continue
		}
		return r, true
	}
	return routes.Route{}, false
}

// pkgBase is the last segment of an import path — the usual package
// qualifier, absent aliasing.
func pkgBase(pkgPath string) string {
	parts := strings.Split(pkgPath, "/")
	return parts[len(parts)-1]
}

// lspLocation converts a 1-based position (zero meaning unknown) to an
// LSP location. Files arrive as plain paths or file:// URIs depending on
// whether the entry came from config seeding or a live didOpen.
func lspLocation(file string, line, col uint32) lsp.Location {
	fileURI := file
	if !strings.HasPrefix(fileURI, "file://") {
		fileURI = string(uri.File(fileURI))
	}
	pos := lsp.Position{}
	if line > 0 {
		pos.Line = line - 1
	}
	if col > 0 {
		pos.Character = col - 1
	}
	return lsp.Location{URI: lsp.DocumentURI(fileURI), Range: lsp.Range{Start: pos, End: pos}}
}

// ghtmxDefinition resolves ghtmx-specific definitions at the position:
// event references to their .ghtmx declarations, bound symbols to their
// Go registration sites. ok is false when the position is not a ghtmx
// construct — the caller then proxies to gopls.
func (p *Server) ghtmxDefinition(templURI string, position lsp.Position) ([]lsp.Location, bool) {
	line, found := p.lineAt(templURI, position)
	if !found {
		return nil, false
	}
	if wire, ok := eventRefAt(line, int(position.Character)); ok {
		if e, declared := p.declaredEvent(wire); declared {
			return []lsp.Location{lspLocation(e.Pos.File, e.Pos.Line, e.Pos.Col)}, true
		}
		return nil, true // A ghtmx context with no target: report nothing.
	}
	if symbol, verb, ok := boundSymbolAt(line, int(position.Character)); ok {
		if r, bound := p.boundRoute(symbol, verb); bound && r.Pos.File != "" {
			return []lsp.Location{lspLocation(r.Pos.File, r.Pos.Line, r.Pos.Col)}, true
		}
		// Unresolved symbols fall through to gopls: it may still know the
		// Go identifier.
		return nil, false
	}
	return nil, false
}

// ghtmxHover renders hover content for ghtmx constructs at the position:
// event payload types and route verb/path for bound symbols. ok is false
// when the position is not a ghtmx construct.
func (p *Server) ghtmxHover(templURI string, position lsp.Position) (*lsp.Hover, bool) {
	line, found := p.lineAt(templURI, position)
	if !found {
		return nil, false
	}
	if wire, ok := eventRefAt(line, int(position.Character)); ok {
		e, declared := p.declaredEvent(wire)
		if !declared {
			return nil, true
		}
		// The raw parameter slice can span lines or carry backticks
		// (struct tags mid-typing); keep the code fence intact.
		payload := strings.Join(strings.Fields(strings.ReplaceAll(e.Params, "`", "")), " ")
		if payload == "()" || payload == "" {
			payload = "()  // no payload"
		}
		return &lsp.Hover{
			Contents: lsp.MarkupContent{
				Kind: lsp.Markdown,
				Value: fmt.Sprintf("```go\nevent %s%s\n```\n\nwire name `%s` — declared at %s",
					e.Name, payload, e.WireName, e.Pos),
			},
		}, true
	}
	if symbol, verb, ok := boundSymbolAt(line, int(position.Character)); ok {
		r, bound := p.boundRoute(symbol, verb)
		if !bound {
			return nil, false // Let gopls describe the Go symbol.
		}
		verbLabel := string(r.Verb)
		if r.Verb == routes.AnyVerb {
			verbLabel = "ANY"
		}
		value := fmt.Sprintf("`%s %s`\n\nhandler `%s.%s`", verbLabel, r.Path, r.Handler.PkgPath, r.Handler.Name)
		if r.Pos.File != "" {
			value += fmt.Sprintf(" — registered at %s", r.Pos)
		}
		return &lsp.Hover{
			Contents: lsp.MarkupContent{Kind: lsp.Markdown, Value: value},
		}, true
	}
	return nil, false
}
