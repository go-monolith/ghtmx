package proxy

import (
	"context"
	"reflect"
	"sort"
	"testing"

	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
)

// Roughly thirty of this server's methods are pure delegation: log, hand
// the request to gopls, log again. They are individually trivial and
// collectively the bulk of server.go, and the hand-written tests below
// cover about ten of them. The untested ones are where a copy-paste slip
// hides, and the worst version of that slip is a nil dereference: the
// proxy sits between the editor and gopls, so a panic in any one method
// takes down the whole language server mid-keystroke.
//
// The sweep drives every lsp.Server method through the proxy and
// requires none of them to panic or fail on a well-formed empty request.
// Unlike the equivalent sweep in pls it cannot use zero-value arguments:
// these methods read params.TextDocument.URI before deciding anything,
// so pointer arguments are allocated rather than left nil.
//
// Delegation correctness — that each method forwards to the matching
// target call and converts positions on the way — is asserted per method
// by the hand-written tests, which check the recorded params.

// serverMethodNames returns every method in the lsp.Server interface,
// taken from the interface itself so a newly added protocol method joins
// the sweep with no edit here.
func serverMethodNames() []string {
	iface := reflect.TypeFor[lsp.Server]()
	names := make([]string, 0, iface.NumMethod())
	for i := range iface.NumMethod() {
		names = append(names, iface.Method(i).Name)
	}
	sort.Strings(names)
	return names
}

// argFor builds a usable argument of type t: pointers are allocated so
// the callee can dereference them, everything else is the zero value.
func argFor(t reflect.Type) reflect.Value {
	if t.Kind() == reflect.Pointer {
		return reflect.New(t.Elem())
	}
	return reflect.Zero(t)
}

// callMethod invokes the named method with allocated arguments and
// returns the trailing error, reporting a panic as a test failure rather
// than letting one method take the whole run down.
func callMethod(t *testing.T, srv lsp.Server, name string) error {
	return callMethodWith(t, srv, name, context.Background(), nil)
}

// callMethodWith invokes the named method with allocated arguments, an
// explicit context, and an optional hook to populate each argument.
func callMethodWith(t *testing.T, srv lsp.Server, name string, ctx context.Context, fill func(reflect.Value)) (err error) {
	t.Helper()
	method := reflect.ValueOf(srv).MethodByName(name)
	if !method.IsValid() {
		t.Fatalf("%s: not present on %T", name, srv)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked on an empty request: %v\n"+
				"the proxy sits between the editor and gopls, so this would take the language server down",
				name, r)
		}
	}()
	mt := method.Type()
	args := make([]reflect.Value, mt.NumIn())
	for i := range args {
		args[i] = argFor(mt.In(i))
	}
	if fill != nil {
		for _, arg := range args {
			fill(arg)
		}
	}
	if len(args) > 0 && mt.In(0) == reflect.TypeFor[context.Context]() {
		args[0] = reflect.ValueOf(ctx)
	}
	out := method.Call(args)
	if len(out) == 0 {
		return nil
	}
	last := out[len(out)-1]
	if last.IsNil() {
		return nil
	}
	e, ok := last.Interface().(error)
	if !ok {
		t.Fatalf("%s: last return value is %s, want error", name, last.Type())
	}
	return e
}

func TestEveryServerMethodHandlesAnEmptyRequest(t *testing.T) {
	for _, name := range serverMethodNames() {
		if notImplementedByTheMock[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			s := newTestServer(&mockServer{})

			if err := callMethod(t, s, name); err != nil {
				t.Errorf("%s returned %v, want nil", name, err)
			}
		})
	}
}

// notImplementedByTheMock are the three methods mockServer answers with
// panic("not implemented"). The panic is the mock's, not the proxy's, so
// sweeping them would only assert something about the test fixture.
var notImplementedByTheMock = map[string]bool{
	"Exit":        true,
	"Initialized": true,
	"Shutdown":    true,
}

// recordingClient is the editor side of the connection. Several methods
// — the document-sync notifications especially — publish diagnostics
// back to the client, and reach it through the context rather than a
// field, so a sweep without one dereferences nil.
type recordingClient struct {
	published []*lsp.PublishDiagnosticsParams
}

func (c *recordingClient) Progress(context.Context, *lsp.ProgressParams) error { return nil }
func (c *recordingClient) WorkDoneProgressCreate(context.Context, *lsp.WorkDoneProgressCreateParams) error {
	return nil
}
func (c *recordingClient) LogMessage(context.Context, *lsp.LogMessageParams) error { return nil }
func (c *recordingClient) PublishDiagnostics(_ context.Context, params *lsp.PublishDiagnosticsParams) error {
	c.published = append(c.published, params)
	return nil
}
func (c *recordingClient) ShowMessage(context.Context, *lsp.ShowMessageParams) error { return nil }
func (c *recordingClient) ShowMessageRequest(context.Context, *lsp.ShowMessageRequestParams) (*lsp.MessageActionItem, error) {
	return nil, nil
}
func (c *recordingClient) Telemetry(context.Context, any) error { return nil }
func (c *recordingClient) RegisterCapability(context.Context, *lsp.RegistrationParams) error {
	return nil
}
func (c *recordingClient) UnregisterCapability(context.Context, *lsp.UnregistrationParams) error {
	return nil
}
func (c *recordingClient) ApplyEdit(context.Context, *lsp.ApplyWorkspaceEditParams) (*lsp.ApplyWorkspaceEditResponse, error) {
	return nil, nil
}
func (c *recordingClient) Configuration(context.Context, *lsp.ConfigurationParams) ([]any, error) {
	return nil, nil
}
func (c *recordingClient) WorkspaceFolders(context.Context) ([]lsp.WorkspaceFolder, error) {
	return nil, nil
}

var _ lsp.Client = (*recordingClient)(nil)

// TestEveryServerMethodHandlesATemplURI runs the sweep again with a
// .ghtmx document URI and a live client, which is the branch that
// matters: every method that rewrites a templ URI to its generated .go
// counterpart takes a different path here than for the empty URI above.
func TestEveryServerMethodHandlesATemplURI(t *testing.T) {
	const templURI = "file:///project/component.ghtmx"

	for _, name := range serverMethodNames() {
		if notImplementedByTheMock[name] {
			continue
		}
		t.Run(name, func(t *testing.T) {
			s := newTestServer(&mockServer{})
			s.TemplSource.Set(templURI, NewDocument(testLog(), "package main\n"))
			ctx := lsp.WithClient(context.Background(), &recordingClient{})

			// Routed through callMethod so the trailing error is
			// checked: discarding it would let a method that started
			// failing on a well-formed .ghtmx request — the branch this
			// test exists for — pass silently.
			if err := callMethodWith(t, s, name, ctx, func(arg reflect.Value) {
				setTextDocumentURI(arg, templURI)
			}); err != nil {
				t.Errorf("%s returned %v for a .ghtmx request, want nil", name, err)
			}
		})
	}
}

// setTextDocumentURI walks a request value and sets any
// TextDocument.URI it finds, so the sweep exercises the templ-to-Go URI
// rewrite rather than the empty-URI shortcut.
func setTextDocumentURI(v reflect.Value, uri string) {
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return
	}
	td := v.FieldByName("TextDocument")
	if !td.IsValid() || td.Kind() != reflect.Struct {
		return
	}
	field := td.FieldByName("URI")
	if !field.IsValid() || !field.CanSet() || field.Kind() != reflect.String {
		return
	}
	field.SetString(uri)
}

// TestSweepCoversTheWholeInterface guards the sweep: if the method list
// ever comes back short, the tests above would pass while checking
// almost nothing.
func TestSweepCoversTheWholeInterface(t *testing.T) {
	if got := serverMethodNames(); len(got) < 60 {
		t.Errorf("serverMethodNames returned %d methods, want at least 60", len(got))
	}
}
