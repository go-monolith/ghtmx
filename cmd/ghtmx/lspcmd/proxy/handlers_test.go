package proxy

import (
	"context"
	"strings"
	"testing"

	"github.com/go-monolith/ghtmx/internal/format"
	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
)

// shutdownServer implements the two lifecycle methods mockServer refuses,
// so they can be swept like everything else. Their bodies are pure
// delegation, but a session that cannot shut down cleanly leaves gopls
// running after the editor closes.
type shutdownServer struct {
	mockServer
	shutdowns int
	exits     int
}

func (s *shutdownServer) Shutdown(ctx context.Context) error {
	s.shutdowns++
	return nil
}

func (s *shutdownServer) Exit(ctx context.Context) error {
	s.exits++
	return nil
}

func TestShutdownAndExitReachGopls(t *testing.T) {
	target := &shutdownServer{}
	s := NewServer(testLog(), target, NewSourceMapCache(), NewDiagnosticCache(), true, format.Config{}, NewTemplateExtension())
	ctx := context.Background()

	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := s.Exit(ctx); err != nil {
		t.Fatalf("Exit: %v", err)
	}

	// Not forwarding either leaves gopls running after the editor closes.
	if target.shutdowns != 1 {
		t.Errorf("Shutdown reached the target %d times, want 1", target.shutdowns)
	}
	if target.exits != 1 {
		t.Errorf("Exit reached the target %d times, want 1", target.exits)
	}
}

// TestStripTemplStringable pins the unwrapping that keeps hover text
// readable: gopls sees the generated ghtmx.stringable(...) wrapper, and
// showing it to the user would expose an implementation detail they did
// not write.
func TestStripTemplStringable(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"wrapped", `ghtmx.stringable(name)`, "name"},
		{"wrapped expression", `ghtmx.stringable(strings.ToUpper(x))`, "strings.ToUpper(x)"},
		{"not wrapped", "plainValue", "plainValue"},
		{"empty", "", ""},
		// A partial match must be left alone rather than half-trimmed.
		{"different prefix", "other.stringable(name)", "other.stringable(name)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripTemplStringable(tt.in); got != tt.want {
				t.Errorf("stripTemplStringable(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// TestNotifyGoplsVersion pins what the user is told at startup. A
// missing version is the case worth getting right: it means gopls could
// not be identified, and the warning is the only hint someone gets
// before Go features start misbehaving.
func TestNotifyGoplsVersion(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		path     string
		wantType lsp.MessageType
		wantText string
	}{
		{
			name:     "known version",
			version:  "v0.23.0",
			path:     "/usr/bin/gopls",
			wantType: lsp.MessageTypeInfo,
			wantText: "v0.23.0",
		},
		{
			name:     "unknown version",
			path:     "/usr/bin/gopls",
			wantType: lsp.MessageTypeWarning,
			wantText: "could not determine gopls version",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(&mockServer{})
			s.GoplsVersion = tt.version
			s.GoplsPath = tt.path

			client := &loggingClient{}
			s.notifyGoplsVersion(lsp.WithClient(context.Background(), client))

			if len(client.messages) != 1 {
				t.Fatalf("logged %d messages, want 1", len(client.messages))
			}
			got := client.messages[0]
			if got.Type != tt.wantType {
				t.Errorf("message type = %v, want %v", got.Type, tt.wantType)
			}
			if !strings.Contains(got.Message, tt.wantText) {
				t.Errorf("message %q does not contain %q", got.Message, tt.wantText)
			}
		})
	}
}

// TestNotifyGoplsVersionWithoutAClient pins the guard: the notification
// runs during startup, when the client may not be attached yet, and
// dereferencing a nil one would crash before the session begins.
func TestNotifyGoplsVersionWithoutAClient(t *testing.T) {
	s := newTestServer(&mockServer{})
	s.GoplsVersion = "v0.23.0"

	// A client is reachable, but not through the context — which is the
	// only place the notification looks. Nothing may reach it.
	client := &loggingClient{}
	s.notifyGoplsVersion(context.Background())

	if len(client.messages) != 0 {
		t.Errorf("a message was sent with no client in the context: %+v", client.messages)
	}
}

// loggingClient records the LogMessage calls the server makes.
type loggingClient struct {
	recordingClient
	messages []*lsp.LogMessageParams
}

func (c *loggingClient) LogMessage(_ context.Context, params *lsp.LogMessageParams) error {
	c.messages = append(c.messages, params)
	return nil
}
