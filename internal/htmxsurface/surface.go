// Package htmxsurface embeds the version-keyed htmx attribute surface set
// (DATA-006, solution design D8) and provides per-version validation of
// hx-* attribute names, values, and combinations.
//
// The embedded data is the authority for FR-024, FR-041, FR-044, FR-052,
// and FR-082. Generation performs no network I/O: the surface set ships
// inside the binary. An htmx version bump is a data update, not a compiler
// change.
package htmxsurface

import (
	"embed"
	"encoding/json"
	"fmt"
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
)

// Keyword is a value keyword with version metadata (e.g. the "inherit"
// keyword accepted by hx-include since htmx 2.0.5).
type Keyword struct {
	Value      string `json:"value"`
	Introduced string `json:"introduced,omitempty"`
	Removed    string `json:"removed,omitempty"`
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
}

// Surface is the attribute surface of one specific htmx version, resolved
// from the embedded family data by filtering introduced/removed metadata.
type Surface struct {
	version string
	fam     *family
	attrs   map[string]AttributeDef
	names   []string // sorted attribute names valid at this version
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

// ForVersion resolves the surface for the given htmx version. A version
// outside the supported range is an error naming the range
// (GHTMX-E0502 content contract).
func ForVersion(version string) (*Surface, error) {
	for _, f := range families {
		if slices.Contains(f.Versions, version) {
			return newSurface(version, f), nil
		}
	}
	supported := SupportedVersions()
	if len(supported) == 0 {
		return nil, fmt.Errorf("htmx version %q is not supported: no surface data is embedded in this build", version)
	}
	return nil, fmt.Errorf("htmx version %q is not supported by this ghtmx release; supported versions: %s to %s", version, supported[0], supported[len(supported)-1])
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
	}
	sort.Strings(s.names)
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

// AttributeNames returns the sorted names of all attributes valid at this
// version (for completion, FR-082).
func (s *Surface) AttributeNames() []string {
	out := make([]string, len(s.names))
	copy(out, s.names)
	return out
}

// Attribute looks up an attribute by name. hx-on:<event> forms (and the
// dash forms hx-on-<event> / hx-on--<htmx-event>) resolve to the hx-on
// definition; a bare "hx-on" or an empty event suffix is invalid — htmx 2
// removed the 1.x hx-on="..." attribute.
func (s *Surface) Attribute(name string) (AttributeDef, bool) {
	if def, ok := s.attrs[name]; ok {
		if def.Prefix {
			return AttributeDef{}, false
		}
		return def, true
	}
	for _, prefix := range []string{"hx-on:", "hx-on-"} {
		if suffix, ok := strings.CutPrefix(name, prefix); ok && strings.Trim(suffix, ":-") != "" {
			if def, ok := s.attrs["hx-on"]; ok {
				return def, true
			}
		}
	}
	return AttributeDef{}, false
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

// HtmxEventNames returns the kebab-case htmx event names valid in
// hx-on:: shorthand.
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

// SwapStyles returns the hx-swap styles of this surface, in data order.
// The root package's typed SwapStyle constants are asserted against this
// set in tests so they cannot drift from the pinned surface.
func (s *Surface) SwapStyles() []string {
	out := make([]string, len(s.fam.SwapStyles))
	copy(out, s.fam.SwapStyles)
	return out
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
