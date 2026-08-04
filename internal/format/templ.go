package format

import (
	"bytes"
	"fmt"

	"github.com/go-monolith/ghtmx/internal/config"
	"github.com/go-monolith/ghtmx/internal/imports"
	parser "github.com/go-monolith/ghtmx/internal/parser"
)

// Config configures formatting. ghtmx has no external formatter dependencies
// (the upstream templ Prettier integration was removed); the struct is kept so
// that a future option does not change the Templ signature.
type Config struct {
	// TemplateExtension is the project's configured template extension,
	// needed to derive the generated Go file name during import
	// processing. Empty means the default.
	TemplateExtension string
}

// templateExt returns the configured extension, or the default when unset
// so a zero Config still formats a conventional project.
func (c Config) templateExt() string {
	if c.TemplateExtension == "" {
		return config.DefaultTemplateExtension
	}
	return c.TemplateExtension
}

// Templ formats templ source, returning the formatted output, whether it changed, and an error if any.
// The fileName is used for Go import processing, use an empty name if the source is not from a file.
func Templ(src []byte, fileName string, config Config) (output []byte, changed bool, err error) {
	t, err := parser.ParseString(string(src))
	if err != nil {
		return nil, false, err
	}
	t.Filepath = fileName
	t, err = imports.Process(t, config.templateExt())
	if err != nil {
		return nil, false, err
	}

	w := new(bytes.Buffer)
	if err = t.Write(w); err != nil {
		return nil, false, fmt.Errorf("formatting error: %w", err)
	}
	out := w.Bytes()
	changed = !bytes.Equal(src, out)
	return out, changed, nil
}
