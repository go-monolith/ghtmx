package htmxsurface

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// ValueError describes an invalid constant attribute value.
type ValueError struct {
	Message string
	// Valid lists the valid values or forms, for the FR-024 message
	// contract ("listing the valid values").
	Valid []string
}

func (e *ValueError) Error() string {
	if len(e.Valid) == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s (valid: %s)", e.Message, strings.Join(e.Valid, ", "))
}

var timingPattern = regexp.MustCompile(`^(\d+(\.\d+)?|\.\d+)(ms|s|m)?$`)

// ValidateValue checks a constant attribute value against the attribute's
// grammar. Only constant values are ever validated: any value containing an
// interpolated expression is exempt from analysis entirely, mirroring the
// FR-042 conservatism. A nil return means the value is valid.
//
// Validation depth (MVP): full grammar for swap, sync, params, and
// enum-valued attributes; token-level checks for trigger; presence-only for
// JSON-ish kinds (hx-vals, hx-headers, hx-request) and free-form kinds.
func (s *Surface) ValidateValue(attr string, value string) *ValueError {
	def, ok := s.Attribute(attr)
	if !ok {
		return nil // Unknown attribute: reported separately as GHTMX-E0201.
	}
	switch def.Kind {
	case KindEnum:
		if slices.Contains(def.Values, value) {
			return nil
		}
		return &ValueError{Message: fmt.Sprintf("invalid value %q for %s", value, attr), Valid: def.Values}
	case KindSwap:
		return s.validateSwap(attr, value)
	case KindSwapOOB:
		return s.validateSwapOOB(attr, value)
	case KindSync:
		return s.validateSync(attr, value)
	case KindParams:
		return s.validateParams(attr, value)
	case KindTrigger:
		return s.validateTrigger(attr, value)
	case KindExtendedSelector:
		// Selectors are free-form, but value keywords carry version
		// metadata: "inherit" on hx-include/hx-indicator/hx-disabled-elt
		// exists only from htmx 2.0.5 (FR-052).
		for _, kw := range def.ExtraKeywords {
			if value == kw.Value && !activeAt(s.version, kw.Introduced, kw.Removed) {
				return &ValueError{Message: fmt.Sprintf("%s=%q requires htmx %s or later; the configured version is %s", attr, value, kw.Introduced, s.version)}
			}
		}
		return nil
	case KindBoolOrURL:
		// true, false, or any URL: nothing to reject statically.
		return nil
	default:
		// Free-form or JSON-ish kinds: presence-only in the MVP.
		return nil
	}
}

// validateSwap checks the full hx-swap grammar: a swap style followed by
// space-separated modifiers.
func (s *Surface) validateSwap(attr, value string) *ValueError {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return &ValueError{Message: fmt.Sprintf("%s must name a swap style", attr), Valid: s.fam.SwapStyles}
	}
	style := fields[0]
	if !slices.Contains(s.fam.SwapStyles, style) {
		return &ValueError{Message: fmt.Sprintf("unknown swap style %q in %s", style, attr), Valid: s.fam.SwapStyles}
	}
	for _, mod := range fields[1:] {
		if err := s.validateSwapModifier(attr, mod); err != nil {
			return err
		}
	}
	return nil
}

func (s *Surface) validateSwapModifier(attr, mod string) *ValueError {
	name, arg, hasArg := strings.Cut(mod, ":")
	grammar, ok := s.fam.SwapModifiers[name]
	if !ok {
		return &ValueError{Message: fmt.Sprintf("unknown swap modifier %q in %s", name, attr), Valid: sortedModifierNames(s.fam.SwapModifiers)}
	}
	switch grammar {
	case "bool":
		if !hasArg || (arg != "true" && arg != "false") {
			return &ValueError{Message: fmt.Sprintf("swap modifier %q in %s requires true or false", name, attr), Valid: []string{name + ":true", name + ":false"}}
		}
	case "timing":
		if !hasArg || !timingPattern.MatchString(arg) {
			return &ValueError{Message: fmt.Sprintf("swap modifier %q in %s requires a timing value such as 500ms or 1s", name, attr)}
		}
	case "flag-or-bool":
		if hasArg && arg != "true" && arg != "false" {
			return &ValueError{Message: fmt.Sprintf("swap modifier %q in %s takes no argument, or true/false", name, attr)}
		}
	case "scroll-spec", "show-spec":
		// scroll:top|bottom, scroll:<sel>:top|bottom,
		// show:top|bottom|none, show:window:top|bottom, show:<sel>:top|bottom.
		if !hasArg {
			return &ValueError{Message: fmt.Sprintf("swap modifier %q in %s requires an argument", name, attr), Valid: []string{name + ":top", name + ":bottom"}}
		}
		last := arg
		if i := strings.LastIndex(arg, ":"); i >= 0 {
			last = arg[i+1:]
		}
		valid := last == "top" || last == "bottom"
		if grammar == "show-spec" && last == "none" {
			valid = true
		}
		if !valid {
			return &ValueError{Message: fmt.Sprintf("swap modifier %q in %s must end in top or bottom", name, attr), Valid: []string{name + ":top", name + ":bottom", name + ":<selector>:top"}}
		}
	}
	return nil
}

// validateSwapOOB checks hx-swap-oob: true, any valid hx-swap value
// (style plus modifiers), or such a value followed by :selector.
func (s *Surface) validateSwapOOB(attr, value string) *ValueError {
	if value == "true" {
		return nil
	}
	// The whole value may be a plain swap value with modifiers.
	if s.validateSwap(attr, value) == nil {
		return nil
	}
	// Or a swap value followed by a colon and a CSS selector. The selector
	// may itself contain colons, so try each split point from the right.
	rest := value
	for {
		i := strings.LastIndex(rest, ":")
		if i < 0 {
			break
		}
		rest = rest[:i]
		if s.validateSwap(attr, rest) == nil {
			return nil
		}
	}
	// Report against the first token so the message names the swap style.
	first, _, _ := strings.Cut(value, ":")
	first, _, _ = strings.Cut(first, " ")
	valid := append([]string{"true"}, s.fam.SwapStyles...)
	return &ValueError{Message: fmt.Sprintf("invalid value %q for %s: expected true, an hx-swap value, or value:selector (got style %q)", value, attr, first), Valid: valid}
}

// validateSync checks hx-sync: <selector>[:strategy]. Because CSS selectors
// may themselves contain colons, only a recognized-looking strategy suffix
// is validated; anything else passes conservatively.
func (s *Surface) validateSync(attr, value string) *ValueError {
	if value == "" {
		return &ValueError{Message: fmt.Sprintf("%s requires a selector, optionally followed by a strategy", attr), Valid: s.fam.SyncStrategies}
	}
	i := strings.LastIndex(value, ":")
	if i < 0 {
		return nil // Selector only; default strategy applies.
	}
	suffix := strings.Join(strings.Fields(value[i+1:]), " ")
	// "queue first" etc. arrive as "<sel>:queue first".
	if slices.Contains(s.fam.SyncStrategies, suffix) {
		return nil
	}
	if head, _, ok := strings.Cut(suffix, " "); ok && head == "queue" {
		return &ValueError{Message: fmt.Sprintf("invalid queue strategy %q in %s", suffix, attr), Valid: []string{"queue first", "queue last", "queue all"}}
	}
	// The suffix may be part of the selector (e.g. a pseudo-class): pass.
	return nil
}

// validateParams checks hx-params: * | none | not <list> | <list>.
func (s *Surface) validateParams(attr, value string) *ValueError {
	if value == "*" || value == "none" {
		return nil
	}
	rest := strings.TrimPrefix(value, "not ")
	if strings.TrimSpace(rest) == "" {
		return &ValueError{Message: fmt.Sprintf("invalid value %q for %s", value, attr), Valid: []string{"*", "none", "not <param-list>", "<param-list>"}}
	}
	return nil
}

// selectorModifiers take an extended CSS selector argument that may itself
// contain spaces (from:closest form, target:find .item, root:#parent).
// Tokens following such a modifier are consumed as the selector until the
// next known modifier.
var selectorModifiers = map[string]bool{"from": true, "target": true, "root": true}

// validateTrigger performs token-level validation of hx-trigger: event
// names with optional [filter] and modifiers, comma separated. Filter
// bracket contents are skipped verbatim; unknown event names pass (custom
// events are legitimate), but unknown modifier names fail.
func (s *Surface) validateTrigger(attr, value string) *ValueError {
	for _, spec := range splitTopLevel(value, ',') {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			continue
		}
		tokens := splitTopLevel(spec, ' ')
		modifiers := tokens[1:]
		// Polling form: "every 2s ...modifiers".
		if tokens[0] == "every" {
			if len(tokens) < 2 || !timingPattern.MatchString(strings.TrimSpace(tokens[1])) {
				return &ValueError{Message: fmt.Sprintf("polling trigger in %s requires a timing value, e.g. \"every 2s\"", attr)}
			}
			modifiers = tokens[2:]
		}
		if err := s.validateTriggerModifiers(attr, modifiers); err != nil {
			return err
		}
	}
	return nil
}

func (s *Surface) validateTriggerModifiers(attr string, tokens []string) *ValueError {
	for i := 0; i < len(tokens); i++ {
		tok := strings.TrimSpace(tokens[i])
		if tok == "" || strings.HasPrefix(tok, "[") {
			continue
		}
		name, arg, _ := strings.Cut(tok, ":")
		if !slices.Contains(s.fam.TriggerModifiers, name) {
			return &ValueError{Message: fmt.Sprintf("unknown trigger modifier %q in %s", name, attr), Valid: s.fam.TriggerModifiers}
		}
		switch {
		case selectorModifiers[name]:
			// The selector argument may span several tokens (closest form,
			// find .item); consume until the next known modifier. A token
			// shaped like word:value whose word is a near-miss of a known
			// modifier is treated as a typo'd modifier, not selector text,
			// so "delya:1s" still errors while "div:first-child" passes.
			for i+1 < len(tokens) {
				next := strings.TrimSpace(tokens[i+1])
				head, _, hasColon := strings.Cut(next, ":")
				if next == "" || strings.HasPrefix(next, "[") || slices.Contains(s.fam.TriggerModifiers, head) {
					break
				}
				if hasColon && likelyModifierTypo(head, s.fam.TriggerModifiers) {
					break
				}
				i++
			}
		case name == "queue":
			if !slices.Contains(s.fam.TriggerQueueValues, arg) {
				return &ValueError{Message: fmt.Sprintf("invalid queue value %q in %s", arg, attr), Valid: s.fam.TriggerQueueValues}
			}
		case name == "delay" || name == "throttle":
			if !timingPattern.MatchString(arg) {
				return &ValueError{Message: fmt.Sprintf("trigger modifier %q in %s requires a timing value such as 500ms or 1s", name, attr)}
			}
		}
	}
	return nil
}

// splitTopLevel splits on sep outside [...] brackets and (...) parens.
func splitTopLevel(s string, sep rune) []string {
	var out []string
	depth := 0
	start := 0
	for i, r := range s {
		switch r {
		case '[', '(':
			depth++
		case ']', ')':
			if depth > 0 {
				depth--
			}
		case sep:
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

func sortedModifierNames(m map[string]string) []string {
	return slices.Sorted(maps.Keys(m))
}

// likelyModifierTypo reports whether a word:value token's word is a
// near-miss (edit distance <= 2) of a known trigger modifier.
func likelyModifierTypo(word string, modifiers []string) bool {
	for _, m := range modifiers {
		if editDistance(word, m) <= 2 {
			return true
		}
	}
	return false
}
