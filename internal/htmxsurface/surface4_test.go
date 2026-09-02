package htmxsurface

import (
	"slices"
	"strings"
	"testing"
)

// The 4.0 family lives beside 2.0: the same code resolves both, keyed off
// data the 2.0 file does not carry. These tests pin the 4.x surface and,
// where the two families differ, that 2.x still answers as it always has.

func TestFamiliesAndRanges(t *testing.T) {
	fams := Families()
	var names []string
	seen := map[string]string{}
	for _, f := range fams {
		names = append(names, f.Name)
		for _, v := range f.Versions {
			if prev, dup := seen[v]; dup {
				t.Errorf("version %s is in families %s and %s", v, prev, f.Name)
			}
			seen[v] = f.Name
		}
	}
	if !slices.Equal(names, []string{"2.0", "4.0"}) {
		t.Fatalf("Families() = %v, want [2.0 4.0]", names)
	}
	if got, want := SupportedRanges(), []string{"2.0.0 – 2.0.10", "4.0.0"}; !slices.Equal(got, want) {
		t.Errorf("SupportedRanges() = %v, want %v", got, want)
	}
	if mustSurface(t, "4.0.0").Family() != "4.0" || mustSurface(t, "2.0.10").Family() != "2.0" {
		t.Error("Family() must name the family the version belongs to")
	}
	_, err := ForVersion("3.0.0")
	if err == nil || !strings.Contains(err.Error(), "2.0.0 – 2.0.10") || !strings.Contains(err.Error(), "4.0.0") {
		t.Errorf("the unsupported-version error must list every family range, got %v", err)
	}
	if !slices.Contains(SupportedVersions(), "4.0.0") {
		t.Error("SupportedVersions must include 4.0.0")
	}
}

func TestParseName4(t *testing.T) {
	s := mustSurface(t, "4.0.0")
	ok := []struct {
		name string
		want ParsedName
	}{
		{"hx-swap", ParsedName{Base: "hx-swap"}},
		{"hx-swap:inherited", ParsedName{Base: "hx-swap", Inherited: true}},
		{"hx-vals:inherited:append", ParsedName{Base: "hx-vals", Inherited: true, Append: true}},
		{"hx-vals:append:inherited", ParsedName{Base: "hx-vals", Inherited: true, Append: true}},
		{"hx-vals:append", ParsedName{Base: "hx-vals", Append: true}},
		{"hx-status:422", ParsedName{Base: "hx-status", Suffix: "422"}},
		{"hx-status:5xx", ParsedName{Base: "hx-status", Suffix: "5xx"}},
		{"hx-status:50x", ParsedName{Base: "hx-status", Suffix: "50x"}},
		{"hx-on::after:swap", ParsedName{Base: "hx-on", Suffix: ":after:swap"}},
		{"hx-on:click", ParsedName{Base: "hx-on", Suffix: "click"}},
		{"hx-on:htmx:after:swap", ParsedName{Base: "hx-on", Suffix: "htmx:after:swap"}},
		// hx-on claims everything after its prefix: listeners never inherit.
		{"hx-on:click:inherited", ParsedName{Base: "hx-on", Suffix: "click:inherited"}},
		{"hx-sse:connect", ParsedName{Base: "hx-sse:connect"}},
		{"hx-ws:send", ParsedName{Base: "hx-ws:send"}},
		{"hx-live", ParsedName{Base: "hx-live"}},
		{"hx-live:disabled", ParsedName{Base: "hx-live", Suffix: "disabled"}},
		{"hx-query", ParsedName{Base: "hx-query"}},
		{"hx-prompt:inherited", ParsedName{Base: "hx-prompt", Inherited: true}},
	}
	for _, tt := range ok {
		got, err := s.ParseName(tt.name)
		if err != nil {
			t.Errorf("ParseName(%q) failed: %v", tt.name, err)
			continue
		}
		if got != tt.want {
			t.Errorf("ParseName(%q) = %+v, want %+v", tt.name, got, tt.want)
		}
	}
	bad := []struct {
		name   string
		reason NameReason
		base   string
	}{
		{"hx-status", NameBarePrefix, "hx-status"},
		{"hx-status:42", NameBadSuffix, "hx-status"},
		{"hx-status:abc", NameBadSuffix, "hx-status"},
		{"hx-status:4xx:inherited", NameBadSuffix, "hx-status"},
		{"hx-on", NameBarePrefix, "hx-on"},
		{"hx-on:", NameBarePrefix, "hx-on"},
		{"hx-on::", NameBarePrefix, "hx-on"},
		{"hx-on-click", NameUnknown, "hx-on-click"},
		{"hx-trigger:inherited", NameModifierNotInheritable, "hx-trigger"},
		{"hx-swap-oob:append", NameModifierNotInheritable, "hx-swap-oob"},
		{"hx-swap:inherited:inherited", NameDuplicateModifier, "hx-swap"},
		{"hx-swap:bogus", NameUnknownModifier, "hx-swap"},
		{"hx-vars", NameUnknown, "hx-vars"},
		{"hx-disabled-elt", NameUnknown, "hx-disabled-elt"},
		{"hx-target-404", NameUnknown, "hx-target-404"},
		{"hx-bogus:inherited", NameUnknown, "hx-bogus:inherited"},
		{"hx-live:", NameBadSuffix, "hx-live"},
		{"hx-swap:", NameUnknownModifier, "hx-swap"},
		{"hx-swap:inherited:", NameUnknownModifier, "hx-swap"},
	}
	for _, tt := range bad {
		_, err := s.ParseName(tt.name)
		if err == nil {
			t.Errorf("ParseName(%q) must fail", tt.name)
			continue
		}
		if err.Reason != tt.reason || err.Base != tt.base {
			t.Errorf("ParseName(%q) = %+v (%v), want reason %v base %q", tt.name, err, err, tt.reason, tt.base)
		}
		if err.Error() == "" {
			t.Errorf("ParseName(%q): empty error text", tt.name)
		}
	}
}

func TestParseNameModifiersUnder2(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	_, err := s.ParseName("hx-swap:inherited")
	if err == nil || err.Reason != NameModifiersUnsupported || err.Base != "hx-swap" || err.Modifier != "inherited" {
		t.Errorf("hx-swap:inherited under 2.0.10 = %+v, want NameModifiersUnsupported on hx-swap", err)
	}
	if _, err := s.ParseName("hx-swap:bogus"); err == nil || err.Reason != NameUnknown {
		t.Errorf("hx-swap:bogus under 2.0.10 = %+v, want NameUnknown", err)
	}
	if p, err := s.ParseName("hx-on-click"); err != nil || p.Base != "hx-on" || p.Suffix != "click" {
		t.Errorf("hx-on-click is htmx 2 syntax: %+v %v", p, err)
	}
	if s.HasNameModifiers() || !s.AcceptsOnPrefix("hx-on-") {
		t.Error("2.0 has no name modifiers and accepts hx-on- listeners")
	}
	four := mustSurface(t, "4.0.0")
	if !four.HasNameModifiers() || four.AcceptsOnPrefix("hx-on-") || !four.AcceptsOnPrefix("hx-on:") {
		t.Error("4.0 has name modifiers and only hx-on: listeners")
	}
	if got := four.NameModifiers(); !slices.Equal(got, []string{"inherited", "append"}) {
		t.Errorf("NameModifiers() = %v", got)
	}
}

func TestAttributeLookup4(t *testing.T) {
	s := mustSurface(t, "4.0.0")
	def, ok := s.Attribute("hx-target:inherited")
	if !ok || def.Kind != KindExtendedSelector || !def.Inherited {
		t.Errorf("hx-target:inherited = %+v ok=%v", def, ok)
	}
	if def, ok := s.Attribute("hx-status:404"); !ok || def.Kind != KindStatusConfig {
		t.Errorf("hx-status:404 = %+v ok=%v", def, ok)
	}
	if def, ok := s.Attribute("hx-disable"); !ok || def.Kind != KindExtendedSelector || !def.RequiresValue || def.Hint == "" {
		t.Errorf("hx-disable = %+v ok=%v; must be a required-value selector with a hint", def, ok)
	}
	if def, ok := s.Attribute("hx-prompt"); !ok || def.Extension != "hx-prompt" {
		t.Errorf("hx-prompt = %+v ok=%v; must be the extension attribute", def, ok)
	}
	if def, ok := s.Attribute("hx-query"); !ok || def.Verb != "QUERY" {
		t.Errorf("hx-query = %+v ok=%v", def, ok)
	}
	for _, name := range []string{"hx-vars", "hx-params", "hx-ext", "hx-disinherit", "hx-inherit", "hx-request", "hx-disabled-elt"} {
		if _, ok := s.Attribute(name); ok {
			t.Errorf("%s must be unknown in htmx 4", name)
		}
	}
	names := s.AttributeNames()
	for _, want := range []string{"hx-query", "hx-action", "hx-method", "hx-config", "hx-ignore", "hx-status", "hx-morph-skip", "hx-morph-skip-children", "hx-sse:connect", "hx-ws:send", "hx-live", "hx-preload"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing attribute %s", want)
		}
	}
	for _, gone := range []string{"hx-vars", "hx-ext", "hx-disabled-elt"} {
		if slices.Contains(names, gone) {
			t.Errorf("%s must not be offered for completion", gone)
		}
	}
	if s.KnownExtension("hx-target-404") {
		t.Error("response-targets is not part of htmx 4")
	}
	if !slices.Contains(s.Suggest("hx-quer"), "hx-query") {
		t.Errorf("Suggest(hx-quer) = %v", s.Suggest("hx-quer"))
	}
}

func TestMigrationEntries(t *testing.T) {
	s := mustSurface(t, "4.0.0")
	for _, name := range []string{"hx-vars", "hx-params", "hx-ext", "hx-disinherit", "hx-inherit", "hx-request", "hx-disabled-elt"} {
		m, ok := s.Migration(name)
		if !ok {
			t.Errorf("Migration(%s) must report the removal", name)
			continue
		}
		if m.RemovedIn != "4.0.0" || (m.Hint == "" && m.RenamedTo == "") {
			t.Errorf("Migration(%s) = %+v: needs RemovedIn and a hint or replacement", name, m)
		}
	}
	if m, _ := s.Migration("hx-disabled-elt"); m.RenamedTo != "hx-disable" {
		t.Errorf("hx-disabled-elt must point at hx-disable, got %+v", m)
	}
	if m, _ := s.Migration("hx-request"); m.RenamedTo != "hx-config" {
		t.Errorf("hx-request must point at hx-config, got %+v", m)
	}
	if _, ok := s.Migration("hx-get"); ok {
		t.Error("hx-get is not removed")
	}
	if _, ok := mustSurface(t, "2.0.10").Migration("hx-vars"); ok {
		t.Error("hx-vars is valid (deprecated) in htmx 2")
	}
	hint, ok := s.RemovedPrefixHint("hx-target-404")
	if !ok || !strings.Contains(hint, "hx-status") {
		t.Errorf("RemovedPrefixHint(hx-target-404) = %q %v", hint, ok)
	}
	for _, name := range []string{"hx-target", "hx-targets", "hx-target-"} {
		if _, ok := s.RemovedPrefixHint(name); ok {
			t.Errorf("%s must not match the hx-target- prefix", name)
		}
	}
}

func TestValidateSwapValues4(t *testing.T) {
	s := mustSurface(t, "4.0.0")
	valid := []string{
		"innerHTML", "innerMorph", "outerMorph", "outerSync",
		"before", "prepend", "append", "after", "upsert", "download",
		"innerHTML show:top showTarget:#other",
		"innerHTML scroll:bottom scrollTarget:#list",
		"innerHTML scroll:window:top",
		"innerHTML show:none",
		"outerHTML transition:true swap:100ms settle:1ms",
		"innerHTML focusScroll:true",
		"innerHTML strip:false",
		"innerHTML swapEmpty",
		"innerHTML swapEmpty:true",
		"innerHTML target:#x",
		"innerHTML ignoreTitle:true",
	}
	for _, v := range valid {
		if err := s.ValidateValue("hx-swap", v); err != nil {
			t.Errorf("hx-swap=%q should be valid: %v", v, err)
		}
	}
	invalid := []string{
		"innerHTML show:#other:top",
		"innerHTML scroll:#list:bottom",
		"innerHTML focus-scroll:true",
		"innerHTML show:sideways",
		"innerHTML scrollTarget",
		"innerHTML strip:yes",
		"replace",
	}
	for _, v := range invalid {
		if err := s.ValidateValue("hx-swap", v); err == nil {
			t.Errorf("hx-swap=%q should be invalid", v)
		}
	}
	if err := s.ValidateValue("hx-swap", "innerHTML show:#other:top"); err == nil || !strings.Contains(err.Error(), "show:top showTarget:<selector>") {
		t.Errorf("the combined show form must point at showTarget, got %v", err)
	}
	if err := s.ValidateValue("hx-swap", "innerHTML focus-scroll:true"); err == nil || !strings.Contains(err.Error(), "focusScroll") {
		t.Errorf("focus-scroll must point at focusScroll, got %v", err)
	}
	// A bad position never earns the "use scroll:<position>" replacement.
	for _, v := range []string{"innerHTML scroll:window:sideways", "innerHTML scroll:#list:none"} {
		if err := s.ValidateValue("hx-swap", v); err == nil || strings.Contains(err.Error(), "scrollTarget") {
			t.Errorf("hx-swap=%q must be rejected without suggesting scrollTarget, got %v", v, err)
		}
	}
	if err := s.ValidateValue("hx-swap", "replace"); err == nil || !strings.Contains(err.Error(), "innerMorph") {
		t.Errorf("the style list must include the htmx 4 styles, got %v", err)
	}
	// The modifier-bearing name validates the same grammar.
	if err := s.ValidateValue("hx-swap:inherited", "innerMorph transition:true"); err != nil {
		t.Errorf("hx-swap:inherited: %v", err)
	}
	if err := s.ValidateValue("hx-swap:inherited", "bogus"); err == nil {
		t.Error("hx-swap:inherited=bogus should be invalid")
	}
	if err := s.ValidateValue("hx-swap-oob", "append:#list"); err != nil {
		t.Errorf("hx-swap-oob with an alias style: %v", err)
	}
	// htmx 2 is unchanged: no morph styles, the combined show form is fine.
	two := mustSurface(t, "2.0.10")
	if err := two.ValidateValue("hx-swap", "innerMorph"); err == nil {
		t.Error("innerMorph is htmx 4 only")
	}
	if err := two.ValidateValue("hx-swap", "innerHTML show:#other:top"); err != nil {
		t.Errorf("htmx 2 combined show form: %v", err)
	}
}

func TestValidateTrigger4(t *testing.T) {
	s := mustSurface(t, "4.0.0")
	valid := []string{
		"click prevent",
		"submit halt",
		"keyup[key=='Enter'] stop",
		"click capture passive",
		"intersect root:#p rootMargin:10px threshold:0.5",
		"click from:(form input)",
		"input changed delay:500ms, keyup[key=='Enter']",
		"htmx:after:swap from:body",
		"custom-event from:body",
		"every 2s",
	}
	for _, v := range valid {
		if err := s.ValidateValue("hx-trigger", v); err != nil {
			t.Errorf("hx-trigger=%q should be valid: %v", v, err)
		}
	}
	invalid := map[string]string{
		"click queue:first":         "hx-sync",
		"htmx:afterSwap from:body":  "htmx:after:swap",
		"htmx:after-swap from:body": "htmx:after:swap",
		"htmx:xhr:progress":         "fetch",
		"click delya:1s":            "unknown trigger modifier",
	}
	for v, want := range invalid {
		err := s.ValidateValue("hx-trigger", v)
		if err == nil {
			t.Errorf("hx-trigger=%q should be invalid", v)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("hx-trigger=%q: error %q must mention %q", v, err, want)
		}
	}
	if err := mustSurface(t, "2.0.10").ValidateValue("hx-trigger", "htmx:afterSwap from:body"); err != nil {
		t.Errorf("htmx 2 has no rename table: %v", err)
	}
}

func TestValidateStatusConfig(t *testing.T) {
	s := mustSurface(t, "4.0.0")
	valid := []string{
		"target:#errors select:#validation-errors",
		"swap:none",
		"swap:innerMorph target:closest form",
		"push:/items/new",
		"replace:false transition:true",
		"swap:none push:false",
		"swap:append",
		// htmx 4 parses the value like hx-config: JSON, commas, spaces
		// after the colon, quotes, selectors with spaces or pseudo-classes.
		`{"swap":"none","target":"#errors"}`,
		"swap: none",
		"swap:none, target:#x",
		`swap:"none", transition:'true'`,
		"target:closest select",
		"target:form:has(input) swap:none",
	}
	for _, v := range valid {
		if err := s.ValidateValue("hx-status:422", v); err != nil {
			t.Errorf("hx-status:422=%q should be valid: %v", v, err)
		}
	}
	invalid := []string{"", "  ", "bogus:1", "swap:replace", "transition:yes", "target:", "swap", "swap:none, bogus:1", "swap: replace"}
	for _, v := range invalid {
		if err := s.ValidateValue("hx-status:5xx", v); err == nil {
			t.Errorf("hx-status:5xx=%q should be invalid", v)
		}
	}
	if err := s.ValidateValue("hx-status:5xx", "bogus:1"); err == nil || !strings.Contains(err.Error(), "swap") {
		t.Errorf("an unknown key must list the valid keys, got %v", err)
	}
	// A name the surface rejects is reported by the name check, not here.
	if err := s.ValidateValue("hx-status:42", "swap:none"); err != nil {
		t.Errorf("value validation must not double-report a bad name: %v", err)
	}
}

func TestValidateEnums4(t *testing.T) {
	s := mustSurface(t, "4.0.0")
	for _, v := range []string{"GET", "QUERY", "DELETE"} {
		if err := s.ValidateValue("hx-method", v); err != nil {
			t.Errorf("hx-method=%s: %v", v, err)
		}
	}
	if err := s.ValidateValue("hx-method", "FETCH"); err == nil {
		t.Error("hx-method=FETCH should be invalid")
	}
	if err := s.ValidateValue("hx-history", "false"); err != nil {
		t.Errorf("hx-history is the hx-history-cache extension attribute: %v", err)
	}
	if err := s.ValidateValue("hx-validate:inherited", "true"); err != nil {
		t.Errorf("hx-validate:inherited: %v", err)
	}
}

func TestInheritKeywordRemoved4(t *testing.T) {
	s := mustSurface(t, "4.0.0")
	for _, attr := range []string{"hx-include", "hx-indicator"} {
		err := s.ValidateValue(attr, "inherit")
		if err == nil || !strings.Contains(err.Error(), "removed in htmx 4.0.0") || !strings.Contains(err.Error(), ":inherited") {
			t.Errorf("%s=inherit must be reported as removed with the :inherited hint, got %v", attr, err)
		}
		if err := s.ValidateValue(attr, "closest form"); err != nil {
			t.Errorf("%s=\"closest form\": %v", attr, err)
		}
	}
}

func TestSwapStylesAndAliases4(t *testing.T) {
	s := mustSurface(t, "4.0.0")
	styles := s.SwapStyles()
	if !slices.Contains(styles, "innerMorph") || slices.Contains(styles, "before") {
		t.Errorf("SwapStyles() = %v: core styles only, aliases separate", styles)
	}
	if got := s.SwapStyleAliases()["before"]; got != "beforebegin" {
		t.Errorf("alias before → %q", got)
	}
	if !slices.Contains(s.ExtensionSwapStyles(), "upsert") {
		t.Errorf("ExtensionSwapStyles() = %v", s.ExtensionSwapStyles())
	}
	if len(mustSurface(t, "2.0.10").SwapStyleAliases()) != 0 {
		t.Error("htmx 2 has no swap style aliases")
	}
	if !slices.Contains(s.SwapModifierNames(), "showTarget") || slices.Contains(s.SwapModifierNames(), "focus-scroll") {
		t.Errorf("SwapModifierNames() = %v", s.SwapModifierNames())
	}
}

func TestHtmxEvents4(t *testing.T) {
	s := mustSurface(t, "4.0.0")
	names := s.HtmxEventNames()
	for _, want := range []string{"after:swap", "config:request", "before:request", "sse:error", "ws:close"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing htmx 4 event %s", want)
		}
	}
	for _, name := range names {
		if strings.ToLower(name) != name || strings.ContainsAny(name, " -") || name == "" {
			t.Errorf("event %q is not a lower-case colon-separated htmx 4 name", name)
		}
	}
	renames := map[string]string{"after-swap": "after:swap", "afterSwap": "after:swap", "load": "after:init", "send-error": "error", "sse-open": "sse:after:connection"}
	for old, want := range renames {
		if got, ok := s.EventRename(old); !ok || got != want {
			t.Errorf("EventRename(%s) = %q %v, want %q", old, got, ok, want)
		}
	}
	if _, ok := s.EventRename("after:swap"); ok {
		t.Error("a current name is not a rename")
	}
	if hint, ok := s.RemovedEvent("xhr:progress"); !ok || hint == "" {
		t.Error("xhr:progress must be a removed event with advice")
	}
	if _, ok := mustSurface(t, "2.0.10").EventRename("after-swap"); ok {
		t.Error("htmx 2 has no rename table")
	}
}

func TestHeadersAndConflicts4(t *testing.T) {
	s := mustSurface(t, "4.0.0")
	if _, ok := s.ResponseHeader("HX-Trigger-After-Swap"); ok {
		t.Error("HX-Trigger-After-Swap was removed in htmx 4")
	}
	if _, ok := s.ResponseHeader("HX-Trigger"); !ok {
		t.Error("HX-Trigger is still a response header")
	}
	if _, ok := mustSurface(t, "2.0.10").ResponseHeader("HX-Trigger-After-Swap"); !ok {
		t.Error("HX-Trigger-After-Swap is an htmx 2 response header")
	}
	if got := s.ValidateCombination([]string{"hx-get", "hx-query"}); len(got) != 1 {
		t.Errorf("hx-get with hx-query: %v", got)
	}
	if got := s.ValidateCombination([]string{"hx-action", "hx-post"}); len(got) != 1 {
		t.Errorf("hx-action with hx-post: %v", got)
	}
	if got := s.ValidateCombination([]string{"hx-action", "hx-method", "hx-target"}); len(got) != 0 {
		t.Errorf("hx-action with hx-method: %v", got)
	}
	for _, name := range []string{"hx-get", "hx-query", "hx-action", "hx-method", "hx-sse:connect", "hx-ws:send"} {
		if !s.IssuesRequest(name) {
			t.Errorf("%s issues requests", name)
		}
	}
	for _, name := range []string{"hx-boost", "hx-target", "hx-vars", "hx-on"} {
		if s.IssuesRequest(name) {
			t.Errorf("%s does not issue requests by itself", name)
		}
	}
}
