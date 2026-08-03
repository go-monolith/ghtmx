package lazyloader

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/go-monolith/ghtmx/internal/config"
	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
	"github.com/go-monolith/ghtmx/internal/lsp/uri"
	"golang.org/x/tools/go/packages"
)

// ext is the configured template extension, falling back to the default
// so a zero-valued traverser still recognises templates.
func (t *goPkgTraverser) ext() string {
	if t.templExt == "" {
		return config.DefaultTemplateExtension
	}
	return t.templExt
}

type pkgTraverser interface {
	openTopologically(ctx context.Context, pkg *packages.Package) error
	closeTopologically(ctx context.Context, pkg *packages.Package) error
}

type goPkgTraverser struct {
	templDocHandler TemplDocHandler
	pkgsRefCount    map[string]int
	fileReader      fileReader
	// templExt is the project's configured template extension: a sibling
	// file only counts as a template when it matches. Empty means the
	// default — an unset value must not silently match nothing.
	templExt string
}

type TemplDocHandler interface {
	HandleDidOpen(ctx context.Context, params *lsp.DidOpenTextDocumentParams) error
	HandleDidClose(ctx context.Context, params *lsp.DidCloseTextDocumentParams) error
}

type fileReader interface {
	read(file string) ([]byte, error)
}

type templFileReader struct{}

func (templFileReader) read(file string) ([]byte, error) {
	return os.ReadFile(file)
}

func (t *goPkgTraverser) openTopologically(ctx context.Context, pkg *packages.Package) error {
	if t.pkgsRefCount[pkg.PkgPath] > 0 {
		t.pkgsRefCount[pkg.PkgPath]++
		return nil
	}

	for _, imp := range pkg.Imports {
		if err := t.openTopologically(ctx, imp); err != nil {
			return fmt.Errorf("open topologically %q: %w", imp.PkgPath, err)
		}
	}

	for _, otherFile := range pkg.OtherFiles {
		if filepath.Ext(otherFile) != t.ext() {
			continue
		}

		text, err := t.fileReader.read(otherFile)
		if err != nil {
			return fmt.Errorf("read file %q: %w", otherFile, err)
		}

		if err := t.templDocHandler.HandleDidOpen(ctx, &lsp.DidOpenTextDocumentParams{
			TextDocument: lsp.TextDocumentItem{
				URI:        uri.File(otherFile),
				Text:       string(text),
				Version:    1,
				LanguageID: "go",
			},
		}); err != nil {
			return fmt.Errorf("did open file %q: %w", otherFile, err)
		}
	}
	t.pkgsRefCount[pkg.PkgPath]++

	return nil
}

func (t *goPkgTraverser) closeTopologically(ctx context.Context, pkg *packages.Package) error {
	if t.pkgsRefCount[pkg.PkgPath] > 1 {
		t.pkgsRefCount[pkg.PkgPath]--
		return nil
	}

	for _, otherFile := range pkg.OtherFiles {
		if filepath.Ext(otherFile) != t.ext() {
			continue
		}

		if err := t.templDocHandler.HandleDidClose(ctx, &lsp.DidCloseTextDocumentParams{
			TextDocument: lsp.TextDocumentIdentifier{
				URI: uri.File(otherFile),
			},
		}); err != nil {
			return fmt.Errorf("did close file %q: %w", otherFile, err)
		}
	}
	delete(t.pkgsRefCount, pkg.PkgPath)

	for _, imp := range pkg.Imports {
		if err := t.closeTopologically(ctx, imp); err != nil {
			return fmt.Errorf("close topologically %q: %w", imp.PkgPath, err)
		}
	}

	return nil
}
