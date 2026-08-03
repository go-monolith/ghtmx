package proxy

import (
	"log/slog"
	"testing"

	"github.com/go-monolith/ghtmx/internal/format"
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
