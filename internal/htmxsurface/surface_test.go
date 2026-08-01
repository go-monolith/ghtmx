package htmxsurface

import (
	"slices"
	"strings"
	"testing"
)

func mustSurface(t *testing.T, version string) *Surface {
	t.Helper()
	s, err := ForVersion(version)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestForVersion(t *testing.T) {
	t.Run("supported versions resolve", func(t *testing.T) {
		for _, v := range []string{"2.0.0", "2.0.5", "2.0.10"} {
			s, err := ForVersion(v)
			if err != nil {
				t.Fatalf("version %s: %v", v, err)
			}
			if s.Version() != v {
				t.Errorf("Version() = %q, want %q", s.Version(), v)
			}
		}
	})
	t.Run("unsupported version names the range", func(t *testing.T) {
		_, err := ForVersion("1.9.12")
		if err == nil {
			t.Fatal("expected an error for htmx 1.x")
		}
		if !strings.Contains(err.Error(), "2.0.0") || !strings.Contains(err.Error(), "2.0.10") {
			t.Errorf("error must name the supported range, got %q", err)
		}
	})
}

func TestAttributeLookup(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	def, ok := s.Attribute("hx-get")
	if !ok || def.Kind != KindURL || def.Verb != "GET" {
		t.Errorf("hx-get = %+v ok=%v", def, ok)
	}
	if _, ok := s.Attribute("hx-fetch"); ok {
		t.Error("hx-fetch must be unknown")
	}
	// hx-on prefixed forms resolve to the hx-on definition.
	for _, name := range []string{"hx-on:click", "hx-on::after-request", "hx-on--before-request"} {
		def, ok := s.Attribute(name)
		if !ok || def.Kind != KindOn {
			t.Errorf("%s should resolve to hx-on, got %+v ok=%v", name, def, ok)
		}
	}
	if _, ok := s.Attribute("hx-vars"); !ok {
		t.Error("hx-vars is deprecated but still valid in 2.0.x")
	}
}

func TestSuggest(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	got := s.Suggest("hx-swp")
	if len(got) == 0 || got[0] != "hx-swap" {
		t.Errorf("Suggest(hx-swp) = %v, want hx-swap first", got)
	}
	got = s.Suggest("hx-trigge")
	if !slices.Contains(got, "hx-trigger") {
		t.Errorf("Suggest(hx-trigge) = %v, want hx-trigger included", got)
	}
}

func TestValidateCombination(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	t.Run("two verb attributes conflict", func(t *testing.T) {
		got := s.ValidateCombination([]string{"hx-get", "hx-post", "hx-target"})
		if len(got) != 1 {
			t.Fatalf("expected 1 conflict, got %v", got)
		}
	})
	t.Run("hx-vals with hx-vars conflicts", func(t *testing.T) {
		got := s.ValidateCombination([]string{"hx-vals", "hx-vars"})
		if len(got) != 1 {
			t.Fatalf("expected 1 conflict, got %v", got)
		}
	})
	t.Run("a single verb with modifiers is fine", func(t *testing.T) {
		got := s.ValidateCombination([]string{"hx-post", "hx-target", "hx-swap", "hx-vals"})
		if len(got) != 0 {
			t.Fatalf("expected no conflicts, got %v", got)
		}
	})
}

func TestValidateSwapValues(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	valid := []string{
		"innerHTML",
		"outerHTML transition:true",
		"beforeend swap:1s settle:20ms",
		"none",
		"textContent",
		"innerHTML scroll:bottom",
		"innerHTML show:#answer:top",
		"innerHTML show:window:top",
		"innerHTML focus-scroll:false",
		"innerHTML ignoreTitle",
		"innerHTML ignoreTitle:true",
	}
	for _, v := range valid {
		if err := s.ValidateValue("hx-swap", v); err != nil {
			t.Errorf("hx-swap=%q should be valid: %v", v, err)
		}
	}
	invalid := []string{
		"innerHtml",           // wrong case
		"replace",             // not a style
		"innerHTML swap:fast", // not a timing
		"innerHTML transition:yes",
		"innerHTML scroll:sideways",
		"innerHTML unknown:1",
	}
	for _, v := range invalid {
		if err := s.ValidateValue("hx-swap", v); err == nil {
			t.Errorf("hx-swap=%q should be invalid", v)
		}
	}
	// The error lists valid values (FR-024).
	err := s.ValidateValue("hx-swap", "replace")
	if err == nil || !strings.Contains(err.Error(), "innerHTML") {
		t.Errorf("swap error should list valid styles, got %v", err)
	}
}

func TestValidateSwapOOB(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	for _, v := range []string{"true", "outerHTML", "beforeend:#list", "innerHTML:#messages"} {
		if err := s.ValidateValue("hx-swap-oob", v); err != nil {
			t.Errorf("hx-swap-oob=%q should be valid: %v", v, err)
		}
	}
	for _, v := range []string{"false", "replace:#x"} {
		if err := s.ValidateValue("hx-swap-oob", v); err == nil {
			t.Errorf("hx-swap-oob=%q should be invalid", v)
		}
	}
}

func TestValidateSync(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	valid := []string{
		"this:drop",
		"closest form:abort",
		"#submit-btn:queue first",
		"this",            // selector only
		"form:has(input)", // pseudo-class colon must not be misread
	}
	for _, v := range valid {
		if err := s.ValidateValue("hx-sync", v); err != nil {
			t.Errorf("hx-sync=%q should be valid: %v", v, err)
		}
	}
	if err := s.ValidateValue("hx-sync", "this:queue firts"); err == nil {
		t.Error("hx-sync with a misspelled queue strategy should be invalid")
	}
}

func TestValidateParams(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	for _, v := range []string{"*", "none", "not id,created", "id,name"} {
		if err := s.ValidateValue("hx-params", v); err != nil {
			t.Errorf("hx-params=%q should be valid: %v", v, err)
		}
	}
	if err := s.ValidateValue("hx-params", "not "); err == nil {
		t.Error("hx-params=\"not \" should be invalid")
	}
}

func TestValidateTrigger(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	valid := []string{
		"click",
		"click once",
		"keyup changed delay:500ms",
		"load, click delay:1s",
		"click[ctrlKey&&shiftKey]",
		"every 2s",
		"intersect root:#parent threshold:0.5",
		"custom-event from:body",
		"click queue:first",
		"click consume",
		"mouseenter once, keyup changed throttle:1s",
	}
	for _, v := range valid {
		if err := s.ValidateValue("hx-trigger", v); err != nil {
			t.Errorf("hx-trigger=%q should be valid: %v", v, err)
		}
	}
	invalid := []string{
		"click delya:500ms",
		"click queue:sometimes",
		"click delay:fast",
		"every fast",
	}
	for _, v := range invalid {
		if err := s.ValidateValue("hx-trigger", v); err == nil {
			t.Errorf("hx-trigger=%q should be invalid", v)
		}
	}
}

func TestValidateEnums(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	if err := s.ValidateValue("hx-boost", "true"); err != nil {
		t.Errorf("hx-boost=true: %v", err)
	}
	if err := s.ValidateValue("hx-boost", "yes"); err == nil {
		t.Error("hx-boost=yes should be invalid")
	}
	if err := s.ValidateValue("hx-encoding", "multipart/form-data"); err != nil {
		t.Errorf("hx-encoding: %v", err)
	}
	if err := s.ValidateValue("hx-encoding", "application/json"); err == nil {
		t.Error("hx-encoding=application/json should be invalid")
	}
}

func TestIntroducedRemovedMetadata(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	v, ok := s.Introduced("hx-get")
	if !ok || v != "2.0.0" {
		t.Errorf("Introduced(hx-get) = %q %v", v, ok)
	}
	if _, ok := s.Introduced("hx-fetch"); ok {
		t.Error("Introduced must not resolve unknown attributes")
	}
	if _, ok := s.Removed("hx-get"); ok {
		t.Error("hx-get is not removed")
	}
}

func TestResponseHeaders(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	def, ok := s.ResponseHeader("HX-Reswap")
	if !ok || def.Kind != KindSwap {
		t.Errorf("HX-Reswap = %+v ok=%v", def, ok)
	}
	if _, ok := s.ResponseHeader("HX-Bogus"); ok {
		t.Error("HX-Bogus must be unknown")
	}
}

func TestAttributeNamesSortedAndComplete(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	names := s.AttributeNames()
	if !slices.IsSorted(names) {
		t.Error("AttributeNames must be sorted")
	}
	// The htmx 2.0 reference lists these; spot-check presence.
	for _, want := range []string{"hx-get", "hx-post", "hx-put", "hx-patch", "hx-delete", "hx-swap", "hx-target", "hx-trigger", "hx-boost", "hx-history-elt", "hx-preserve", "hx-request", "hx-validate"} {
		if !slices.Contains(names, want) {
			t.Errorf("missing attribute %s", want)
		}
	}
}

// Regression tests for review findings.

func TestTriggerSelectorModifiersWithSpaces(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	valid := []string{
		"click from:closest form",
		"keyup target:closest div throttle:1s",
		"intersect root:#parent threshold:0.5",
		"click from:find .item once",
		"click from:next input",
		"click delay:.5s",
		"every 2s once",
	}
	for _, v := range valid {
		if err := s.ValidateValue("hx-trigger", v); err != nil {
			t.Errorf("hx-trigger=%q should be valid: %v", v, err)
		}
	}
	invalid := []string{
		"click from:closest form delya:1s",
		"every 2s queue:bogus",
	}
	for _, v := range invalid {
		if err := s.ValidateValue("hx-trigger", v); err == nil {
			t.Errorf("hx-trigger=%q should be invalid", v)
		}
	}
}

func TestSwapOOBWithModifiers(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	valid := []string{
		"outerHTML settle:20ms",
		"beforeend swap:1s:#list",
		"innerHTML transition:true:#target",
	}
	for _, v := range valid {
		if err := s.ValidateValue("hx-swap-oob", v); err != nil {
			t.Errorf("hx-swap-oob=%q should be valid: %v", v, err)
		}
	}
	if err := s.ValidateValue("hx-swap-oob", "replace settle:20ms"); err == nil {
		t.Error("hx-swap-oob with an unknown style should be invalid")
	}
}

func TestBareHxOnIsInvalid(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	for _, name := range []string{"hx-on", "hx-on:", "hx-on-"} {
		if _, ok := s.Attribute(name); ok {
			t.Errorf("%s must be invalid: htmx 2 requires an event suffix", name)
		}
	}
	if _, ok := s.Attribute("hx-online"); ok {
		t.Error("hx-online must not match the hx-on- prefix")
	}
}

func TestSyncInnerWhitespaceNormalized(t *testing.T) {
	s := mustSurface(t, "2.0.10")
	if err := s.ValidateValue("hx-sync", "this:queue  first"); err != nil {
		t.Errorf("double space inside a queue strategy should be tolerated: %v", err)
	}
}

func TestInheritKeywordVersionGating(t *testing.T) {
	old := mustSurface(t, "2.0.0")
	if err := old.ValidateValue("hx-include", "inherit"); err == nil {
		t.Error("hx-include=inherit requires htmx 2.0.5; 2.0.0 must reject it")
	}
	current := mustSurface(t, "2.0.10")
	if err := current.ValidateValue("hx-include", "inherit"); err != nil {
		t.Errorf("hx-include=inherit is valid from 2.0.5: %v", err)
	}
}

func TestSplitTopLevelUnbalanced(t *testing.T) {
	for _, in := range []string{"a[b,c", "a]b,c", "a(b,c", "a)b(,c]"} {
		_ = splitTopLevel(in, ',') // must not panic or underflow
	}
	got := splitTopLevel("click[a,b], keyup", ',')
	if len(got) != 2 {
		t.Errorf("bracketed comma must not split: %v", got)
	}
}
