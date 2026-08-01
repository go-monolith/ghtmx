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
// contradictory combinations on one element (GHTMX-E0203), and names valid
// in the htmx family but not at the configured version (GHTMX-E0501).
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
		case *parser.FragmentDeclaration:
			walkNodes(t.Children, v.visitNode)
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
	// (outside conditional branches) for combination checking.
	var present []string
	var firstRange parser.Range
	v.walkAttributes(attrs, false, func(name string, rng parser.Range, constantValue string, hasConstantValue bool, conditional bool) {
		if !strings.HasPrefix(name, "hx-") {
			return
		}
		v.validateName(name, rng)
		if hasConstantValue {
			// templ carve-out 1 (FR-004): a verb attribute with a string URL
			// is reinterpreted as a typed binding in .ghtmx.
			if def, ok := v.surface.Attribute(name); ok && def.Verb != "" {
				v.sink.Add(diag.CarveOutStringURL, v.pos(rng),
					fmt.Sprintf("templ carve-out 1: %s takes a typed route binding, not the string URL %q", name, constantValue),
					fmt.Sprintf("bind a handler symbol (%s={ handlers.MyHandler }) or a generated route constructor (%s={ ghtmxgen.MyRoute(...) })", name, name))
				return
			}
			if verr := v.surface.ValidateValue(name, constantValue); verr != nil {
				v.sink.Add(diag.InvalidAttributeValue, v.pos(rng), verr.Error(), "")
			}
		}
		if !conditional {
			if len(present) == 0 {
				firstRange = rng
			}
			present = append(present, name)
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
func (v *attrValidator) walkAttributes(attrs []parser.Attribute, conditional bool, fn func(name string, rng parser.Range, constantValue string, hasConstantValue, conditional bool)) {
	for _, a := range attrs {
		switch attr := a.(type) {
		case *parser.ConstantAttribute:
			if name, rng, ok := constantKey(attr.Key); ok {
				fn(name, rng, attr.Value, true, conditional)
			}
		case *parser.BoolConstantAttribute:
			if name, rng, ok := constantKey(attr.Key); ok {
				fn(name, rng, "", false, conditional)
			}
		case *parser.ExpressionAttribute:
			// Dynamic value: name check only.
			if name, rng, ok := constantKey(attr.Key); ok {
				fn(name, rng, "", false, conditional)
			}
		case *parser.BoolExpressionAttribute:
			if name, rng, ok := constantKey(attr.Key); ok {
				fn(name, rng, "", false, conditional)
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

func (v *attrValidator) validateName(name string, rng parser.Range) {
	if _, ok := v.surface.Attribute(name); ok {
		return
	}
	if v.surface.KnownExtension(name) {
		return
	}
	pos := v.pos(rng)
	// A prefix attribute used without an event suffix.
	if name == "hx-on" || strings.Trim(strings.TrimPrefix(name, "hx-on"), ":-") == "" && strings.HasPrefix(name, "hx-on") {
		v.sink.Add(diag.UnknownAttribute, pos,
			fmt.Sprintf("%s requires an event name", name),
			"use hx-on:<event> for DOM events (hx-on:click) or hx-on::<htmx-event> for htmx events (hx-on::after-request)")
		return
	}
	// Known to the htmx family but not active at the configured version:
	// the FR-052 message contract names both versions.
	if introduced, ok := v.surface.Introduced(name); ok {
		msg := fmt.Sprintf("%s is not available in htmx %s: it was introduced in %s", name, v.surface.Version(), introduced)
		if removed, wasRemoved := v.surface.Removed(name); wasRemoved {
			msg = fmt.Sprintf("%s is not available in htmx %s: it was removed in %s", name, v.surface.Version(), removed)
		}
		v.sink.Add(diag.VersionMismatch, pos, msg, "change the pinned htmxVersion in ghtmx.json, or avoid the attribute")
		return
	}
	suggest := ""
	if names := v.surface.Suggest(name); len(names) > 0 {
		suggest = "did you mean " + strings.Join(names, ", ") + "?"
	}
	v.sink.Add(diag.UnknownAttribute, pos,
		fmt.Sprintf("unknown attribute %s for htmx %s", name, v.surface.Version()), suggest)
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
