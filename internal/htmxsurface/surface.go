// Package htmxsurface embeds the version-keyed htmx attribute surface set
// (DATA-006, solution design D8) and provides per-version validation of
// hx-* attribute names, values, and combinations.
//
// The embedded data is the authority for FR-024, FR-041, FR-044, FR-052,
// and FR-082. Generation performs no network I/O: the surface set ships
// inside the binary. An htmx version bump is a data update, not a compiler
// change: each major line is one family file under data/, and every
// behaviour that differs between families (attribute-name modifiers,
// listener prefixes, migration hints) is keyed off fields that are absent
// from the families that do not have it.
package htmxsurface

import (
	"embed"
	"encoding/json"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"sort"
	"strings"

	"golang.org/x/mod/semver"
)

//go:embed data/*.json
var dataFS embed.FS

// Kind classifies an attribute's value grammar.
type Kind string

const (
	KindURL              Kind = "url"
	KindOn               Kind = "on"
	KindBoolOrURL        Kind = "bool-or-url"
	KindSelector         Kind = "selector"
	KindSelectorList     Kind = "selector-list"
	KindSwap             Kind = "swap"
	KindSwapOOB          Kind = "swap-oob"
	KindExtendedSelector Kind = "extended-selector"
	KindTrigger          Kind = "trigger"
	KindJSON             Kind = "json"
	KindEnum             Kind = "enum"
	KindString           Kind = "string"
	KindFlag             Kind = "flag"
	KindAttrList         Kind = "attr-list"
	KindExtList          Kind = "ext-list"
	KindParams           Kind = "params"
	KindRequest          Kind = "request"
	KindSync             Kind = "sync"
	KindTriggerEvents    Kind = "trigger-events"
	KindURLOrJSON        Kind = "url-or-json"
	// KindStatusConfig is the hx-status:<code> value grammar of htmx 4:
	// space-separated key:value pairs configuring the swap for one status.
	KindStatusConfig Kind = "status-config"
)

// Keyword is a value keyword with version metadata (e.g. the "inherit"
// keyword accepted by hx-include since htmx 2.0.5, and gone in 4.0).
type Keyword struct {
	Value      string `json:"value"`
	Introduced string `json:"introduced,omitempty"`
	Removed    string `json:"removed,omitempty"`
	// Hint is the migration advice shown when the keyword was removed.
	Hint string `json:"hint,omitempty"`
}

// AttributeDef describes one hx-* attribute for a version family.
type AttributeDef struct {
	Name          string    `json:"-"`
	Kind          Kind      `json:"kind"`
	Verb          string    `json:"verb,omitempty"`
	Values        []string  `json:"values,omitempty"`
	Inherited     bool      `json:"inherited,omitempty"`
	Deprecated    string    `json:"deprecated,omitempty"`
	Introduced    string    `json:"introduced,omitempty"`
	Removed       string    `json:"removed,omitempty"`
	Prefix        bool      `json:"prefix,omitempty"`
	ExtraKeywords []Keyword `json:"extraKeywords,omitempty"`
	// Hint is migration advice for an attribute the family removed or
	// whose meaning changed (shown alongside the diagnostic).
	Hint string `json:"hint,omitempty"`
	// RenamedTo names the attribute that replaced a removed one.
	RenamedTo string `json:"renamedTo,omitempty"`
	// SuffixPattern is a regular expression the text after "name:" must
	// match (hx-status:422, hx-live:disabled). With Prefix set the bare
	// name is invalid; without it both the bare and the suffixed forms are.
	SuffixPattern string `json:"suffixPattern,omitempty"`
	// IssuesRequest marks attributes whose presence means the element
	// itself issues requests (verbs, hx-action, hx-boost, hx-ws:send…), so
	// its own inheritable attributes are not inheritance.
	IssuesRequest bool `json:"issuesRequest,omitempty"`
	// RequiresValue rejects the bare boolean form of an attribute whose
	// value is required (htmx 4 hx-disable, which was a flag in htmx 2).
	RequiresValue bool `json:"requiresValue,omitempty"`
	// Extension names the shipped extension that defines the attribute,
	// when it is not core htmx.
	Extension string `json:"extension,omitempty"`

	suffixRE *regexp.Regexp
}

// Conflict is a mutually incompatible attribute combination (FR-044).
type Conflict struct {
	Attrs     []string `json:"attrs"`
	AtMostOne bool     `json:"atMostOne,omitempty"`
	Message   string   `json:"message"`
}

// HeaderDef describes an HX-* response header's value grammar.
type HeaderDef struct {
	Kind   Kind     `json:"kind"`
	Values []string `json:"values,omitempty"`
}

type family struct {
	Family               string                  `json:"family"`
	Versions             []string                `json:"versions"`
	Attributes           map[string]AttributeDef `json:"attributes"`
	SwapStyles           []string                `json:"swapStyles"`
	SwapModifiers        map[string]string       `json:"swapModifiers"`
	TriggerModifiers     []string                `json:"triggerModifiers"`
	TriggerQueueValues   []string                `json:"triggerQueueValues"`
	TriggerSpecialEvents []string                `json:"triggerSpecialEvents"`
	SyncStrategies       []string                `json:"syncStrategies"`
	Conflicts            []Conflict              `json:"conflicts"`
	RequestHeaders       []string                `json:"requestHeaders"`
	ResponseHeaders      map[string]HeaderDef    `json:"responseHeaders"`
	HtmxEvents           []string                `json:"htmxEvents"`
	// ExtensionAttributePrefixes are attribute-name prefixes claimed by
	// known htmx extensions (e.g. "hx-target-" from response-targets).
	// Names matching a prefix are accepted with presence-only values.
	ExtensionAttributePrefixes []string `json:"extensionAttributePrefixes"`

	// The fields below are absent from families that predate the
	// construct they describe; their zero values keep those families'
	// behaviour unchanged.

	// NameModifiers are the attribute-name modifiers the family accepts
	// after a colon (htmx 4: "inherited", "append").
	NameModifiers []string `json:"nameModifiers"`
	// OnEventPrefixes are the hx-on listener prefixes; empty means the
	// htmx 2 pair (hx-on: and the hx-on- dash form).
	OnEventPrefixes []string `json:"onEventPrefixes"`
	// SwapStyleAliases maps alias swap styles to their canonical style.
	SwapStyleAliases map[string]string `json:"swapStyleAliases"`
	// ExtensionSwapStyles are swap styles shipped extensions add.
	ExtensionSwapStyles []string `json:"extensionSwapStyles"`
	// SwapModifierRenames maps a previous family's modifier names to the
	// current spelling, for migration hints.
	SwapModifierRenames map[string]string `json:"swapModifierRenames"`
	// TriggerModifierRemoved maps removed trigger modifiers to advice.
	TriggerModifierRemoved map[string]string `json:"triggerModifierRemoved"`
	// RemovedAttributePrefixes maps attribute-name prefixes a previous
	// family accepted (extension prefixes) to migration advice.
	RemovedAttributePrefixes map[string]string `json:"removedAttributePrefixes"`
	// EventRenames maps a previous family's htmx event names (as written
	// in hx-on:: and hx-trigger) to the current names.
	EventRenames map[string]string `json:"eventRenames"`
	// RemovedEvents maps a previous family's htmx event names that have
	// no replacement to advice.
	RemovedEvents map[string]string `json:"removedEvents"`
}

var legacyOnPrefixes = []string{"hx-on:", "hx-on-"}

func (f *family) onPrefixes() []string {
	if len(f.OnEventPrefixes) == 0 {
		return legacyOnPrefixes
	}
	return f.OnEventPrefixes
}

// Surface is the attribute surface of one specific htmx version, resolved
// from the embedded family data by filtering introduced/removed metadata.
type Surface struct {
	version  string
	fam      *family
	attrs    map[string]AttributeDef
	names    []string       // sorted attribute names valid at this version
	suffixed []AttributeDef // attributes taking a name:suffix form, by name
}

var families = loadFamilies()

func loadFamilies() []*family {
	entries, err := dataFS.ReadDir("data")
	if err != nil {
		panic(fmt.Sprintf("htmxsurface: embedded data unreadable: %v", err))
	}
	var fams []*family
	for _, e := range entries {
		data, err := dataFS.ReadFile("data/" + e.Name())
		if err != nil {
			panic(fmt.Sprintf("htmxsurface: %v", err))
		}
		f := new(family)
		if err := json.Unmarshal(data, f); err != nil {
			panic(fmt.Sprintf("htmxsurface: invalid embedded surface %s: %v", e.Name(), err))
		}
		for name, def := range f.Attributes {
			if def.SuffixPattern == "" {
				continue
			}
			re, err := regexp.Compile(def.SuffixPattern)
			if err != nil {
				panic(fmt.Sprintf("htmxsurface: %s: attribute %s has an invalid suffixPattern: %v", e.Name(), name, err))
			}
			def.suffixRE = re
			f.Attributes[name] = def
		}
		fams = append(fams, f)
	}
	sort.Slice(fams, func(i, j int) bool { return fams[i].Family < fams[j].Family })
	return fams
}

// SupportedVersions returns every htmx version the embedded set covers, in
// ascending order.
func SupportedVersions() []string {
	var out []string
	for _, f := range families {
		out = append(out, f.Versions...)
	}
	sort.Slice(out, func(i, j int) bool { return semver.Compare("v"+out[i], "v"+out[j]) < 0 })
	return out
}

// FamilyInfo describes one embedded version family.
type FamilyInfo struct {
	// Name is the family label, e.g. "2.0" or "4.0".
	Name string
	// Versions are the family's versions in ascending order.
	Versions []string
}

// Families returns the embedded version families in ascending order.
func Families() []FamilyInfo {
	out := make([]FamilyInfo, 0, len(families))
	for _, f := range families {
		versions := slices.Clone(f.Versions)
		sort.Slice(versions, func(i, j int) bool { return semver.Compare("v"+versions[i], "v"+versions[j]) < 0 })
		out = append(out, FamilyInfo{Name: f.Family, Versions: versions})
	}
	return out
}

// SupportedRanges renders each family's version range for messages and
// documentation: "2.0.0 – 2.0.10" for a multi-version family, the bare
// version for a single-version one.
func SupportedRanges() []string {
	var out []string
	for _, f := range Families() {
		if len(f.Versions) == 0 {
			continue
		}
		if len(f.Versions) == 1 {
			out = append(out, f.Versions[0])
			continue
		}
		out = append(out, f.Versions[0]+" – "+f.Versions[len(f.Versions)-1])
	}
	return out
}

// ForVersion resolves the surface for the given htmx version. A version
// outside the supported range is an error naming the range
// (GHTMX-E0502 content contract).
func ForVersion(version string) (*Surface, error) {
	for _, f := range families {
		if slices.Contains(f.Versions, version) {
			return newSurface(version, f), nil
		}
	}
	ranges := SupportedRanges()
	if len(ranges) == 0 {
		return nil, fmt.Errorf("htmx version %q is not supported: no surface data is embedded in this build", version)
	}
	return nil, fmt.Errorf("htmx version %q is not supported by this ghtmx release; supported versions: %s", version, strings.Join(ranges, ", "))
}

func newSurface(version string, f *family) *Surface {
	s := &Surface{version: version, fam: f, attrs: make(map[string]AttributeDef, len(f.Attributes))}
	for name, def := range f.Attributes {
		def.Name = name
		if !activeAt(version, def.Introduced, def.Removed) {
			continue
		}
		s.attrs[name] = def
		s.names = append(s.names, name)
		if def.suffixRE != nil {
			s.suffixed = append(s.suffixed, def)
		}
	}
	sort.Strings(s.names)
	sort.Slice(s.suffixed, func(i, j int) bool { return s.suffixed[i].Name < s.suffixed[j].Name })
	return s
}

func activeAt(version, introduced, removed string) bool {
	if introduced != "" && semver.Compare("v"+version, "v"+introduced) < 0 {
		return false
	}
	if removed != "" && semver.Compare("v"+version, "v"+removed) >= 0 {
		return false
	}
	return true
}

// Version returns the resolved htmx version.
func (s *Surface) Version() string { return s.version }

// Family returns the label of the version family ("2.0", "4.0").
func (s *Surface) Family() string { return s.fam.Family }

// AttributeNames returns the sorted names of all attributes valid at this
// version (for completion, FR-082).
func (s *Surface) AttributeNames() []string {
	out := make([]string, len(s.names))
	copy(out, s.names)
	return out
}

// ParsedName is an attribute name resolved to its definition.
type ParsedName struct {
	// Base is the defined attribute name ("hx-swap", "hx-on", "hx-status",
	// "hx-sse:connect").
	Base string
	// Suffix is the text after the base for suffixed attributes: the event
	// of hx-on (":after:swap", "click"), the code of hx-status ("422"), the
	// bound attribute of hx-live ("disabled").
	Suffix string
	// Inherited and Append report the htmx 4 :inherited / :append
	// attribute-name modifiers.
	Inherited bool
	Append    bool
}

// NameReason classifies why an attribute name did not resolve.
type NameReason int

const (
	// NameUnknown: no definition matches the name.
	NameUnknown NameReason = iota
	// NameBarePrefix: a suffixed attribute (hx-on, hx-status) used without
	// its suffix.
	NameBarePrefix
	// NameBadSuffix: the suffix does not match the attribute's grammar
	// (hx-status:42).
	NameBadSuffix
	// NameModifiersUnsupported: the name carries an attribute-name
	// modifier (:inherited, :append) the configured family does not have.
	NameModifiersUnsupported
	// NameUnknownModifier: an attribute-name modifier the family does not
	// define.
	NameUnknownModifier
	// NameModifierNotInheritable: :inherited or :append on an attribute
	// that does not inherit.
	NameModifierNotInheritable
	// NameDuplicateModifier: the same modifier twice.
	NameDuplicateModifier
)

// NameError explains why ParseName rejected a name.
type NameError struct {
	Reason NameReason
	// Base is the defined attribute the name was resolved against, or the
	// whole name when nothing matched.
	Base string
	// Modifier is the offending modifier or suffix, when there is one.
	Modifier string
}

func (e *NameError) Error() string {
	switch e.Reason {
	case NameBarePrefix:
		return e.Base + " requires a suffix"
	case NameBadSuffix:
		return fmt.Sprintf("%s:%s has an invalid suffix", e.Base, e.Modifier)
	case NameModifiersUnsupported:
		return fmt.Sprintf("attribute-name modifier :%s on %s is not available in this htmx version", e.Modifier, e.Base)
	case NameUnknownModifier:
		return fmt.Sprintf("unknown attribute-name modifier :%s on %s", e.Modifier, e.Base)
	case NameModifierNotInheritable:
		return fmt.Sprintf("%s does not inherit; :%s applies only to inheritable attributes", e.Base, e.Modifier)
	case NameDuplicateModifier:
		return fmt.Sprintf("duplicate attribute-name modifier :%s on %s", e.Modifier, e.Base)
	}
	return "unknown attribute " + e.Base
}

// modifierWords are the attribute-name modifiers any family may define;
// used to tell "modifier the family lacks" from "unknown attribute".
var modifierWords = []string{"inherited", "append"}

// ParseName resolves an attribute name to its definition: an exact name,
// an hx-on listener, a suffixed attribute (hx-status:422, hx-live:disabled),
// or a name with attribute-name modifiers (hx-target:inherited,
// hx-vals:inherited:append). The hot path — an exact name — allocates
// nothing.
func (s *Surface) ParseName(name string) (ParsedName, *NameError) {
	if def, ok := s.attrs[name]; ok {
		if def.Prefix {
			return ParsedName{}, &NameError{Reason: NameBarePrefix, Base: name}
		}
		return ParsedName{Base: name}, nil
	}
	if _, ok := s.attrs["hx-on"]; ok {
		for _, prefix := range s.fam.onPrefixes() {
			suffix, cut := strings.CutPrefix(name, prefix)
			if !cut {
				continue
			}
			if strings.Trim(suffix, ":-") == "" {
				return ParsedName{}, &NameError{Reason: NameBarePrefix, Base: "hx-on"}
			}
			return ParsedName{Base: "hx-on", Suffix: suffix}, nil
		}
	}
	for _, def := range s.suffixed {
		rest, cut := strings.CutPrefix(name, def.Name+":")
		if !cut {
			continue
		}
		if def.suffixRE.MatchString(rest) {
			return ParsedName{Base: def.Name, Suffix: rest}, nil
		}
		return ParsedName{}, &NameError{Reason: NameBadSuffix, Base: def.Name, Modifier: rest}
	}
	base, rest, hasColon := strings.Cut(name, ":")
	if !hasColon {
		return ParsedName{}, &NameError{Reason: NameUnknown, Base: name}
	}
	def, known := s.attrs[base]
	if !known || def.Prefix {
		return ParsedName{}, &NameError{Reason: NameUnknown, Base: name}
	}
	mods := strings.Split(rest, ":")
	if len(s.fam.NameModifiers) == 0 {
		for _, mod := range mods {
			if !slices.Contains(modifierWords, mod) {
				return ParsedName{}, &NameError{Reason: NameUnknown, Base: name}
			}
		}
		return ParsedName{}, &NameError{Reason: NameModifiersUnsupported, Base: base, Modifier: mods[0]}
	}
	p := ParsedName{Base: base}
	for _, mod := range mods {
		if !slices.Contains(s.fam.NameModifiers, mod) {
			return ParsedName{}, &NameError{Reason: NameUnknownModifier, Base: base, Modifier: mod}
		}
		if !def.Inherited {
			return ParsedName{}, &NameError{Reason: NameModifierNotInheritable, Base: base, Modifier: mod}
		}
		switch mod {
		case "inherited":
			if p.Inherited {
				return ParsedName{}, &NameError{Reason: NameDuplicateModifier, Base: base, Modifier: mod}
			}
			p.Inherited = true
		case "append":
			if p.Append {
				return ParsedName{}, &NameError{Reason: NameDuplicateModifier, Base: base, Modifier: mod}
			}
			p.Append = true
		}
	}
	return p, nil
}

// Attribute looks up an attribute by name, resolving hx-on:<event> forms,
// suffixed attributes, and attribute-name modifiers to their definition. A
// bare "hx-on" or an empty event suffix is invalid — htmx 2 removed the
// 1.x hx-on="..." attribute.
func (s *Surface) Attribute(name string) (AttributeDef, bool) {
	p, err := s.ParseName(name)
	if err != nil {
		return AttributeDef{}, false
	}
	return s.attrs[p.Base], true
}

// Definition returns the definition registered under exactly name, with
// no prefix, suffix, or modifier resolution — for tooling that iterates
// AttributeNames and needs each entry's metadata (prefix, extension).
func (s *Surface) Definition(name string) (AttributeDef, bool) {
	def, ok := s.attrs[name]
	return def, ok
}

// HasNameModifiers reports whether the family accepts attribute-name
// modifiers (:inherited, :append), which is also the signal that attribute
// inheritance is explicit.
func (s *Surface) HasNameModifiers() bool { return len(s.fam.NameModifiers) > 0 }

// NameModifiers returns the attribute-name modifiers of the family.
func (s *Surface) NameModifiers() []string { return slices.Clone(s.fam.NameModifiers) }

// AcceptsOnPrefix reports whether the family spells hx-on listeners with
// the given prefix ("hx-on:" always; "hx-on-" only in htmx 2).
func (s *Surface) AcceptsOnPrefix(prefix string) bool {
	return slices.Contains(s.fam.onPrefixes(), prefix)
}

// KnownExtension reports whether the attribute name is claimed by a known
// htmx extension. Extension attributes pass name validation; their values
// are not constrained by the core surface.
func (s *Surface) KnownExtension(name string) bool {
	for _, prefix := range s.fam.ExtensionAttributePrefixes {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			return true
		}
	}
	return false
}

// Migration describes what became of an attribute this version removed.
type Migration struct {
	// RemovedIn is the version that removed the attribute.
	RemovedIn string
	// RenamedTo is the replacement attribute, when there is a direct one.
	RenamedTo string
	// Hint is free-form migration advice.
	Hint string
}

// Migration reports the migration record of an attribute the family knows
// but this version has removed. ok is false for attributes that are
// valid, unknown, or merely not yet introduced.
func (s *Surface) Migration(name string) (Migration, bool) {
	def, ok := s.fam.Attributes[name]
	if !ok || def.Removed == "" || semver.Compare("v"+s.version, "v"+def.Removed) < 0 {
		return Migration{}, false
	}
	return Migration{RemovedIn: def.Removed, RenamedTo: def.RenamedTo, Hint: def.Hint}, true
}

// RemovedPrefixHint reports migration advice for an attribute name a
// previous family accepted through an extension prefix (hx-target-404).
func (s *Surface) RemovedPrefixHint(name string) (string, bool) {
	for prefix, hint := range s.fam.RemovedAttributePrefixes {
		if strings.HasPrefix(name, prefix) && len(name) > len(prefix) {
			return hint, true
		}
	}
	return "", false
}

// EventRename reports the current name of an htmx event a previous family
// spelled differently (after-swap → after:swap).
func (s *Surface) EventRename(event string) (string, bool) {
	current, ok := s.fam.EventRenames[event]
	return current, ok
}

// RemovedEvent reports advice for an htmx event a previous family had and
// this one dropped.
func (s *Surface) RemovedEvent(event string) (string, bool) {
	hint, ok := s.fam.RemovedEvents[event]
	return hint, ok
}

// IssuesRequest reports whether the named attribute (by base name) makes
// its element issue requests.
func (s *Surface) IssuesRequest(base string) bool {
	def, ok := s.attrs[base]
	return ok && def.IssuesRequest
}

// Introduced returns the version that introduced the named attribute, where
// the embedded metadata records one. ok is false when the attribute is
// unknown to the family entirely.
func (s *Surface) Introduced(name string) (version string, ok bool) {
	def, ok := s.fam.Attributes[name]
	if !ok {
		return "", false
	}
	if def.Introduced == "" {
		return s.fam.Versions[0], true
	}
	return def.Introduced, true
}

// Removed returns the version that removed the named attribute, if any.
func (s *Surface) Removed(name string) (version string, ok bool) {
	def, ok := s.fam.Attributes[name]
	if !ok || def.Removed == "" {
		return "", false
	}
	return def.Removed, true
}

// ValidateCombination checks the attribute names present on one element
// against the conflict rules (FR-044) and returns every violated rule.
func (s *Surface) ValidateCombination(attrs []string) []Conflict {
	present := make(map[string]bool, len(attrs))
	for _, a := range attrs {
		present[a] = true
	}
	var out []Conflict
	for _, c := range s.fam.Conflicts {
		n := 0
		for _, a := range c.Attrs {
			if present[a] {
				n++
			}
		}
		if (c.AtMostOne && n > 1) || (!c.AtMostOne && n == len(c.Attrs)) {
			out = append(out, c)
		}
	}
	return out
}

// ResponseHeader looks up an HX-* response header definition.
func (s *Surface) ResponseHeader(name string) (HeaderDef, bool) {
	def, ok := s.fam.ResponseHeaders[name]
	return def, ok
}

// HtmxEventNames returns the htmx event names valid in hx-on:: shorthand:
// kebab-case in htmx 2, colon-separated (after:swap) in htmx 4.
func (s *Surface) HtmxEventNames() []string {
	out := make([]string, len(s.fam.HtmxEvents))
	copy(out, s.fam.HtmxEvents)
	return out
}

// Suggest returns up to three known attribute names closest to the unknown
// name by edit distance, for did-you-mean diagnostics (FR-024).
func (s *Surface) Suggest(name string) []string {
	type cand struct {
		name string
		dist int
	}
	var cands []cand
	for _, n := range s.names {
		d := editDistance(name, n)
		if d <= 3 {
			cands = append(cands, cand{n, d})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].dist != cands[j].dist {
			return cands[i].dist < cands[j].dist
		}
		return cands[i].name < cands[j].name
	})
	var out []string
	for i, c := range cands {
		if i == 3 {
			break
		}
		out = append(out, c.name)
	}
	return out
}

func editDistance(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	curr := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		curr[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j]+1, curr[j-1]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(rb)]
}

// SwapStyles returns the core hx-swap styles of this surface, in data
// order. The root package's typed SwapStyle constants are asserted against
// this set in tests so they cannot drift from the pinned surface.
func (s *Surface) SwapStyles() []string {
	out := make([]string, len(s.fam.SwapStyles))
	copy(out, s.fam.SwapStyles)
	return out
}

// SwapStyleAliases returns the alias swap styles of this surface mapped to
// their canonical style (htmx 4: before → beforebegin, …).
func (s *Surface) SwapStyleAliases() map[string]string {
	return maps.Clone(s.fam.SwapStyleAliases)
}

// ExtensionSwapStyles returns the swap styles shipped extensions add.
func (s *Surface) ExtensionSwapStyles() []string {
	return slices.Clone(s.fam.ExtensionSwapStyles)
}

// isSwapStyle reports whether style is a core style, an alias, or an
// extension style of this surface.
func (s *Surface) isSwapStyle(style string) bool {
	if slices.Contains(s.fam.SwapStyles, style) || slices.Contains(s.fam.ExtensionSwapStyles, style) {
		return true
	}
	_, alias := s.fam.SwapStyleAliases[style]
	return alias
}

// SwapModifierNames returns the hx-swap modifier names of this surface,
// sorted. The root package's typed modifier constructors are asserted
// against this set in tests.
func (s *Surface) SwapModifierNames() []string {
	out := make([]string, 0, len(s.fam.SwapModifiers))
	for name := range s.fam.SwapModifiers {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
