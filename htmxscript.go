package ghtmx

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
)

// htmxIntegrity pins the subresource-integrity hash of every supported
// htmx version's dist/htmx.min.js (sha384 over the published npm asset).
// The browser refuses an asset that does not hash to the pinned value, so
// a served file that does not match the configured version is blocked and
// reported by the user agent — the FR-052 mismatch guarantee, enforced at
// the strongest possible point.
var htmxIntegrity = map[string]string{
	"2.0.0":  "sha384-wS5l5IKJBvK6sPTKa2WZ1js3d947pvWXbPJ1OmWfEuxLgeHcEbjUUA5i9V5ZkpCw",
	"2.0.1":  "sha384-QWGpdj554B4ETpJJC9z+ZHJcA/i59TyjxEPXiiUgN2WmTyV5OEZWCD6gQhgkdpB/",
	"2.0.2":  "sha384-Y7hw+L/jvKeWIRRkqWYfPcvVxHzVzn5REgzbawhxAuQGwX1XWe70vji+VSeHOThJ",
	"2.0.3":  "sha384-0895/pl2MU10Hqc6jd4RvrthNlDiE9U1tWmX7WRESftEDRosgxNsQG/Ze9YMRzHq",
	"2.0.4":  "sha384-HGfztofotfshcF7+8n44JQL2oJmowVChPTg48S+jvZoztPfvwD79OC/LTtG6dMp+",
	"2.0.5":  "sha384-t4DxZSyQK+0Uv4jzy5B0QyHyWQD2GFURUmxKMBVww9+e2EJ0ei/vCvv7+79z0fkr",
	"2.0.6":  "sha384-Akqfrbj/HpNVo8k11SXBb6TlBWmXXlYQrCSqEWmyKJe+hDm3Z/B2WVG4smwBkRVm",
	"2.0.7":  "sha384-ZBXiYtYQ6hJ2Y0ZNoYuI+Nq5MqWBr+chMrS/RkXpNzQCApHEhOt2aY8EJgqwHLkJ",
	"2.0.8":  "sha384-/TgkGk7p307TH7EXJDuUlgG3Ce1UVolAOFopFekQkkXihi5u/6OCvVKyz1W+idaz",
	"2.0.9":  "sha384-ESlCao+z/oasnu2Uc/5K1LQTI7YCF2KKO4xakCPQCFuiHhCh8Oa/R5NwHY6guZ3m",
	"2.0.10": "sha384-H5SrcfygHmAuTDZphMHqBJLc3FhssKjG7w/CeCpFReSfwBWDTKpkzPP8c+cLsK+V",
	"4.0.0":  "sha384-BvJpBiO8Kh31EqtJe5DRIeWrHWnCGkwytKs9NKFi86Hhw96dEqdEMzZDeK9iEGTc",
}

// SupportedHtmxVersions returns the htmx versions the script helper can
// emit, in numeric version order.
func SupportedHtmxVersions() []string {
	out := make([]string, 0, len(htmxIntegrity))
	for v := range htmxIntegrity {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return versionLess(out[i], out[j]) })
	return out
}

// HTMXScriptIntegrity returns the pinned subresource-integrity hash for
// version, and whether the version has one. It lets a project that serves
// htmx itself (WithScriptSrc) assert in a test that the vendored file is
// the exact published build the tag pins, without scraping rendered HTML.
func HTMXScriptIntegrity(version string) (string, bool) {
	integrity, ok := htmxIntegrity[version]
	return integrity, ok
}

// versionLess compares dotted versions numerically segment by segment.
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) && i < len(bs); i++ {
		an, aerr := strconv.Atoi(as[i])
		bn, berr := strconv.Atoi(bs[i])
		if aerr != nil || berr != nil {
			if as[i] != bs[i] {
				return as[i] < bs[i]
			}
			continue
		}
		if an != bn {
			return an < bn
		}
	}
	return len(as) < len(bs)
}

// ScriptOption configures HTMXScriptTag.
type ScriptOption func(*scriptConfig)

type scriptConfig struct {
	src string
}

// WithScriptSrc serves htmx from the given URL (self-hosted or another
// CDN) instead of jsDelivr. The integrity attribute still pins the
// configured version's official asset, so a self-hosted file must be that
// exact published build.
func WithScriptSrc(url string) ScriptOption {
	return func(c *scriptConfig) { c.src = url }
}

// HTMXScriptTag returns a component rendering the script tag for the
// given htmx version, with its subresource-integrity hash (FR-091).
// Rendering fails for a version outside the supported surface set — use
// the generated ghtmxgen.HTMXScript(), which bakes the configured version
// so tag and configuration cannot diverge.
func HTMXScriptTag(version string, opts ...ScriptOption) Component {
	var cfg scriptConfig
	for _, opt := range opts {
		opt(&cfg)
	}
	return ComponentFunc(func(ctx context.Context, w io.Writer) error {
		integrity, ok := htmxIntegrity[version]
		if !ok {
			return fmt.Errorf("GHTMX-E0502: htmx version %q has no pinned script asset; supported versions: %v", version, SupportedHtmxVersions())
		}
		src := cfg.src
		if src == "" {
			src = "https://cdn.jsdelivr.net/npm/htmx.org@" + version + "/dist/htmx.min.js"
		}
		_, err := io.WriteString(w,
			`<script src="`+EscapeString(src)+`" integrity="`+integrity+`" crossorigin="anonymous"></script>`)
		return err
	})
}
