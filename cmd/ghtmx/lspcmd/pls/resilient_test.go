package pls

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
)

// brokenServer fails every stubbed call with a transport-death error.
type brokenServer struct {
	lsp.Server
}

func (brokenServer) Hover(ctx context.Context, params *lsp.HoverParams) (*lsp.Hover, error) {
	return nil, io.EOF
}

// healthyServer records calls.
type healthyServer struct {
	lsp.Server
	hovers    atomic.Int64
	initCalls atomic.Int64
	opens     atomic.Int64
}

func (h *healthyServer) Hover(ctx context.Context, params *lsp.HoverParams) (*lsp.Hover, error) {
	h.hovers.Add(1)
	return &lsp.Hover{}, nil
}

func (h *healthyServer) Initialize(ctx context.Context, params *lsp.InitializeParams) (*lsp.InitializeResult, error) {
	h.initCalls.Add(1)
	return &lsp.InitializeResult{}, nil
}

func (h *healthyServer) Initialized(ctx context.Context, params *lsp.InitializedParams) error {
	return nil
}

func (h *healthyServer) DidOpen(ctx context.Context, params *lsp.DidOpenTextDocumentParams) error {
	h.opens.Add(1)
	return nil
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fixedSpawner returns the given servers in order; nil entries fail.
func fixedSpawner(spawned *atomic.Int64, servers ...lsp.Server) func(context.Context, int64) (lsp.Server, io.Closer, error) {
	return func(ctx context.Context, gen int64) (lsp.Server, io.Closer, error) {
		n := spawned.Add(1)
		if int(n) > len(servers) || servers[n-1] == nil {
			return nil, nil, errors.New("spawn failed")
		}
		return servers[n-1], nopCloser{}, nil
	}
}

// TestDegradedWhileDown: with gopls down, requests return empty results
// and no error; Initialize returns a usable empty result, never nil.
func TestDegradedWhileDown(t *testing.T) {
	r := NewResilient(discardLog(), nil)
	hover, err := r.Hover(context.Background(), &lsp.HoverParams{})
	if err != nil || hover != nil {
		t.Errorf("down target must degrade to empty results, got %v %v", hover, err)
	}
	res, err := r.Initialize(context.Background(), &lsp.InitializeParams{})
	if err != nil || res == nil {
		t.Errorf("degraded Initialize must return a non-nil result, got %v %v", res, err)
	}
}

// TestTransportDeathTriggersRestartWithReplay: an EOF-class error marks
// gopls down; the supervisor restarts with backoff, replaying the session
// before serving, and flushes document notifications buffered while down.
func TestTransportDeathTriggersRestartWithReplay(t *testing.T) {
	replacement := &healthyServer{}
	var spawned atomic.Int64
	// First spawn: broken instance. Restart attempt 1 fails (backoff
	// retries), attempt 2 delivers the replacement.
	r := NewResilient(discardLog(), fixedSpawner(&spawned, brokenServer{}, nil, replacement))
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	var replayed atomic.Int64
	r.SetReplay(func(ctx context.Context, target lsp.Server) error {
		replayed.Add(1)
		if _, err := target.Initialize(ctx, &lsp.InitializeParams{}); err != nil {
			return err
		}
		return nil
	})

	// The failing call degrades and starts the restart cycle.
	_, _ = r.Hover(context.Background(), &lsp.HoverParams{})
	// A document notification arriving while down is buffered, not lost.
	if err := r.DidOpen(context.Background(), &lsp.DidOpenTextDocumentParams{}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if hover, err := r.Hover(context.Background(), &lsp.HoverParams{}); err == nil && hover != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if replacement.hovers.Load() == 0 {
		t.Fatal("the restarted gopls never served")
	}
	if replayed.Load() != 1 || replacement.initCalls.Load() != 1 {
		t.Errorf("replay must run once before serving: replays=%d inits=%d", replayed.Load(), replacement.initCalls.Load())
	}
	if replacement.opens.Load() != 1 {
		t.Errorf("the buffered didOpen must flush to the new target, got %d", replacement.opens.Load())
	}
	if spawned.Load() != 3 {
		t.Errorf("expected initial spawn + failed retry + success, got %d", spawned.Load())
	}
}

// TestStaleExitHookCannotKillSuccessor: after a restart, the superseded
// process's exit hook fires with its old generation and must no-op.
func TestStaleExitHookCannotKillSuccessor(t *testing.T) {
	replacement := &healthyServer{}
	var spawned atomic.Int64
	r := NewResilient(discardLog(), fixedSpawner(&spawned, brokenServer{}, replacement))
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.MarkDown() // generation 1 goes down; generation 2 comes up.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if hover, _ := r.Hover(context.Background(), &lsp.HoverParams{}); hover != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if replacement.hovers.Load() == 0 {
		t.Fatal("restart never completed")
	}

	// The old process exits now: its hook must not touch generation 2.
	before := spawned.Load()
	r.MarkDownIf(1)
	time.Sleep(50 * time.Millisecond)
	if spawned.Load() != before {
		t.Error("a stale exit hook must not restart the healthy successor")
	}
	if hover, err := r.Hover(context.Background(), &lsp.HoverParams{}); err != nil || hover == nil {
		t.Error("the successor must keep serving")
	}
}

// TestStaleObservationCannotKillSuccessor: an in-flight call against the
// old generation returns a transport error after the swap; observing it
// must not restart the new instance.
func TestStaleObservationCannotKillSuccessor(t *testing.T) {
	replacement := &healthyServer{}
	var spawned atomic.Int64
	r := NewResilient(discardLog(), fixedSpawner(&spawned, brokenServer{}, replacement))
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.MarkDown()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if hover, _ := r.Hover(context.Background(), &lsp.HoverParams{}); hover != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	before := spawned.Load()
	r.observeAt(1, io.EOF) // Stale generation's error arrives late.
	time.Sleep(50 * time.Millisecond)
	if spawned.Load() != before {
		t.Error("a stale observation must not restart the successor")
	}
}

// TestMarkDownIsIdempotent: repeated exit signals start one restart.
func TestMarkDownIsIdempotent(t *testing.T) {
	var spawned atomic.Int64
	blocker := make(chan struct{})
	r := NewResilient(discardLog(), func(ctx context.Context, gen int64) (lsp.Server, io.Closer, error) {
		if gen == 1 {
			return brokenServer{}, nopCloser{}, nil
		}
		spawned.Add(1)
		<-blocker
		return &healthyServer{}, nopCloser{}, nil
	})
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	r.MarkDown()
	r.MarkDown()
	r.MarkDown()
	time.Sleep(100 * time.Millisecond)
	if got := spawned.Load(); got != 1 {
		t.Errorf("one restart cycle expected, got %d spawns", got)
	}
	close(blocker)
}

// TestResponseErrorsDoNotRestart: an ordinary gopls response error is the
// caller's business, not a crash.
func TestResponseErrorsDoNotRestart(t *testing.T) {
	var spawned atomic.Int64
	r := NewResilient(discardLog(), fixedSpawner(&spawned, &failingHover{}))
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	before := spawned.Load()
	if _, err := r.Hover(context.Background(), &lsp.HoverParams{}); err == nil {
		t.Fatal("the response error must propagate to the caller")
	}
	time.Sleep(50 * time.Millisecond)
	if spawned.Load() != before {
		t.Error("ordinary errors must not trigger a restart")
	}
}

type failingHover struct{ lsp.Server }

func (failingHover) Hover(ctx context.Context, params *lsp.HoverParams) (*lsp.Hover, error) {
	return nil, errors.New("no identifier at position")
}
