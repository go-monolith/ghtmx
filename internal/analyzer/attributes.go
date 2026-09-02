// Package analyzer implements the ghtmx semantic analysis passes over the
// shared AST (constitution A2/A4): hx-* attribute validation against the
// pinned htmx surface, and — in later milestones — binding resolution,
// fragment classification, and the event registry.
//
// Analysis is accumulative, never fail-fast: all diagnostics for a run are
// collected so the CLI and LSP report a complete set.
package analyzer

import (
	"fmt"
	"strings"

	"github.com/go-monolith/ghtmx/internal/diag"
	"github.com/go-monolith/ghtmx/internal/htmxsurface"
	parser "github.com/go-monolith/ghtmx/internal/parser"
)

// ValidateAttributes checks every hx-* attribute in the template file
// against the surface for the configured htmx version (FR-024, FR-041,
// FR-044): unknown names (GHTMX-E0201, with did-you-mean suggestions),
// invalid constant values for constrained attributes (GHTMX-E0202),
// contradictory combinations on one element (GHTMX-E0203), names valid in
// the htmx family but not at the configured version — introduced later,
// or removed or renamed earlier, with the replacement named
// (GHTMX-E0501) — and, for families with explicit attribute inheritance,
// inheritable attributes that never reach the descendants issuing the
// requests (GHTMX-W0202).
//
// Only constant names and values are validated: an expression attribute
// key, a dynamic value, or spread attributes are exempt from analysis
// entirely, mirroring the FR-042 conservatism.
func ValidateAttributes(file *parser.TemplateFile, surface *htmxsurface.Surface, sink *diag.Sink) {
	v := &attrValidator{file: file, surface: surface, sink: sink}
	for _, node := range file.Nodes {
		switch t := node.(type) {
		case *parser.HTMLTemplate:
			walkNodes(t.Children, v.visitNode)
			if surface.HasNameModifiers() {
				v.checkInheritance(t.Children)
			}
		case *parser.FragmentDeclaration:
			walkNodes(t.Children, v.visitNode)
			if surface.HasNameModifiers() {
				v.checkInheritance(t.Children)
			}
		}
	}
}

// walkNodes visits nodes depth-first, descending through composite nodes
// (elements, control flow, templ elements).
func walkNodes(nodes []parser.Node, fn func(parser.Node)) {
	for _, n := range nodes {
		fn(n)
		if c, ok := n.(parser.CompositeNode); ok {
			walkNodes(c.ChildNodes(), fn)
		}
	}
}

type attrValidator struct {
	file    *parser.TemplateFile
	surface *htmxsurface.Surface
	sink    *diag.Sink
}

func (v *attrValidator) visitNode(n parser.Node) {
	switch e := n.(type) {
	case *parser.Element:
		v.validateElement(e.Attributes, e.NameRange)
	case *parser.ScriptElement:
		v.validateElement(e.Attributes, e.Range)
	case *parser.RawElement:
		v.validateElement(e.Attributes, e.NameRange)
	}
}

func (v *attrValidator) validateElement(attrs []parser.Attribute, elementRange parser.Range) {
	// Collect the hx-* names that are certainly present on the element
	// (outside conditional branches) for combination checking. Names are
	// recorded by their base so hx-target:inherited counts as hx-target.
	var present []string
	var firstRange parser.Range
	v.walkAttributes(attrs, false, func(name string, rng parser.Range, constantValue string, hasConstantValue, bare, conditional bool) {
		if !strings.HasPrefix(name, "hx-") {
			return
		}
		parsed, ok := v.validateName(name, rng)
		if !ok {
			return
		}
		def, _ := v.surface.Attribute(name)
		if hasConstantValue {
			// templ carve-out 1 (FR-004): a verb attribute with a string URL
			// is reinterpreted as a typed binding in .ghtmx.
			if def.Verb != "" {
				v.sink.Add(diag.CarveOutStringURL, v.pos(rng),
					fmt.Sprintf("templ carve-out 1: %s takes a typed route binding, not the string URL %q", name, constantValue),
					fmt.Sprintf("bind a handler symbol (%s={ handlers.MyHandler }) or a generated route constructor (%s={ ghtmxgen.MyRoute(...) })", name, name))
				return
			}
			if verr := v.surface.ValidateValue(name, constantValue); verr != nil {
				v.sink.Add(diag.InvalidAttributeValue, v.pos(rng), verr.Error(), "")
			}
		} else if bare && def.RequiresValue {
			// The bare boolean form of an attribute whose value is
			// required: in htmx 4 hx-disable names elements, where htmx 2's
			// flag skipped processing.
			v.sink.Add(diag.InvalidAttributeValue, v.pos(rng),
				fmt.Sprintf("%s requires a value in htmx %s", name, v.surface.Version()), def.Hint)
		}
		if !conditional {
			if len(present) == 0 {
				firstRange = rng
			}
			present = append(present, parsed.Base)
		}
	})
	if len(present) > 1 {
		for _, conflict := range v.surface.ValidateCombination(present) {
			v.sink.Add(diag.AttributeConflict, v.pos(firstRange),
				fmt.Sprintf("invalid combination of %s: %s", strings.Join(conflict.Attrs, ", "), conflict.Message), "")
		}
	}
	_ = elementRange
}

// walkAttributes visits every attribute with a constant name, descending
// into conditional branches (marked conditional=true, exempt from
// combination checks since their presence is not statically certain).
// bare reports the valueless boolean forms (hx-disable, hx-disable?={ c }).
func (v *attrValidator) walkAttributes(attrs []parser.Attribute, conditional bool, fn func(name string, rng parser.Range, constantValue string, hasConstantValue, bare, conditional bool)) {
	for _, a := range attrs {
		switch attr := a.(type) {
		case *parser.ConstantAttribute:
			if name, rng, ok := constantKey(attr.Key); ok {
				fn(name, rng, attr.Value, true, false, conditional)
			}
		case *parser.BoolConstantAttribute:
			if name, rng, ok := constantKey(attr.Key); ok {
				fn(name, rng, "", false, true, conditional)
			}
		case *parser.ExpressionAttribute:
			// Dynamic value: name check only.
			if name, rng, ok := constantKey(attr.Key); ok {
				fn(name, rng, "", false, false, conditional)
			}
		case *parser.BoolExpressionAttribute:
			if name, rng, ok := constantKey(attr.Key); ok {
				fn(name, rng, "", false, true, conditional)
			}
		case *parser.ConditionalAttribute:
			v.walkAttributes(attr.Then, true, fn)
			v.walkAttributes(attr.Else, true, fn)
		}
	}
}

func constantKey(key parser.AttributeKey) (string, parser.Range, bool) {
	k, ok := key.(parser.ConstantAttributeKey)
	if !ok {
		return "", parser.Range{}, false
	}
	return k.Name, k.NameRange, true
}

// validateName resolves an hx-* name against the surface and reports every
// way it can fail. ok is true when the name resolved (the parsed form is
// returned) or is a known extension attribute.
func (v *attrValidator) validateName(name string, rng parser.Range) (htmxsurface.ParsedName, bool) {
	parsed, nerr := v.surface.ParseName(name)
	if nerr == nil {
		if parsed.Base == "hx-on" {
			v.checkListenerEvent(name, parsed.Suffix, rng)
		}
		return parsed, true
	}
	if v.surface.KnownExtension(name) {
		return htmxsurface.ParsedName{Base: name}, true
	}
	pos := v.pos(rng)
	version := v.surface.Version()
	switch nerr.Reason {
	case htmxsurface.NameBarePrefix:
		// A suffixed attribute used without its suffix.
		if nerr.Base == "hx-on" {
			v.sink.Add(diag.UnknownAttribute, pos,
				fmt.Sprintf("%s requires an event name", name),
				"use hx-on:<event> for DOM events (hx-on:click) or hx-on::<htmx-event> for htmx events (hx-on::after-request)")
			return parsed, false
		}
		if nerr.Base == "hx-status" {
			v.sink.Add(diag.UnknownAttribute, pos,
				fmt.Sprintf("%s requires a status code suffix", name),
				"use an exact code (hx-status:404) or a wildcard (hx-status:40x, hx-status:4xx)")
			return parsed, false
		}
		v.sink.Add(diag.UnknownAttribute, pos,
			fmt.Sprintf("%s requires a suffix", name), fmt.Sprintf("write %s:<suffix>", nerr.Base))
		return parsed, false
	case htmxsurface.NameBadSuffix:
		suggest := ""
		if nerr.Base == "hx-status" {
			suggest = "use an exact code (hx-status:404) or a wildcard (hx-status:40x, hx-status:4xx)"
		}
		v.sink.Add(diag.UnknownAttribute, pos,
			fmt.Sprintf("%s: %q is not a valid %s suffix", name, nerr.Modifier, nerr.Base), suggest)
		return parsed, false
	case htmxsurface.NameModifiersUnsupported:
		v.sink.Add(diag.VersionMismatch, pos,
			fmt.Sprintf("%s is not available in htmx %s: attribute-name modifiers such as :%s were introduced in htmx 4.0.0", name, version, nerr.Modifier),
			"pin htmxVersion to 4.0.0 or later in ghtmx.json, or drop the modifier")
		return parsed, false
	case htmxsurface.NameUnknownModifier:
		msg := fmt.Sprintf("unknown attribute-name modifier :%s on %s", nerr.Modifier, nerr.Base)
		if nerr.Modifier == "" {
			msg = fmt.Sprintf("%s has a trailing colon", name)
		}
		v.sink.Add(diag.UnknownAttribute, pos, msg,
			"valid modifiers: "+strings.Join(prefixEach(":", v.surface.NameModifiers()), ", "))
		return parsed, false
	case htmxsurface.NameModifierNotInheritable:
		v.sink.Add(diag.UnknownAttribute, pos,
			fmt.Sprintf("%s does not inherit in htmx %s; :%s applies only to inheritable attributes", nerr.Base, version, nerr.Modifier),
			fmt.Sprintf("remove :%s", nerr.Modifier))
		return parsed, false
	case htmxsurface.NameDuplicateModifier:
		v.sink.Add(diag.UnknownAttribute, pos,
			fmt.Sprintf("duplicate attribute-name modifier :%s on %s", nerr.Modifier, nerr.Base),
			fmt.Sprintf("write :%s once", nerr.Modifier))
		return parsed, false
	}
	// Dash-form listeners are htmx 2 syntax; htmx 4 spells them hx-on:.
	if strings.HasPrefix(name, "hx-on-") && !v.surface.AcceptsOnPrefix("hx-on-") {
		v.sink.Add(diag.VersionMismatch, pos,
			fmt.Sprintf("%s is not available in htmx %s: dash-form listeners (hx-on-<event>) are htmx 2 syntax", name, version),
			"write hx-on:<event> for DOM events, or hx-on::<htmx-event> for htmx events")
		return parsed, false
	}
	// Removed or renamed by the configured family: name the replacement.
	base, _, _ := strings.Cut(name, ":")
	if m, ok := v.surface.Migration(base); ok {
		msg := fmt.Sprintf("%s is not available in htmx %s: it was removed in %s", base, version, m.RemovedIn)
		suggest := m.Hint
		if m.RenamedTo != "" {
			msg = fmt.Sprintf("%s is not available in htmx %s: it was renamed to %s in %s", base, version, m.RenamedTo, m.RemovedIn)
			suggest = "use " + m.RenamedTo
			if m.Hint != "" {
				suggest += "; " + m.Hint
			}
		}
		if suggest == "" {
			suggest = "change the pinned htmxVersion in ghtmx.json, or avoid the attribute"
		}
		v.sink.Add(diag.VersionMismatch, pos, msg, suggest)
		return parsed, false
	}
	if hint, ok := v.surface.RemovedPrefixHint(name); ok {
		v.sink.Add(diag.VersionMismatch, pos,
			fmt.Sprintf("%s is not available in htmx %s", name, version), hint)
		return parsed, false
	}
	// Known to the htmx family but not active at the configured version:
	// the FR-052 message contract names both versions.
	if introduced, ok := v.surface.Introduced(name); ok {
		msg := fmt.Sprintf("%s is not available in htmx %s: it was introduced in %s", name, version, introduced)
		if removed, wasRemoved := v.surface.Removed(name); wasRemoved {
			msg = fmt.Sprintf("%s is not available in htmx %s: it was removed in %s", name, version, removed)
		}
		v.sink.Add(diag.VersionMismatch, pos, msg, "change the pinned htmxVersion in ghtmx.json, or avoid the attribute")
		return parsed, false
	}
	suggest := ""
	if names := v.surface.Suggest(name); len(names) > 0 {
		suggest = "did you mean " + strings.Join(names, ", ") + "?"
	}
	v.sink.Add(diag.UnknownAttribute, pos,
		fmt.Sprintf("unknown attribute %s for htmx %s", name, version), suggest)
	return parsed, false
}

// checkListenerEvent reports an hx-on:: listener for an htmx event the
// configured family renamed or removed (htmx 2's after-request is htmx
// 4's after:request). Families without a rename table report nothing.
func (v *attrValidator) checkListenerEvent(name, suffix string, rng parser.Range) {
	event, ok := strings.CutPrefix(suffix, ":")
	if !ok {
		event, ok = strings.CutPrefix(suffix, "htmx:")
	}
	if !ok {
		return
	}
	version := v.surface.Version()
	if current, renamed := v.surface.EventRename(event); renamed {
		v.sink.Add(diag.VersionMismatch, v.pos(rng),
			fmt.Sprintf("%s is not available in htmx %s: the htmx event %s is named %s", name, version, event, current),
			fmt.Sprintf("write hx-on::%s", current))
		return
	}
	if hint, removed := v.surface.RemovedEvent(event); removed {
		v.sink.Add(diag.VersionMismatch, v.pos(rng),
			fmt.Sprintf("%s is not available in htmx %s: the htmx event %s was removed", name, version, event), hint)
	}
}

func prefixEach(prefix string, items []string) []string {
	out := make([]string, len(items))
	for i, item := range items {
		out[i] = prefix + item
	}
	return out
}

// pos converts a parser range (0-indexed, LSP-style) into a display
// position (1-indexed).
func (v *attrValidator) pos(rng parser.Range) diag.Position {
	return diag.Position{
		File:  v.file.Filepath,
		Line:  rng.From.Line + 1,
		Col:   rng.From.Col + 1,
		Index: rng.From.Index,
	}
}
