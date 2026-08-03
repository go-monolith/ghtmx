package config

import (
	"strings"
	"testing"
)

// The extension decides what the whole toolchain treats as a template, so
// an unrecognised value must fail loudly at load rather than silently
// matching nothing and reporting "no templates found".
func TestTemplateExtension(t *testing.T) {
	for _, tc := range []struct {
		name    string
		json    string
		want    string
		wantErr string
	}{{
		name: "absent means the canonical extension",
		json: `{}`,
		want: ".ghtmx",
	}, {
		name: "the canonical extension is accepted",
		json: `{"templateExtension": ".ghtmx"}`,
		want: ".ghtmx",
	}, {
		name: "the alternative extension is accepted",
		json: `{"templateExtension": ".htmx"}`,
		want: ".htmx",
	}, {
		name:    "an unsupported extension is rejected by name",
		json:    `{"templateExtension": ".tmpl"}`,
		wantErr: `templateExtension ".tmpl" must be one of ".ghtmx" or ".htmx"`,
	}, {
		// Without the dot the suffix match would still work by accident on
		// "page.htmx", but not on a file literally named "htmx".
		name:    "a missing leading dot is rejected",
		json:    `{"templateExtension": "htmx"}`,
		wantErr: `templateExtension "htmx" must be one of`,
	}, {
		// The generator would consume its own output.
		name:    "the Go extension is rejected",
		json:    `{"templateExtension": ".go"}`,
		wantErr: `templateExtension ".go" must be one of`,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := Parse("ghtmx.json", []byte(tc.json))
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected an error containing %q, got extension %q", tc.wantErr, cfg.TemplateExtension)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if cfg.TemplateExtension != tc.want {
				t.Errorf("templateExtension = %q, want %q", cfg.TemplateExtension, tc.want)
			}
		})
	}
}

// Precedence is flag > file > default (FR-073).
func TestTemplateExtensionFlagWins(t *testing.T) {
	fileCfg, err := Parse("ghtmx.json", []byte(`{"templateExtension": ".htmx"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := Resolve(fileCfg, Flags{}).TemplateExtension; got != ".htmx" {
		t.Errorf("without a flag the file value must stand, got %q", got)
	}
	override := ".ghtmx"
	if got := Resolve(fileCfg, Flags{TemplateExtension: &override}).TemplateExtension; got != ".ghtmx" {
		t.Errorf("the flag must override the file, got %q", got)
	}
}

// The extension changes what is generated, so it has to salt the build
// cache key: otherwise a switch reuses artifacts built for the old one.
func TestTemplateExtensionChangesTheConfigHash(t *testing.T) {
	a := Default()
	b := Default()
	b.TemplateExtension = ".htmx"
	if a.Hash() == b.Hash() {
		t.Error("changing the template extension must change the config hash, or stale output survives the switch")
	}
}
