// Package editors holds the editor extensions and the validation tests
// that keep their artifacts consistent: the extensions themselves are
// deliberately outside the Go build (packaging them needs Node or a
// JDK), so these tests pin what the standard toolchain can check —
// artifact structure, grammar coverage of the ghtmx-native constructs,
// grammar synchronization across editors, and the versioning contract.
package editors

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"strings"
	"testing"
)

func read(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("required artifact missing: %v", err)
	}
	return data
}

// TestVSCodeManifest: the extension declares the ghtmx language, the
// grammar, the LSP entry point, and the settings the client wires into
// `ghtmx lsp`.
func TestVSCodeManifest(t *testing.T) {
	var manifest struct {
		Version     string `json:"version"`
		Main        string `json:"main"`
		Engines     map[string]string
		Contributes struct {
			Languages []struct {
				ID            string   `json:"id"`
				Extensions    []string `json:"extensions"`
				Configuration string   `json:"configuration"`
				Icon          struct {
					Light string `json:"light"`
					Dark  string `json:"dark"`
				} `json:"icon"`
			} `json:"languages"`
			Grammars []struct {
				Language          string            `json:"language"`
				ScopeName         string            `json:"scopeName"`
				Path              string            `json:"path"`
				EmbeddedLanguages map[string]string `json:"embeddedLanguages"`
			} `json:"grammars"`
			Configuration struct {
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"configuration"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(read(t, "vscode/package.json"), &manifest); err != nil {
		t.Fatalf("package.json is not valid JSON: %v", err)
	}
	if manifest.Main != "./out/extension.js" {
		t.Errorf("main = %q, want the compiled extension entry point", manifest.Main)
	}
	// Both template extensions map to the one ghtmx language: .ghtmx is
	// canonical, .htmx is accepted because some projects prefer it.
	if len(manifest.Contributes.Languages) != 1 || manifest.Contributes.Languages[0].ID != "ghtmx" {
		t.Fatalf("the extension must declare exactly the ghtmx language, got %+v", manifest.Contributes.Languages)
	}
	language := manifest.Contributes.Languages[0]
	if got := strings.Join(language.Extensions, ","); got != ".ghtmx,.htmx" {
		t.Errorf("language extensions = %q, want %q", got, ".ghtmx,.htmx")
	}
	// A file icon only renders if both variants exist: the source mark is
	// near-black and would all but vanish in a dark sidebar, so the dark
	// variant is not optional dressing.
	for theme, path := range map[string]string{"light": language.Icon.Light, "dark": language.Icon.Dark} {
		if path == "" {
			t.Errorf("the language must contribute a %s file icon", theme)
			continue
		}
		if _, err := os.Stat("vscode/" + strings.TrimPrefix(path, "./")); err != nil {
			t.Errorf("contributed %s icon: %v", theme, err)
		}
	}
	if len(manifest.Contributes.Grammars) != 1 || manifest.Contributes.Grammars[0].ScopeName != "source.ghtmx" {
		t.Fatalf("the extension must contribute the source.ghtmx grammar, got %+v", manifest.Contributes.Grammars)
	}
	grammar := manifest.Contributes.Grammars[0]
	if _, err := os.Stat("vscode/" + strings.TrimPrefix(grammar.Path, "./")); err != nil {
		t.Errorf("contributed grammar path: %v", err)
	}
	for scope, language := range map[string]string{
		"meta.embedded.expression.go":               "go",
		"meta.embedded.expression.route-binding.go": "go",
		"meta.embedded.block.css":                   "css",
		"meta.embedded.block.js":                    "javascript",
	} {
		if grammar.EmbeddedLanguages[scope] != language {
			t.Errorf("embedded scope %s must map to %s for editor features", scope, language)
		}
	}
	if _, ok := manifest.Contributes.Configuration.Properties["ghtmx.path"]; !ok {
		t.Error("the ghtmx.path setting must exist so users can point at a specific binary")
	}
	for _, path := range []string{"vscode/language-configuration.json", "vscode/src/extension.ts", "vscode/README.md", "vscode/CHANGELOG.md", "vscode/.vscodeignore", "vscode/LICENSE"} {
		read(t, path)
	}
	// The client must launch the module's LSP entry point, honouring the
	// binary-path setting.
	src := string(read(t, "vscode/src/extension.ts"))
	if !strings.Contains(src, `const args = ["lsp"]`) {
		t.Error("extension.ts must launch the lsp subcommand")
	}
	if !strings.Contains(src, `config.get<string>("path") || "ghtmx"`) {
		t.Error("extension.ts must resolve the binary from the ghtmx.path setting")
	}
	// Build artifacts and dependencies stay out of the repository.
	ignored := string(read(t, "vscode/.gitignore"))
	for _, entry := range []string{"node_modules/", "out/", "*.vsix"} {
		if !strings.Contains(ignored, entry) {
			t.Errorf(".gitignore must exclude %s", entry)
		}
	}
	// The icons must reach the .vsix; excluding them would leave the
	// contribution pointing at files that are not in the package.
	for line := range strings.SplitSeq(string(read(t, "vscode/.vscodeignore")), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "icons") {
			t.Errorf(".vscodeignore must not exclude the icons: %q", strings.TrimSpace(line))
		}
	}
}

// TestVSCodeIconsSuitLightAndDark: the htmx mark is near-black, so the
// same artwork cannot serve both themes — on a dark sidebar the two
// chevrons disappear and only the slash survives. The variants must
// actually differ, and each must carry marks its background can show.
func TestVSCodeIconsSuitLightAndDark(t *testing.T) {
	light := string(read(t, "vscode/icons/ghtmx-light.svg"))
	dark := string(read(t, "vscode/icons/ghtmx-dark.svg"))

	if light == dark {
		t.Fatal("the light and dark icons are identical, so one theme renders an invisible mark")
	}
	// Matched as a fill, not as bare text: both files explain the choice
	// in a comment that names the colour.
	if !strings.Contains(light, `fill="#111111"`) {
		t.Error("the light-theme icon must keep the dark mark so it reads on a light background")
	}
	if strings.Contains(dark, `fill="#111111"`) {
		t.Error("the dark-theme icon must not fill with the near-black mark: it vanishes on a dark background")
	}
	// Square viewBox: a file icon gets a square slot, and the source art
	// is 256x168, so an unpadded viewBox letterboxes or squashes it.
	for name, svg := range map[string]string{"light": light, "dark": dark} {
		if !strings.Contains(svg, `viewBox="0 0 256 256"`) {
			t.Errorf("the %s icon must use a square viewBox so it is not squashed at 16px", name)
		}
	}
}

// TestGrammarCoversNativeConstructs: syntax highlighting must cover the
// ghtmx-native constructs — fragment and event declarations, htmx
// attributes, and route bindings.
func TestGrammarCoversNativeConstructs(t *testing.T) {
	raw := read(t, "vscode/syntaxes/ghtmx.tmLanguage.json")
	var grammar struct {
		ScopeName  string                     `json:"scopeName"`
		Repository map[string]json.RawMessage `json:"repository"`
	}
	if err := json.Unmarshal(raw, &grammar); err != nil {
		t.Fatalf("grammar is not valid JSON: %v", err)
	}
	if grammar.ScopeName != "source.ghtmx" {
		t.Errorf("scopeName = %q, want source.ghtmx", grammar.ScopeName)
	}
	for _, rule := range []string{
		"template-declaration", // templ and fragment blocks
		"nested-fragment",      // fragment declared inside a template
		"event-declaration",    // event Name(params)
		"htmx-binding",         // hx-post={ handlers.CreateItem }
		"htmx-attribute",       // hx-* names
		"component-reference",  // @Name references
	} {
		if _, ok := grammar.Repository[rule]; !ok {
			t.Errorf("grammar must keep the %s rule: it covers a ghtmx-native construct", rule)
		}
	}
	text := string(raw)
	for _, needle := range []string{"templ|fragment", "(event)", "hx-[A-Za-z0-9:._-]+", "meta.embedded.expression.route-binding.go"} {
		if !strings.Contains(text, needle) {
			t.Errorf("grammar lost the %q pattern", needle)
		}
	}
}

// TestJetBrainsBundleMatchesVSCodeGrammar: the JetBrains TextMate
// bundle is a copy of the VS Code grammar and must stay byte-identical
// so highlighting never diverges between editors.
func TestJetBrainsBundleMatchesVSCodeGrammar(t *testing.T) {
	vscode := read(t, "vscode/syntaxes/ghtmx.tmLanguage.json")
	jetbrains := read(t, "jetbrains/src/main/resources/textmate/ghtmx.tmLanguage.json")
	if !bytes.Equal(vscode, jetbrains) {
		t.Error("the JetBrains grammar copy diverged from vscode/syntaxes/ghtmx.tmLanguage.json; copy the file over")
	}
	var bundle struct {
		Contributes struct {
			Grammars []struct {
				ScopeName string `json:"scopeName"`
				Path      string `json:"path"`
			} `json:"grammars"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(read(t, "jetbrains/src/main/resources/textmate/package.json"), &bundle); err != nil {
		t.Fatalf("bundle manifest is not valid JSON: %v", err)
	}
	if len(bundle.Contributes.Grammars) != 1 || bundle.Contributes.Grammars[0].ScopeName != "source.ghtmx" {
		t.Errorf("bundle must contribute the source.ghtmx grammar, got %+v", bundle.Contributes.Grammars)
	}
	// The bundle's claimed extensions are what TextMate highlights, so
	// they must track the VS Code manifest.
	var languages struct {
		Contributes struct {
			Languages []struct {
				Extensions []string `json:"extensions"`
			} `json:"languages"`
		} `json:"contributes"`
	}
	if err := json.Unmarshal(read(t, "jetbrains/src/main/resources/textmate/package.json"), &languages); err != nil {
		t.Fatal(err)
	}
	if len(languages.Contributes.Languages) != 1 {
		t.Fatalf("bundle must declare one language, got %+v", languages.Contributes.Languages)
	}
	if got := strings.Join(languages.Contributes.Languages[0].Extensions, ","); got != ".ghtmx,.htmx" {
		t.Errorf("bundle extensions = %q, want %q", got, ".ghtmx,.htmx")
	}
}

// TestJetBrainsPlugin: the plugin manifest wires the file type, the LSP
// provider, and the TextMate bundle.
func TestJetBrainsPlugin(t *testing.T) {
	raw := read(t, "jetbrains/src/main/resources/META-INF/plugin.xml")
	var plugin struct {
		ID         string `xml:"id"`
		Extensions []struct {
			NS       string `xml:"defaultExtensionNs,attr"`
			InnerXML string `xml:",innerxml"`
		} `xml:"extensions"`
	}
	if err := xml.Unmarshal(raw, &plugin); err != nil {
		t.Fatalf("plugin.xml is not well-formed: %v", err)
	}
	if plugin.ID != "dev.ghtmx.jetbrains" {
		t.Errorf("plugin id = %q", plugin.ID)
	}
	// Both extension points live in the com.intellij namespace — the
	// TextMate plugin declares its EP as com.intellij.textmate.bundleProvider,
	// so declaring it under any other namespace silently loads nothing.
	var intellijNS string
	for _, ext := range plugin.Extensions {
		if ext.NS == "com.intellij" {
			intellijNS = ext.InnerXML
		}
	}
	if intellijNS == "" {
		t.Fatal("plugin.xml must declare extensions in the com.intellij namespace")
	}
	for _, needle := range []string{
		"platform.lsp.serverSupportProvider",
		"GhtmxLspServerSupportProvider",
		"textmate.bundleProvider",
		"GhtmxTextMateBundleProvider",
	} {
		if !strings.Contains(intellijNS, needle) {
			t.Errorf("the com.intellij extensions block must wire %q", needle)
		}
	}
	launcher := string(read(t, "jetbrains/src/main/kotlin/dev/ghtmx/jetbrains/GhtmxLspServerSupportProvider.kt"))
	if !strings.Contains(launcher, `GeneralCommandLine("ghtmx", "lsp")`) {
		t.Error("the JetBrains plugin must launch `ghtmx lsp`")
	}
	if !strings.Contains(launcher, `setOf("ghtmx", "htmx")`) {
		t.Error("the LSP provider must recognise both template extensions")
	}
}

// TestNeovimPlugin: filetype detection, syntax coverage of the native
// constructs, and LSP wiring.
func TestNeovimPlugin(t *testing.T) {
	ftdetect := string(read(t, "nvim/ftdetect/ghtmx.lua"))
	for _, mapping := range []string{`ghtmx = "ghtmx"`, `htmx = "ghtmx"`} {
		if !strings.Contains(ftdetect, mapping) {
			t.Errorf("ftdetect must map %s so both template extensions get the filetype", mapping)
		}
	}
	syntax := string(read(t, "nvim/syntax/ghtmx.vim"))
	for _, needle := range []string{"fragment", "event", "hx-", "templ"} {
		if !strings.Contains(syntax, needle) {
			t.Errorf("syntax file must highlight %q", needle)
		}
	}
	initLua := string(read(t, "nvim/lua/ghtmx/init.lua"))
	if !strings.Contains(initLua, `{ "ghtmx", "lsp" }`) || !strings.Contains(initLua, "vim.lsp.start") {
		t.Error("init.lua must start `ghtmx lsp` via vim.lsp.start")
	}
	read(t, "nvim/README.md")
}

// TestVersioningPolicy: the versioning contract (AC: extension
// versioning and its relation to the module version are documented) is
// written down, and the shipped extension versions match its table.
func TestVersioningPolicy(t *testing.T) {
	doc := string(read(t, "README.md"))
	for _, needle := range []string{"## Versioning", "ghtmx module series", "## Release path"} {
		if !strings.Contains(doc, needle) {
			t.Errorf("editors/README.md must document %q", needle)
		}
	}

	var manifest struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(read(t, "vscode/package.json"), &manifest); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(doc, fmt.Sprintf("| VS Code (`vscode/package.json`) | %s |", manifest.Version)) {
		t.Errorf("the compatibility table must list the VS Code extension version %s", manifest.Version)
	}
	if !strings.Contains(doc, fmt.Sprintf("| Neovim (released with the repository) | %s |", manifest.Version)) {
		t.Errorf("the compatibility table must list the Neovim plugin at the lockstep version %s", manifest.Version)
	}

	gradle := string(read(t, "jetbrains/build.gradle.kts"))
	version := ""
	for line := range strings.SplitSeq(gradle, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), `version = "`); ok {
			version = strings.TrimSuffix(v, `"`)
		}
	}
	if version == "" {
		t.Fatal("build.gradle.kts must declare a plugin version")
	}
	if !strings.Contains(doc, fmt.Sprintf("| JetBrains (`jetbrains/build.gradle.kts`) | %s |", version)) {
		t.Errorf("the compatibility table must list the JetBrains plugin version %s", version)
	}
	if manifest.Version != version {
		t.Errorf("extension versions diverged: vscode %s vs jetbrains %s — release them in lockstep", manifest.Version, version)
	}
}
