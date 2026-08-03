package proxy

import (
	"context"
	"reflect"
	"sort"
	"testing"

	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
)

// Client is the return leg: gopls talks about *_ghtmx.go files, and this
// rewrites those messages so the editor sees the .ghtmx the user is
// actually looking at. Most of its methods are straight delegation, and
// like the server side they were largely untested — a method that
// forwards to the wrong call, or returns before forwarding, makes an
// editor feature go quiet with nothing logged anywhere.

func clientMethodNames() []string {
	iface := reflect.TypeFor[lsp.Client]()
	names := make([]string, 0, iface.NumMethod())
	for i := range iface.NumMethod() {
		names = append(names, iface.Method(i).Name)
	}
	sort.Strings(names)
	return names
}

// countingClient records how many calls reached it, which is what proves
// the wrapper delegated rather than answering by itself.
type countingClient struct {
	recordingClient
	calls int
}

func (c *countingClient) Progress(ctx context.Context, p *lsp.ProgressParams) error {
	c.calls++
	return nil
}

func (c *countingClient) WorkDoneProgressCreate(ctx context.Context, p *lsp.WorkDoneProgressCreateParams) error {
	c.calls++
	return nil
}

func (c *countingClient) LogMessage(ctx context.Context, p *lsp.LogMessageParams) error {
	c.calls++
	return nil
}

func (c *countingClient) ShowMessage(ctx context.Context, p *lsp.ShowMessageParams) error {
	c.calls++
	return nil
}

func newTestClient(target lsp.Client) *Client {
	c, init := NewClient(testLog(), NewSourceMapCache(), NewDiagnosticCache(), NewTemplateExtension())
	init(target)
	return c
}

// TestEveryClientMethodHandlesAnEmptyMessage sweeps the whole interface.
// A panic here takes the language server down while gopls is mid-reply,
// which the user experiences as the editor losing Go features until it
// is restarted.
func TestEveryClientMethodHandlesAnEmptyMessage(t *testing.T) {
	for _, name := range clientMethodNames() {
		t.Run(name, func(t *testing.T) {
			c := newTestClient(&recordingClient{})

			method := reflect.ValueOf(c).MethodByName(name)
			if !method.IsValid() {
				t.Fatalf("%s: not present on *Client", name)
			}
			mt := method.Type()
			args := make([]reflect.Value, mt.NumIn())
			for i := range args {
				args[i] = argFor(mt.In(i))
			}
			if len(args) > 0 && mt.In(0) == reflect.TypeFor[context.Context]() {
				args[0] = reflect.ValueOf(context.Background())
			}

			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s panicked on an empty message: %v", name, r)
				}
			}()
			out := method.Call(args)
			if len(out) == 0 {
				return
			}
			last := out[len(out)-1]
			if !last.IsNil() {
				if err, ok := last.Interface().(error); ok && err != nil {
					t.Errorf("%s returned %v, want nil", name, err)
				}
			}
		})
	}
}

func TestClientDelegatesToTheTarget(t *testing.T) {
	target := &countingClient{}
	c := newTestClient(target)
	ctx := context.Background()

	tests := []struct {
		name string
		call func() error
	}{
		{"Progress", func() error { return c.Progress(ctx, &lsp.ProgressParams{}) }},
		{"WorkDoneProgressCreate", func() error {
			return c.WorkDoneProgressCreate(ctx, &lsp.WorkDoneProgressCreateParams{})
		}},
		{"LogMessage", func() error { return c.LogMessage(ctx, &lsp.LogMessageParams{Message: "hi"}) }},
		{"ShowMessage", func() error { return c.ShowMessage(ctx, &lsp.ShowMessageParams{Message: "hi"}) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := target.calls
			if err := tt.call(); err != nil {
				t.Fatalf("%s: %v", tt.name, err)
			}
			if target.calls != before+1 {
				t.Errorf("%s did not reach the target", tt.name)
			}
		})
	}
}

// TestPublishDiagnosticsSkipsGoMod pins a deliberate filter: gopls
// reports go.mod problems that have nothing to do with the template the
// user is editing, and surfacing them in a .ghtmx file would be noise
// pointing at a file that is not open.
func TestPublishDiagnosticsSkipsGoMod(t *testing.T) {
	target := &recordingClient{}
	c := newTestClient(target)

	err := c.PublishDiagnostics(context.Background(), &lsp.PublishDiagnosticsParams{
		URI:         "file:///project/go.mod",
		Diagnostics: []lsp.Diagnostic{{Message: "module problem"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(target.published) != 0 {
		t.Errorf("go.mod diagnostics were forwarded to the editor: %+v", target.published)
	}
}

// TestPublishDiagnosticsReportsAMissingSourceMap pins the error path: a
// diagnostic arriving before didOpen has no map to rewrite through, and
// silently dropping it would hide a real ordering bug.
func TestPublishDiagnosticsReportsAMissingSourceMap(t *testing.T) {
	c := newTestClient(&recordingClient{})

	err := c.PublishDiagnostics(context.Background(), &lsp.PublishDiagnosticsParams{
		URI:         "file:///project/never-opened_ghtmx.go",
		Diagnostics: []lsp.Diagnostic{{Message: "x"}},
	})
	if err == nil {
		t.Error("no error for a diagnostic with no source map in the cache")
	}
}

// TestShowMessageSuppressesGeneratedFileWarnings pins the filter that
// stops gopls telling the user not to edit a file they are not editing:
// they have the .ghtmx open, not the generated .go.
func TestShowMessageSuppressesGeneratedFileWarnings(t *testing.T) {
	tests := []struct {
		name      string
		message   string
		forwarded bool
	}{
		{"do not edit", "Do not edit this file!", false},
		{"warning editing", "Warning: editing a generated file", false},
		{"ordinary message", "gopls: build finished", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &countingClient{}
			c := newTestClient(target)

			if err := c.ShowMessage(context.Background(), &lsp.ShowMessageParams{Message: tt.message}); err != nil {
				t.Fatal(err)
			}
			if got := target.calls > 0; got != tt.forwarded {
				t.Errorf("forwarded = %v, want %v for %q", got, tt.forwarded, tt.message)
			}
		})
	}
}

// TestSweepCoversTheClientInterface guards the sweep itself.
func TestSweepCoversTheClientInterface(t *testing.T) {
	if got := clientMethodNames(); len(got) < 10 {
		t.Errorf("clientMethodNames returned %d methods, want at least 10", len(got))
	}
}
