package analyzer

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/go-monolith/ghtmx/internal/diag"
	parser "github.com/go-monolith/ghtmx/internal/parser"
)

// Explicit attribute inheritance (htmx 4). In htmx 2 an hx-target on a
// wrapper reached every descendant; in htmx 4 it reaches nothing unless
// written hx-target:inherited. The upgrade trap is a wrapper that carries
// an inheritable attribute for the requests underneath it: the markup is
// valid, and the requests silently swap into the wrong place. GHTMX-W0202
// flags that shape, following the heuristics of htmx's own upgrade-check:
// a wrapper's inheritable attribute without :inherited warns when a
// descendant issues a request; hx-headers warns regardless (layouts
// render their children elsewhere, and a CSRF header that never reaches
// the requests is the common case); hx-boost warns when an <a> or <form>
// sits underneath. Content rendered elsewhere (component calls, children)
// is opaque, so the check stays conservative there.

var csrfPattern = regexp.MustCompile(`(?i)csrf|xsrf`)

// subtreeFacts summarises what a subtree contains.
type subtreeFacts struct {
	// request is set when an element in the subtree issues requests;
	// requestLine is the 1-indexed line of the first such element.
	request     bool
	requestLine int
	// anchorOrForm is set when the subtree holds an <a> or <form>.
	anchorOrForm bool
}

func (f *subtreeFacts) merge(o subtreeFacts) {
	if o.request && !f.request {
		f.request, f.requestLine = true, o.requestLine
	}
	f.anchorOrForm = f.anchorOrForm || o.anchorOrForm
}

// checkInheritance runs the explicit-inheritance check over a template
// body.
func (v *attrValidator) checkInheritance(nodes []parser.Node) {
	for _, n := range nodes {
		v.inheritanceSubtree(n)
	}
}

// inheritanceSubtree checks n and returns the facts of its subtree.
func (v *attrValidator) inheritanceSubtree(n parser.Node) subtreeFacts {
	switch e := n.(type) {
	case *parser.Element:
		return v.inheritanceElement(e)
	case *parser.TemplElementExpression:
		// The block passed to a component (@layout() { … }) is rendered
		// wherever the component puts its children, so it contributes no
		// facts to the enclosing element — but it is a tree of its own,
		// and nearly every page body lives in one, so it is checked.
		v.checkInheritance(e.Children)
		return subtreeFacts{}
	case *parser.CallTemplateExpression, *parser.ChildrenExpression:
		// Rendered elsewhere: what it contains is not visible here.
		return subtreeFacts{}
	case parser.CompositeNode:
		var facts subtreeFacts
		for _, c := range e.ChildNodes() {
			facts.merge(v.inheritanceSubtree(c))
		}
		return facts
	}
	return subtreeFacts{}
}

type ownAttribute struct {
	name      string
	base      string
	inherited bool
	value     string
	rng       parser.Range
}

func (v *attrValidator) inheritanceElement(e *parser.Element) subtreeFacts {
	isAnchorOrForm := e.Name == "a" || e.Name == "form"
	var own []ownAttribute
	self := false
	v.walkAttributes(e.Attributes, false, func(name string, rng parser.Range, value string, hasConstantValue, bare, conditional bool) {
		if !strings.HasPrefix(name, "hx-") {
			return
		}
		p, err := v.surface.ParseName(name)
		if err != nil {
			return
		}
		// hx-boost issues requests only from the anchors and forms it is
		// on; on a wrapper it is inheritance like any other attribute.
		if v.surface.IssuesRequest(p.Base) || (p.Base == "hx-boost" && isAnchorOrForm) {
			self = true
		}
		own = append(own, ownAttribute{name: name, base: p.Base, inherited: p.Inherited, value: value, rng: rng})
	})

	var below subtreeFacts
	for _, c := range e.Children {
		below.merge(v.inheritanceSubtree(c))
	}

	// The response-routing attributes of <hx-partial> and <template hx>
	// are swap instructions, not inheritance. An element issuing its own
	// request consumes its own attributes — except hx-headers, which a
	// descendant's separate request still needs (a form carrying the
	// CSRF header around a row's hx-delete button).
	if e.Name != "hx-partial" && e.Name != "template" {
		for _, a := range own {
			def, ok := v.surface.Attribute(a.name)
			if !ok || !def.Inherited || a.inherited {
				continue
			}
			switch {
			case self && a.base == "hx-headers":
				if !below.request {
					continue
				}
			case self:
				continue
			case a.base == "hx-boost":
				if isAnchorOrForm || !below.anchorOrForm {
					continue
				}
			case a.base == "hx-headers":
				// Warns regardless of descendants: layouts render their
				// children elsewhere.
			case !below.request:
				continue
			}
			v.warnInheritance(e, a, below)
		}
	}

	facts := below
	if self {
		facts.request, facts.requestLine = true, int(e.NameRange.From.Line)+1
	}
	facts.anchorOrForm = facts.anchorOrForm || isAnchorOrForm
	return facts
}

func (v *attrValidator) warnInheritance(e *parser.Element, a ownAttribute, below subtreeFacts) {
	msg := fmt.Sprintf("%s on <%s> is not inherited by its descendants: htmx 4 attribute inheritance is explicit", a.name, e.Name)
	switch {
	case a.base == "hx-boost" && a.value == "false":
		msg += " (an <a> or <form> underneath still follows the nearest hx-boost:inherited ancestor)"
	case a.base == "hx-boost":
		msg += " (an <a> or <form> underneath is not boosted)"
	case below.request:
		msg += fmt.Sprintf(" (a descendant on line %d issues a request)", below.requestLine)
	}
	if a.base == "hx-headers" && csrfPattern.MatchString(a.value) {
		msg += "; this looks like a CSRF token, and requests from descendants will be sent without it"
	}
	v.sink.Add(diag.ImplicitInheritance, v.pos(a.rng), msg,
		fmt.Sprintf("write %s:inherited=…, move it onto the requesting element, or silence GHTMX-W0202 if the page sets htmx.config.implicitInheritance", a.base))
}
