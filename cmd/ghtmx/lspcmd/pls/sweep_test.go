package pls

import (
	"context"
	"io"
	"reflect"
	"slices"
	"sort"
	"sync/atomic"
	"testing"

	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
)

// The Resilient wrapper is 61 near-identical methods: fetch the current
// target, return empty results if gopls is down, otherwise delegate and
// report the outcome. Hand-writing a test per method would be 61 copies
// of the same three assertions, and the copies that were never written
// are exactly where a mistake hides — a wrapper that dereferences
// params before the nil-target check panics the whole language server
// the first time gopls dies.
//
// So the sweep drives every method of the lsp.Server interface by
// reflection, in each of the two states a wrapper can be in. Zero-value
// arguments are safe precisely because of the property under test: no
// wrapper may touch its params before deciding whether the target is
// live.

// serverMethods returns every method name in the lsp.Server interface,
// sorted so failures are reported in a stable order. Taking the names
// from the interface rather than from Resilient's method set is what
// makes the sweep self-maintaining: a method added to lsp.Server (and
// so to Resilient) joins the sweep with no edit here.
func serverMethods() []string {
	iface := reflect.TypeFor[lsp.Server]()
	names := make([]string, 0, iface.NumMethod())
	for i := range iface.NumMethod() {
		names = append(names, iface.Method(i).Name)
	}
	sort.Strings(names)
	return names
}

// callWithZeroArgs invokes the named method with zero values for every
// argument and returns the trailing error. It fails the test rather
// than panicking out of the goroutine, so one bad wrapper reports as
// one failure instead of taking the run down.
func callWithZeroArgs(t *testing.T, srv lsp.Server, name string) (err error) {
	t.Helper()
	method := reflect.ValueOf(srv).MethodByName(name)
	if !method.IsValid() {
		t.Fatalf("%s: not present on %T", name, srv)
	}
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked with zero-value arguments: %v\n"+
				"a wrapper must not touch its params before checking whether the target is live",
				name, r)
		}
	}()
	mt := method.Type()
	args := make([]reflect.Value, mt.NumIn())
	for i := range args {
		args[i] = reflect.Zero(mt.In(i))
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

// stubServer is a complete lsp.Server that answers every method with
// empty results. It is a Resilient with no target — the degraded path
// already has exactly those semantics — which keeps the sweep free of a
// hand-maintained 61-method fake that would rot the moment the protocol
// gained a request.
type stubServer struct {
	*Resilient
}

func newStubServer() *stubServer {
	var spawned atomic.Int64
	// Never started, so target stays nil and every call degrades.
	return &stubServer{Resilient: NewResilient(discardLog(), fixedSpawner(&spawned))}
}

// pendingCountOf reports how many document-sync notifications a
// supervisor has buffered. It takes the lock because restart() runs in a
// goroutine and reassigns pending.
func pendingCountOf(r *Resilient) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.pending)
}

// pendingCount is the same read for the stub target.
func (s *stubServer) pendingCount() int { return pendingCountOf(s.Resilient) }

// documentSyncMethods are the notifications Resilient buffers for replay
// while gopls is down, rather than dropping. Keeping the list explicit
// pins the policy: adding a fifth buffered notification should be a
// deliberate edit here, not a silent behaviour change.
var documentSyncMethods = []string{"DidChange", "DidClose", "DidOpen", "DidSave"}

func TestEveryServerMethodDegradesWhileGoplsIsDown(t *testing.T) {
	var spawned atomic.Int64
	r := NewResilient(discardLog(), fixedSpawner(&spawned))
	// Deliberately not Started: target is nil, the degraded state.

	for _, name := range serverMethods() {
		t.Run(name, func(t *testing.T) {
			if err := callWithZeroArgs(t, r, name); err != nil {
				t.Errorf("%s returned %v while gopls is down, want nil: "+
					"degrading must look like an empty result, not a failure", name, err)
			}
		})
	}

	// The document-sync notifications are buffered for replay; every
	// other method drops or returns empty.
	if got, want := len(r.pending), len(documentSyncMethods); got != want {
		t.Errorf("buffered %d notifications, want %d (%v)", got, want, documentSyncMethods)
	}
}

func TestEveryServerMethodDelegatesToTheTarget(t *testing.T) {
	stub := newStubServer()
	var spawned atomic.Int64
	r := NewResilient(discardLog(), fixedSpawner(&spawned, stub))
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, name := range serverMethods() {
		t.Run(name, func(t *testing.T) {
			if err := callWithZeroArgs(t, r, name); err != nil {
				t.Errorf("%s returned %v, want nil", name, err)
			}
		})
	}

	// With a live target nothing may be buffered on the wrapper itself:
	// the notifications went through to the target, which buffered them
	// in turn because it is degraded. Read under the lock: restart()
	// runs in a goroutine and reassigns pending.
	if got := pendingCountOf(r); got != 0 {
		t.Errorf("wrapper buffered %d notifications while the target was live, want 0", got)
	}
	if got, want := stub.pendingCount(), len(documentSyncMethods); got != want {
		t.Errorf("target received %d document-sync notifications, want %d (%v) — "+
			"the sweep did not actually reach the target", got, want, documentSyncMethods)
	}
}

// TestDocumentSyncNotificationsReplayInOrder pins the reason the
// notifications are buffered at all: a document edited while gopls was
// down must reach the replacement in the order the edits happened, or
// the new gopls sees a file that never existed.
func TestDocumentSyncNotificationsReplayInOrder(t *testing.T) {
	ctx := context.Background()
	var spawned atomic.Int64
	r := NewResilient(discardLog(), fixedSpawner(&spawned))

	// Down: these buffer rather than reach anyone.
	if err := r.DidOpen(ctx, &lsp.DidOpenTextDocumentParams{}); err != nil {
		t.Fatal(err)
	}
	if err := r.DidChange(ctx, &lsp.DidChangeTextDocumentParams{}); err != nil {
		t.Fatal(err)
	}
	if got := len(r.pending); got != 2 {
		t.Fatalf("buffered %d notifications, want 2", got)
	}

	// Replaying against a live target drains them in arrival order.
	var order []string
	target := &orderRecorder{onOpen: func() { order = append(order, "open") },
		onChange: func() { order = append(order, "change") }}
	r.mu.Lock()
	pending := r.pending
	r.mu.Unlock()
	for _, send := range pending {
		if err := send(ctx, target); err != nil {
			t.Fatal(err)
		}
	}
	if want := []string{"open", "change"}; !reflect.DeepEqual(order, want) {
		t.Errorf("replay order = %v, want %v", order, want)
	}
}

// orderRecorder records the document-sync calls it receives. It embeds
// a degraded Resilient so it satisfies the whole interface without a
// hand-written fake.
type orderRecorder struct {
	*Resilient
	onOpen   func()
	onChange func()
}

func (o *orderRecorder) DidOpen(ctx context.Context, params *lsp.DidOpenTextDocumentParams) error {
	o.onOpen()
	return nil
}

func (o *orderRecorder) DidChange(ctx context.Context, params *lsp.DidChangeTextDocumentParams) error {
	o.onChange()
	return nil
}

// TestSweepCoversTheWholeInterface guards the sweep itself: if
// serverMethods ever returns a short list — a reflect API change, a
// refactor that splits the interface — the sweeps above would silently
// test almost nothing and still pass.
func TestSweepCoversTheWholeInterface(t *testing.T) {
	got := serverMethods()
	if len(got) < 60 {
		t.Errorf("serverMethods returned %d methods, want at least 60 — the sweep is not covering the interface", len(got))
	}
	// Spot-check the shapes the sweep has to handle: a plain request, a
	// notification, a context-only method, and the variadic-looking one
	// with an extra string argument.
	for _, want := range []string{"Hover", "DidOpen", "Shutdown", "Request"} {
		if !slices.Contains(got, want) {
			t.Errorf("serverMethods is missing %s", want)
		}
	}
}

// TestDegradedResultsAreUsable pins that the degraded path returns
// values a client can consume. A nil *InitializeResult would crash the
// editor's handshake, which is the one moment gopls being down must not
// be fatal.
func TestDegradedResultsAreUsable(t *testing.T) {
	ctx := context.Background()
	var spawned atomic.Int64
	r := NewResilient(discardLog(), fixedSpawner(&spawned))

	result, err := r.Initialize(ctx, &lsp.InitializeParams{})
	if err != nil {
		t.Fatalf("Initialize while down: %v", err)
	}
	if result == nil {
		t.Error("Initialize returned a nil result while gopls was down; the handshake needs a usable value")
	}

	if _, err := r.Hover(ctx, &lsp.HoverParams{}); err != nil {
		t.Errorf("Hover while down: %v", err)
	}
	if _, err := r.Completion(ctx, &lsp.CompletionParams{}); err != nil {
		t.Errorf("Completion while down: %v", err)
	}
}

// TestObserveAtIgnoresNilAndResponseErrors and its transport-death
// counterpart are covered by the hand-written tests in resilient_test.go;
// this pins the remaining branch, that a non-transport error neither
// restarts nor is swallowed into a restart loop.
func TestNonTransportErrorDoesNotRestart(t *testing.T) {
	var spawned atomic.Int64
	r := NewResilient(discardLog(), fixedSpawner(&spawned, &healthyServer{}))
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.observeAt(1, io.ErrNoProgress)
	if target, _ := r.currentWithGen(); target == nil {
		t.Error("a non-transport error took gopls down; only transport death may restart")
	}
}
