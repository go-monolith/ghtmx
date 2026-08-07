// Package config loads the ghtmx project configuration (ghtmx.json at the
// module root) and applies the flag > file > default precedence (FR-070,
// FR-071, FR-072, FR-073).
//
// The file is optional: a conventional project works with no configuration
// at all. An invalid key or value produces a positioned error naming the
// offending key.
package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/go-monolith/ghtmx/internal/diag"
)

// FileName is the configuration file name looked up at the module root.
const FileName = "ghtmx.json"

// DefaultHtmxVersion is the pinned htmx version used when none is
// configured. It must be one of the versions embedded in the htmx surface
// set.
const DefaultHtmxVersion = "2.0.10"

// DefaultTemplateExtension is the canonical template file extension.
const DefaultTemplateExtension = ".ghtmx"

// TemplateExtensions are the extensions a project may configure. The set is
// closed on purpose: the generator would consume its own output if it
// walked ".go", and an unrecognised extension is far more likely a typo
// than an intent, so it is worth rejecting by name.
var TemplateExtensions = []string{DefaultTemplateExtension, ".htmx"}

// GeneratedPackage identifies the central generated package that receives
// route constructors and event emitters (solution design D5).
type GeneratedPackage struct {
	// Dir is the directory of the generated package, relative to the module
	// root.
	Dir string `json:"dir"`
	// Name is the Go package name.
	Name string `json:"name"`
}

// Config is the resolved project configuration.
type Config struct {
	// HtmxVersion is the pinned htmx version driving attribute validation
	// and the script-inclusion helper.
	HtmxVersion string `json:"htmxVersion"`
	// SourceDirs are the template source directories, relative to the module
	// root.
	SourceDirs []string `json:"sourceDirs"`
	// RouteScope is the list of Go package patterns scanned by route
	// discovery.
	RouteScope []string `json:"routeScope"`
	// GeneratedPackage is where route constructors and event emitters are
	// emitted.
	GeneratedPackage GeneratedPackage `json:"generatedPackage"`
	// GeneratedSuffix is the file-name suffix replacing the template
	// extension on generated Go files. Dev-mode hot reload of text literals
	// requires the default.
	GeneratedSuffix string `json:"generatedSuffix"`
	// TemplateExtension is the file extension templates are written with.
	// A project uses exactly one: files with the other extension are not
	// templates as far as the toolchain is concerned.
	TemplateExtension string `json:"templateExtension"`
	// Checks overrides the severity of warning-class checks by stable
	// diagnostic ID: "error", "warning", or "off".
	Checks map[string]diag.Severity `json:"checks"`
	// StrictTargets promotes the dangling swap target warning
	// (GHTMX-W0201) to an error (FR-042).
	StrictTargets bool `json:"strictTargets"`
	// HtmxScript controls whether the central generated package emits the
	// HTMXScript() helper. Nil means true. False supports projects that use
	// ghtmx purely as a server-side template engine and load no htmx at
	// all; HtmxVersion keeps driving attribute validation regardless.
	HtmxScript *bool `json:"htmxScript"`
}

// EmitHtmxScript reports whether the generated central package should
// include the HTMXScript() helper: true unless htmxScript is explicitly
// false.
func (c Config) EmitHtmxScript() bool {
	return c.HtmxScript == nil || *c.HtmxScript
}

// Default returns the documented defaults (FR-072): a conventional project
// needs no configuration file.
func Default() Config {
	return Config{
		HtmxVersion:       DefaultHtmxVersion,
		SourceDirs:        []string{"."},
		RouteScope:        []string{"./..."},
		GeneratedPackage:  GeneratedPackage{Dir: "ghtmxgen", Name: "ghtmxgen"},
		GeneratedSuffix:   "_ghtmx.go",
		TemplateExtension: DefaultTemplateExtension,
		Checks:            map[string]diag.Severity{},
	}
}

// SeverityOverrides returns the effective per-check severity overrides,
// folding StrictTargets into the map.
func (c Config) SeverityOverrides() map[string]diag.Severity {
	out := make(map[string]diag.Severity, len(c.Checks)+1)
	maps.Copy(out, c.Checks)
	if c.StrictTargets {
		out[diag.DanglingTarget] = diag.Error
	}
	return out
}

// Hash returns a stable content hash of the configuration, used to salt the
// build cache key so a configuration change invalidates generated output.
// Semantically identical configs hash identically: a nil Checks map is
// canonicalized before encoding, and json.Marshal sorts map keys.
func (c Config) Hash() string {
	if c.Checks == nil {
		c.Checks = map[string]diag.Severity{}
	}
	if c.HtmxScript == nil {
		enabled := true
		c.HtmxScript = &enabled
	}
	b, err := json.Marshal(c)
	if err != nil {
		// Config contains only marshalable types; this is unreachable.
		panic(fmt.Sprintf("config hash: %v", err))
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// Load reads the configuration file from dir. A missing file is not an
// error: the defaults are returned (FR-072). An unparseable file, unknown
// key, or invalid value produces a positioned error naming the offending
// key (FR-070).
func Load(dir string) (Config, error) {
	path := filepath.Join(dir, FileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("failed to read %s: %w", path, err)
	}
	return Parse(path, data)
}

// allowedKeys is the configuration schema: top-level key to a short
// description used in error suggestions.
var allowedKeys = map[string]string{
	"htmxVersion":       "pinned htmx version, e.g. \"2.0.10\"",
	"sourceDirs":        "template source directories",
	"routeScope":        "route discovery package patterns",
	"generatedPackage":  "central generated package {dir, name}",
	"generatedSuffix":   "generated Go file suffix, default \"_ghtmx.go\"",
	"templateExtension": "template file extension, \".ghtmx\" (default) or \".htmx\"",
	"checks":            "per-check severity overrides by diagnostic ID",
	"strictTargets":     "promote dangling target warnings to errors",
	"htmxScript":        "emit the ghtmxgen.HTMXScript() helper, default true",
}

// Parse decodes configuration content, validating keys and values with
// positioned errors. An empty or whitespace-only file behaves like a
// missing file: the defaults apply.
func Parse(path string, data []byte) (Config, error) {
	// Windows editors commonly prepend a UTF-8 BOM; strip it before
	// decoding rather than failing on an invisible character.
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if len(bytes.TrimSpace(data)) == 0 {
		return Default(), nil
	}

	// First pass: token walk to find unknown or duplicate top-level keys
	// with positions.
	if err := validateKeys(path, data); err != nil {
		return Config{}, err
	}

	cfg := Default()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			line, col := offsetToLineCol(data, syn.Offset)
			return Config{}, fmt.Errorf("%s:%d:%d: invalid configuration: %v", path, line, col, syn)
		}
		var typ *json.UnmarshalTypeError
		if errors.As(err, &typ) {
			line, col := offsetToLineCol(data, typ.Offset)
			return Config{}, fmt.Errorf("%s:%d:%d: invalid value for %q: expected %s", path, line, col, typ.Field, friendlyType(typ.Type.String()))
		}
		// Nested unknown fields (e.g. inside generatedPackage) surface here
		// from DisallowUnknownFields; recover the key for positioning.
		if key, ok := unknownFieldName(err); ok {
			line, col := findKey(data, key)
			return Config{}, fmt.Errorf("%s:%d:%d: unknown configuration key %q", path, line, col, key)
		}
		return Config{}, fmt.Errorf("%s: invalid configuration: %w", path, err)
	}
	// Reject trailing content after the configuration object.
	if _, err := dec.Token(); err != io.EOF {
		off := dec.InputOffset()
		line, col := offsetToLineCol(data, off)
		return Config{}, fmt.Errorf("%s:%d:%d: unexpected content after the configuration object", path, line, col)
	}

	if err := validateValues(path, data, cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// unknownFieldName extracts the field name from an encoding/json
// DisallowUnknownFields error.
func unknownFieldName(err error) (string, bool) {
	const marker = `unknown field "`
	_, rest, found := strings.Cut(err.Error(), marker)
	if !found {
		return "", false
	}
	key, _, found := strings.Cut(rest, `"`)
	if !found {
		return "", false
	}
	return key, true
}

// friendlyType translates Go type names in decode errors into user
// vocabulary.
func friendlyType(t string) string {
	switch t {
	case "string":
		return "a string"
	case "bool":
		return "true or false"
	case "[]string":
		return "a list of strings"
	case "diag.Severity":
		return `a severity: "error", "warning", or "off"`
	case "map[string]diag.Severity":
		return "an object mapping diagnostic IDs to severities"
	case "config.GeneratedPackage":
		return "an object with dir and name"
	}
	return t
}

// validateKeys walks the top level of the JSON document and reports the
// first unknown or duplicate key with its position.
func validateKeys(path string, data []byte) error {
	seen := map[string]bool{}
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		line, col := offsetToLineCol(data, dec.InputOffset())
		return fmt.Errorf("%s:%d:%d: invalid configuration: %v", path, line, col, err)
	}
	if tok != json.Delim('{') {
		line, col := offsetToLineCol(data, 0)
		return fmt.Errorf("%s:%d:%d: configuration must be a JSON object", path, line, col)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			line, col := offsetToLineCol(data, dec.InputOffset())
			return fmt.Errorf("%s:%d:%d: invalid configuration: %v", path, line, col, err)
		}
		key, ok := tok.(string)
		if !ok {
			continue
		}
		// InputOffset is at the end of the key token; step back over the
		// quoted key to point at its start.
		keyOffset := max(dec.InputOffset()-int64(len(fmt.Sprintf("%q", key))), 0)
		if _, known := allowedKeys[key]; !known {
			line, col := offsetToLineCol(data, keyOffset)
			return fmt.Errorf("%s:%d:%d: unknown configuration key %q (known keys: %s)", path, line, col, key, strings.Join(sortedKeys(), ", "))
		}
		if seen[key] {
			line, col := offsetToLineCol(data, keyOffset)
			return fmt.Errorf("%s:%d:%d: duplicate configuration key %q", path, line, col, key)
		}
		seen[key] = true
		// Skip the value.
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			line, col := offsetToLineCol(data, dec.InputOffset())
			return fmt.Errorf("%s:%d:%d: invalid value for %q: %v", path, line, col, key, err)
		}
	}
	return nil
}

// validateValues checks semantic constraints on decoded values.
func validateValues(path string, data []byte, cfg Config) error {
	for id, sev := range cfg.Checks {
		check, ok := diag.Registry[id]
		if !ok {
			line, col := findKey(data, id)
			return fmt.Errorf("%s:%d:%d: unknown diagnostic ID %q in checks", path, line, col, id)
		}
		if sev != diag.Error && sev != diag.Warning && sev != diag.Off {
			line, col := findKey(data, id)
			return fmt.Errorf("%s:%d:%d: invalid severity %q for %s: use \"error\", \"warning\", or \"off\"", path, line, col, sev, id)
		}
		if check.DefaultSeverity == diag.Error && sev != diag.Error {
			line, col := findKey(data, id)
			return fmt.Errorf("%s:%d:%d: %s is an error-level check and cannot be demoted to %q", path, line, col, id, sev)
		}
	}
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return nil
}

// Validate checks semantic constraints on a resolved configuration. It runs
// after Parse and MUST also run after Resolve: flag precedence does not
// bypass validation (FR-073).
func (c Config) Validate() error {
	if c.HtmxVersion == "" {
		return errors.New("htmxVersion must not be empty")
	}
	if c.GeneratedPackage.Dir == "" || c.GeneratedPackage.Name == "" {
		return errors.New("generatedPackage requires both dir and name")
	}
	if len(c.SourceDirs) == 0 {
		return errors.New("sourceDirs must not be empty; omit the key to use the default")
	}
	if len(c.RouteScope) == 0 {
		return errors.New("routeScope must not be empty; omit the key to use the default")
	}
	if !strings.HasSuffix(c.GeneratedSuffix, ".go") || c.GeneratedSuffix == ".go" || !strings.HasPrefix(c.GeneratedSuffix, "_") {
		return fmt.Errorf("generatedSuffix %q must start with _ and end with .go, e.g. \"_ghtmx.go\"", c.GeneratedSuffix)
	}
	if !slices.Contains(TemplateExtensions, c.TemplateExtension) {
		quoted := make([]string, len(TemplateExtensions))
		for i, ext := range TemplateExtensions {
			quoted[i] = strconv.Quote(ext)
		}
		return fmt.Errorf("templateExtension %q must be one of %s", c.TemplateExtension, strings.Join(quoted, " or "))
	}
	for id, sev := range c.Checks {
		check, ok := diag.Registry[id]
		if !ok {
			return fmt.Errorf("unknown diagnostic ID %q in checks", id)
		}
		if sev != diag.Error && sev != diag.Warning && sev != diag.Off {
			return fmt.Errorf(`invalid severity %q for %s: use "error", "warning", or "off"`, sev, id)
		}
		if check.DefaultSeverity == diag.Error && sev != diag.Error {
			return fmt.Errorf("%s is an error-level check and cannot be demoted to %q", id, sev)
		}
	}
	return nil
}

func sortedKeys() []string {
	keys := make([]string, 0, len(allowedKeys))
	for k := range allowedKeys {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// findKey locates the first occurrence of a quoted key in the document for
// error positioning. Best-effort: falls back to 1:1.
func findKey(data []byte, key string) (line, col int) {
	idx := strings.Index(string(data), fmt.Sprintf("%q", key))
	if idx < 0 {
		return 1, 1
	}
	return offsetToLineCol(data, int64(idx))
}

func offsetToLineCol(data []byte, offset int64) (line, col int) {
	if offset > int64(len(data)) {
		offset = int64(len(data))
	}
	line, col = 1, 1
	for _, b := range data[:offset] {
		if b == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}

// Flags holds CLI flag values that override file configuration (FR-073).
// A nil pointer means the flag was not set.
type Flags struct {
	HtmxVersion       *string
	SourceDirs        []string
	RouteScope        []string
	GeneratedPkgDir   *string
	GeneratedPkgName  *string
	GeneratedSuffix   *string
	TemplateExtension *string
	StrictTargets     *bool
	HtmxScript        *bool
	CheckSeverities   map[string]diag.Severity
}

// Resolve applies precedence flag > file > default and returns the
// effective configuration.
func Resolve(fileCfg Config, flags Flags) Config {
	cfg := fileCfg
	if flags.HtmxVersion != nil {
		cfg.HtmxVersion = *flags.HtmxVersion
	}
	if len(flags.SourceDirs) > 0 {
		cfg.SourceDirs = flags.SourceDirs
	}
	if len(flags.RouteScope) > 0 {
		cfg.RouteScope = flags.RouteScope
	}
	if flags.GeneratedPkgDir != nil {
		cfg.GeneratedPackage.Dir = *flags.GeneratedPkgDir
	}
	if flags.GeneratedPkgName != nil {
		cfg.GeneratedPackage.Name = *flags.GeneratedPkgName
	}
	if flags.GeneratedSuffix != nil {
		cfg.GeneratedSuffix = *flags.GeneratedSuffix
	}
	if flags.TemplateExtension != nil {
		cfg.TemplateExtension = *flags.TemplateExtension
	}
	if flags.StrictTargets != nil {
		cfg.StrictTargets = *flags.StrictTargets
	}
	if flags.HtmxScript != nil {
		v := *flags.HtmxScript
		cfg.HtmxScript = &v
	}
	if len(flags.CheckSeverities) > 0 {
		if cfg.Checks == nil {
			cfg.Checks = map[string]diag.Severity{}
		}
		maps.Copy(cfg.Checks, flags.CheckSeverities)
	}
	return cfg
}
