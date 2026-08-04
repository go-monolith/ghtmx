package proxy

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-monolith/ghtmx/internal/format"
	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
	"github.com/go-monolith/ghtmx/internal/lsp/uri"
)

// The Server and Client proxies map URIs in opposite directions and must
// read one extension. A nil holder breaks that silently — Set becomes a
// no-op and the server ignores the project's configured extension — so
// construction refuses it rather than letting a .htmx project discover the
// problem as missing diagnostics.
func TestNewServerRequiresTheSharedHolder(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewServer accepted a nil template-extension holder")
		}
	}()
	NewServer(slog.New(slog.DiscardHandler), nil, NewSourceMapCache(), NewDiagnosticCache(), true, format.Config{}, nil)
}

// The holder is written by the Server at Initialize and read by the
// Client, so both must observe the same value through the same pointer.
func TestTemplateExtensionIsSharedBetweenProxies(t *testing.T) {
	shared := NewTemplateExtension()
	if got := shared.Get(); got != ".ghtmx" {
		t.Errorf("an unset holder must read as the default, got %q", got)
	}

	client, _ := NewClient(slog.New(slog.DiscardHandler), NewSourceMapCache(), NewDiagnosticCache(), shared)
	server := NewServer(slog.New(slog.DiscardHandler), nil, NewSourceMapCache(), NewDiagnosticCache(), true, format.Config{}, shared)

	shared.Set(".htmx")
	if got := server.templateExt(); got != ".htmx" {
		t.Errorf("server extension = %q, want %q", got, ".htmx")
	}
	if got := client.TemplateExtension.Get(); got != ".htmx" {
		t.Errorf("client extension = %q, want %q — the proxies must not diverge", got, ".htmx")
	}

	// Round-tripping must agree, or a diagnostic lands on a file name that
	// was never generated.
	isTempl, goURI := server.convertTemplToGoURI("file:///p/page.htmx")
	if !isTempl || goURI != "file:///p/page_ghtmx.go" {
		t.Fatalf("templ->go = (%v, %q)", isTempl, goURI)
	}
	isGo, templURI := convertTemplGoToTemplURI(client.TemplateExtension.Get(), goURI)
	if !isGo || templURI != "file:///p/page.htmx" {
		t.Errorf("go->templ = (%v, %q), want the original .htmx URI", isGo, templURI)
	}
}

// A nil holder must still read as the default rather than panicking: the
// proxy tests build Server literals directly.
func TestNilHolderReadsTheDefault(t *testing.T) {
	var nilHolder *TemplateExtension
	nilHolder.Set(".htmx") // must not panic
	if got := nilHolder.Get(); got != ".ghtmx" {
		t.Errorf("nil holder = %q, want the default", got)
	}
}

// Initialize is where ghtmx.json is read, and everything downstream — the
// URI mappings, the workspace scan, and the lazy loader it constructs in
// the NoPreload branch — reads the extension from the server. If the
// config never reached the server, all of them would quietly fall back to
// the canonical extension and a .htmx project would lose diagnostics with
// no error shown.
func TestInitializeTakesTheExtensionFromProjectConfig(t *testing.T) {
	for _, tc := range []struct{ name, configJSON, want string }{
		{name: "configured .htmx", configJSON: `{"templateExtension": ".htmx"}`, want: ".htmx"},
		{name: "absent config", configJSON: "", want: ".ghtmx"},
		{name: "config without the key", configJSON: `{}`, want: ".ghtmx"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.configJSON != "" {
				if err := os.WriteFile(filepath.Join(root, "ghtmx.json"), []byte(tc.configJSON), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			s := newTestServer(&mockServer{})
			s.NoPreload = true
			// Built through the URI helper, not by string concatenation:
			// a Windows temp dir is "C:\\Users\\..." and "file://" + that
			// is not a parseable URI at all.
			if _, err := s.Initialize(context.Background(), &lsp.InitializeParams{
				RootURI: lsp.DocumentURI(uri.URIFromPath(root)),
			}); err != nil {
				t.Fatalf("Initialize: %v", err)
			}

			if got := s.templateExt(); got != tc.want {
				t.Errorf("server extension = %q, want %q", got, tc.want)
			}
			// The NoPreload branch builds the lazy loader from this value.
			if s.templDocLazyLoader == nil {
				t.Error("NoPreload must still construct the lazy loader")
			}
			// The URI mapping must follow the same value.
			isTempl, _ := s.convertTemplToGoURI(lsp.DocumentURI("file:///p/page" + tc.want))
			if !isTempl {
				t.Errorf("a %s document must be recognised as a template", tc.want)
			}
		})
	}
}
