// Code generated for gopls supervision (FR-085); mechanical delegation
// with a hand-written control core below.
//
// Resilient wraps the live gopls connection. While gopls is down,
// requests degrade to empty results, document-sync notifications are
// buffered for replay, and other notifications drop — embedded-Go
// features pause while .ghtmx diagnostics keep working — and a
// supervisor restarts gopls with backoff, replaying session state.
package pls

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/go-monolith/ghtmx/internal/lsp/jsonrpc2"
	lsp "github.com/go-monolith/ghtmx/internal/lsp/protocol"
)

var _ lsp.Server = (*Resilient)(nil)

// Resilient is an lsp.Server whose underlying target can die and come
// back. Every target instance carries a generation token: stale exit
// hooks and errors from superseded instances can never take down a
// healthy successor.
type Resilient struct {
	log *slog.Logger
	mu  sync.Mutex

	target     lsp.Server
	closer     io.Closer
	generation int64
	restarting bool

	// spawn starts a fresh gopls for the given generation; replay
	// re-establishes session state on a new target before it serves.
	spawn  func(ctx context.Context, generation int64) (lsp.Server, io.Closer, error)
	replay func(ctx context.Context, target lsp.Server) error

	// pending holds document-sync notifications that arrived while gopls
	// was down; they flush in order once the new target is live.
	pending []func(ctx context.Context, target lsp.Server) error
}

// NewResilient builds an unstarted supervisor; call Start for the first
// spawn.
func NewResilient(log *slog.Logger, spawn func(ctx context.Context, generation int64) (lsp.Server, io.Closer, error)) *Resilient {
	return &Resilient{log: log, spawn: spawn}
}

// Start performs the initial spawn (generation 1). A failure here is the
// caller's to handle — without a first gopls the session still works,
// but the caller may prefer to exit.
func (r *Resilient) Start(ctx context.Context) error {
	target, closer, err := r.spawn(ctx, 1)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.target = target
	r.closer = closer
	r.generation = 1
	r.mu.Unlock()
	return nil
}

// SetReplay installs the session-state replay used after a restart.
func (r *Resilient) SetReplay(replay func(ctx context.Context, target lsp.Server) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.replay = replay
}

// Close shuts the current target's connection down for good.
func (r *Resilient) Close() error {
	r.mu.Lock()
	closer := r.closer
	r.closer = nil
	r.target = nil
	r.spawn = nil
	r.mu.Unlock()
	if closer != nil {
		return closer.Close()
	}
	return nil
}

func (r *Resilient) currentWithGen() (lsp.Server, int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.target, r.generation
}

// MarkDown takes the current generation down; MarkDownIf no-ops unless
// the given generation is still current, so a superseded instance's exit
// hook cannot kill its successor.
func (r *Resilient) MarkDown() {
	r.mu.Lock()
	gen := r.generation
	r.mu.Unlock()
	r.MarkDownIf(gen)
}

func (r *Resilient) MarkDownIf(generation int64) {
	r.mu.Lock()
	if r.restarting || generation != r.generation || r.spawn == nil {
		r.mu.Unlock()
		return
	}
	r.restarting = true
	r.target = nil
	closer := r.closer
	r.closer = nil
	spawn := r.spawn
	r.mu.Unlock()
	if closer != nil {
		_ = closer.Close()
	}
	if spawn == nil {
		return
	}
	r.log.Warn("gopls is down; embedded-Go features degrade while it restarts")
	go r.restart()
}

func (r *Resilient) restart() {
	policy := backoff.NewExponentialBackOff()
	policy.InitialInterval = 250 * time.Millisecond
	policy.MaxInterval = 10 * time.Second
	policy.MaxElapsedTime = 0 // Keep trying for the session's lifetime.
	attempts := 0
	_ = backoff.Retry(func() error {
		ctx := context.Background()
		r.mu.Lock()
		candidate := r.generation + 1
		spawn := r.spawn
		replay := r.replay
		r.mu.Unlock()
		if spawn == nil {
			return nil // Closed for good.
		}
		target, closer, err := r.spawn(ctx, candidate)
		if err != nil {
			attempts++
			if attempts <= 3 || attempts%10 == 0 {
				r.log.Warn("gopls restart attempt failed", slog.Int("attempt", attempts), slog.Any("error", err))
			}
			return err
		}
		if replay != nil {
			if err := replay(ctx, target); err != nil {
				r.log.Warn("gopls session replay failed", slog.Any("error", err))
				_ = closer.Close()
				return err
			}
		}
		r.mu.Lock()
		r.generation = candidate
		r.target = target
		r.closer = closer
		r.restarting = false
		pending := r.pending
		r.pending = nil
		r.mu.Unlock()
		// Flush the downtime deltas in arrival order.
		for _, send := range pending {
			if err := send(ctx, target); err != nil {
				r.log.Warn("failed to flush buffered notification", slog.Any("error", err))
			}
		}
		r.log.Info("gopls restarted; embedded-Go features restored")
		return nil
	}, policy)
}

// bufferNotification queues a document-sync notification that arrived
// while gopls was down.
func (r *Resilient) bufferNotification(send func(ctx context.Context, target lsp.Server) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = append(r.pending, send)
}

// observeAt funnels transport-death errors from the given generation into
// the restart cycle. Response errors (jsonrpc2.Error) are gopls speaking,
// not dying, and never trigger a restart; neither do errors from a
// superseded generation.
func (r *Resilient) observeAt(generation int64, err error) {
	if err == nil {
		return
	}
	var respErr *jsonrpc2.Error
	if errors.As(err, &respErr) {
		return
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, os.ErrClosed) || errors.Is(err, net.ErrClosed) {
		r.MarkDownIf(generation)
	}
}

func (r *Resilient) Initialize(ctx context.Context, params *lsp.InitializeParams) (result *lsp.InitializeResult, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		// Never hand the proxy a nil result to dereference.
		return &lsp.InitializeResult{}, nil
	}
	result, err = t.Initialize(ctx, params)
	r.observeAt(gen, err)
	if result == nil {
		result = &lsp.InitializeResult{}
	}
	return
}

func (r *Resilient) Initialized(ctx context.Context, params *lsp.InitializedParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.Initialized(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) Shutdown(ctx context.Context) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.Shutdown(ctx)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) Exit(ctx context.Context) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.Exit(ctx)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) WorkDoneProgressCancel(ctx context.Context, params *lsp.WorkDoneProgressCancelParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.WorkDoneProgressCancel(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) LogTrace(ctx context.Context, params *lsp.LogTraceParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.LogTrace(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) SetTrace(ctx context.Context, params *lsp.SetTraceParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.SetTrace(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) CodeAction(ctx context.Context, params *lsp.CodeActionParams) (result []lsp.CodeAction, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.CodeAction(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) CodeLens(ctx context.Context, params *lsp.CodeLensParams) (result []lsp.CodeLens, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.CodeLens(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) CodeLensResolve(ctx context.Context, params *lsp.CodeLens) (result *lsp.CodeLens, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.CodeLensResolve(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) ColorPresentation(ctx context.Context, params *lsp.ColorPresentationParams) (result []lsp.ColorPresentation, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.ColorPresentation(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) Completion(ctx context.Context, params *lsp.CompletionParams) (result *lsp.CompletionList, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.Completion(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) CompletionResolve(ctx context.Context, params *lsp.CompletionItem) (result *lsp.CompletionItem, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.CompletionResolve(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) Declaration(ctx context.Context, params *lsp.DeclarationParams) (result []lsp.Location /* Declaration | DeclarationLink[] | null */, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.Declaration(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) Definition(ctx context.Context, params *lsp.DefinitionParams) (result []lsp.Location /* Definition | DefinitionLink[] | null */, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.Definition(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DidChange(ctx context.Context, params *lsp.DidChangeTextDocumentParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		r.bufferNotification(func(ctx context.Context, target lsp.Server) error { return target.DidChange(ctx, params) })
		return
	}
	err = t.DidChange(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DidChangeConfiguration(ctx context.Context, params *lsp.DidChangeConfigurationParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.DidChangeConfiguration(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DidChangeWatchedFiles(ctx context.Context, params *lsp.DidChangeWatchedFilesParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.DidChangeWatchedFiles(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DidChangeWorkspaceFolders(ctx context.Context, params *lsp.DidChangeWorkspaceFoldersParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.DidChangeWorkspaceFolders(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DidClose(ctx context.Context, params *lsp.DidCloseTextDocumentParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		r.bufferNotification(func(ctx context.Context, target lsp.Server) error { return target.DidClose(ctx, params) })
		return
	}
	err = t.DidClose(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DidOpen(ctx context.Context, params *lsp.DidOpenTextDocumentParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		r.bufferNotification(func(ctx context.Context, target lsp.Server) error { return target.DidOpen(ctx, params) })
		return
	}
	err = t.DidOpen(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DidSave(ctx context.Context, params *lsp.DidSaveTextDocumentParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		r.bufferNotification(func(ctx context.Context, target lsp.Server) error { return target.DidSave(ctx, params) })
		return
	}
	err = t.DidSave(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DocumentColor(ctx context.Context, params *lsp.DocumentColorParams) (result []lsp.ColorInformation, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.DocumentColor(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DocumentHighlight(ctx context.Context, params *lsp.DocumentHighlightParams) (result []lsp.DocumentHighlight, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.DocumentHighlight(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DocumentLink(ctx context.Context, params *lsp.DocumentLinkParams) (result []lsp.DocumentLink, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.DocumentLink(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DocumentLinkResolve(ctx context.Context, params *lsp.DocumentLink) (result *lsp.DocumentLink, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.DocumentLinkResolve(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DocumentSymbol(ctx context.Context, params *lsp.DocumentSymbolParams) (result []lsp.SymbolInformationOrDocumentSymbol, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.DocumentSymbol(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) ExecuteCommand(ctx context.Context, params *lsp.ExecuteCommandParams) (result any, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.ExecuteCommand(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) FoldingRanges(ctx context.Context, params *lsp.FoldingRangeParams) (result []lsp.FoldingRange, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.FoldingRanges(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) Formatting(ctx context.Context, params *lsp.DocumentFormattingParams) (result []lsp.TextEdit, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.Formatting(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) Hover(ctx context.Context, params *lsp.HoverParams) (result *lsp.Hover, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.Hover(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) Implementation(ctx context.Context, params *lsp.ImplementationParams) (result []lsp.Location, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.Implementation(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) OnTypeFormatting(ctx context.Context, params *lsp.DocumentOnTypeFormattingParams) (result []lsp.TextEdit, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.OnTypeFormatting(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) PrepareRename(ctx context.Context, params *lsp.PrepareRenameParams) (result *lsp.Range, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.PrepareRename(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) RangeFormatting(ctx context.Context, params *lsp.DocumentRangeFormattingParams) (result []lsp.TextEdit, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.RangeFormatting(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) References(ctx context.Context, params *lsp.ReferenceParams) (result []lsp.Location, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.References(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) Rename(ctx context.Context, params *lsp.RenameParams) (result *lsp.WorkspaceEdit, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.Rename(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) SignatureHelp(ctx context.Context, params *lsp.SignatureHelpParams) (result *lsp.SignatureHelp, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.SignatureHelp(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) Symbols(ctx context.Context, params *lsp.WorkspaceSymbolParams) (result []lsp.SymbolInformation, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.Symbols(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) TypeDefinition(ctx context.Context, params *lsp.TypeDefinitionParams) (result []lsp.Location, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.TypeDefinition(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) WillSave(ctx context.Context, params *lsp.WillSaveTextDocumentParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.WillSave(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) WillSaveWaitUntil(ctx context.Context, params *lsp.WillSaveTextDocumentParams) (result []lsp.TextEdit, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.WillSaveWaitUntil(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) ShowDocument(ctx context.Context, params *lsp.ShowDocumentParams) (result *lsp.ShowDocumentResult, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.ShowDocument(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) WillCreateFiles(ctx context.Context, params *lsp.CreateFilesParams) (result *lsp.WorkspaceEdit, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.WillCreateFiles(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DidCreateFiles(ctx context.Context, params *lsp.CreateFilesParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.DidCreateFiles(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) WillRenameFiles(ctx context.Context, params *lsp.RenameFilesParams) (result *lsp.WorkspaceEdit, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.WillRenameFiles(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DidRenameFiles(ctx context.Context, params *lsp.RenameFilesParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.DidRenameFiles(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) WillDeleteFiles(ctx context.Context, params *lsp.DeleteFilesParams) (result *lsp.WorkspaceEdit, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.WillDeleteFiles(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) DidDeleteFiles(ctx context.Context, params *lsp.DeleteFilesParams) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.DidDeleteFiles(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) CodeLensRefresh(ctx context.Context) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.CodeLensRefresh(ctx)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) PrepareCallHierarchy(ctx context.Context, params *lsp.CallHierarchyPrepareParams) (result []lsp.CallHierarchyItem, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.PrepareCallHierarchy(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) IncomingCalls(ctx context.Context, params *lsp.CallHierarchyIncomingCallsParams) (result []lsp.CallHierarchyIncomingCall, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.IncomingCalls(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) OutgoingCalls(ctx context.Context, params *lsp.CallHierarchyOutgoingCallsParams) (result []lsp.CallHierarchyOutgoingCall, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.OutgoingCalls(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) SemanticTokensFull(ctx context.Context, params *lsp.SemanticTokensParams) (result *lsp.SemanticTokens, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.SemanticTokensFull(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) SemanticTokensFullDelta(ctx context.Context, params *lsp.SemanticTokensDeltaParams) (result any /* SemanticTokens | SemanticTokensDelta */, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.SemanticTokensFullDelta(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) SemanticTokensRange(ctx context.Context, params *lsp.SemanticTokensRangeParams) (result *lsp.SemanticTokens, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.SemanticTokensRange(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) SemanticTokensRefresh(ctx context.Context) (err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	err = t.SemanticTokensRefresh(ctx)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) LinkedEditingRange(ctx context.Context, params *lsp.LinkedEditingRangeParams) (result *lsp.LinkedEditingRanges, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.LinkedEditingRange(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) Moniker(ctx context.Context, params *lsp.MonikerParams) (result []lsp.Moniker, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return // Degraded: gopls is down.
	}
	result, err = t.Moniker(ctx, params)
	r.observeAt(gen, err)
	return
}

func (r *Resilient) Request(ctx context.Context, method string, params any) (result any, err error) {
	t, gen := r.currentWithGen()
	if t == nil {
		return nil, nil // Degraded: gopls is down.
	}
	result, err = t.Request(ctx, method, params)
	r.observeAt(gen, err)
	return result, err
}
