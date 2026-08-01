package lspcmd

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"

	"github.com/go-monolith/ghtmx/cmd/ghtmx/lspcmd/httpdebug"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/lspcmd/pls"
	"github.com/go-monolith/ghtmx/cmd/ghtmx/lspcmd/proxy"
	"github.com/go-monolith/ghtmx/internal/format"
	"github.com/go-monolith/ghtmx/internal/lsp/jsonrpc2"
	"github.com/go-monolith/ghtmx/internal/lsp/protocol"

	_ "net/http/pprof"
)

type Arguments struct {
	Log           string
	GoplsLog      string
	GoplsRPCTrace bool
	GoplsRemote   string
	// PPROF sets whether to start a profiling server on localhost:9999
	PPROF bool
	// HTTPDebug sets the HTTP endpoint to listen on. Leave empty for no web debug.
	HTTPDebug string
	// NoPreload disables preloading of templ files on server startup (useful for large monorepos)
	NoPreload    bool
	FormatConfig format.Config
}

func Run(stdin io.Reader, stdout, stderr io.Writer, args Arguments) (err error) {
	ctx := context.Background()
	ctx, cancel := context.WithCancel(ctx)
	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, os.Interrupt)
	defer func() {
		signal.Stop(signalChan)
		cancel()
	}()
	if args.PPROF {
		go func() {
			_ = http.ListenAndServe("localhost:9999", nil)
		}()
	}
	go func() {
		select {
		case <-signalChan: // First signal, cancel context.
			cancel()
		case <-ctx.Done():
		}
		<-signalChan // Second signal, hard exit.
		os.Exit(2)
	}()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if args.Log != "" {
		file, err := os.OpenFile(args.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if err != nil {
			return fmt.Errorf("failed to open log file: %w", err)
		}
		defer func() {
			_ = file.Close()
		}()

		// Create a new logger with a file writer
		log = slog.New(slog.NewJSONHandler(file, nil))
		log.Debug("Logging to file", slog.String("file", args.Log))
	}
	templStream := jsonrpc2.NewStream(newStdRwc(log, "templStream", stdout, stdin))
	return run(ctx, log, templStream, args)
}

func run(ctx context.Context, log *slog.Logger, templStream jsonrpc2.Stream, args Arguments) (err error) {
	log.Info("lsp: starting up...")
	defer func() {
		if r := recover(); r != nil {
			log.Error("handled panic", slog.Any("recovered", r))
		}
	}()

	log.Info("lsp: starting gopls...")
	goplsLocation, err := pls.FindGopls()
	if err != nil {
		log.Error("failed to find gopls", slog.Any("error", err))
		os.Exit(1)
	}
	log.Info("found gopls", slog.String("location", goplsLocation))

	goplsVersion, err := pls.GoplsVersion(goplsLocation)
	if err != nil {
		log.Warn("could not determine gopls version", slog.Any("error", err))
	} else {
		log.Info("gopls version", slog.String("version", goplsVersion))
	}

	cache := proxy.NewSourceMapCache()
	diagnosticCache := proxy.NewDiagnosticCache()

	log.Info("creating gopls client")
	clientProxy, clientInit := proxy.NewClient(log, cache, diagnosticCache)
	// resilient supervises gopls (FR-085): a crash degrades embedded-Go
	// features only — .ghtmx diagnostics keep working — while gopls
	// restarts with backoff. Each spawn's exit hook carries its own
	// generation, so a superseded process can never take down its
	// successor. gopls connection death is NOT a session shutdown signal.
	var resilient *pls.Resilient
	spawnGopls := func(spawnCtx context.Context, generation int64) (protocol.Server, io.Closer, error) {
		_, newRwc, err := pls.NewGopls(spawnCtx, log, pls.Options{
			Log:      args.GoplsLog,
			RPCTrace: args.GoplsRPCTrace,
			Remote:   args.GoplsRemote,
			OnExit: func() {
				resilient.MarkDownIf(generation)
			},
		})
		if err != nil {
			return nil, nil, err
		}
		_, conn, srv := protocol.NewClient(spawnCtx, clientProxy, jsonrpc2.NewStream(newRwc), log)
		return srv, conn, nil
	}
	resilient = pls.NewResilient(log, spawnGopls)
	if err := resilient.Start(ctx); err != nil {
		log.Error("failed to start gopls", slog.Any("error", err))
		os.Exit(1)
	}
	defer func() {
		if closeErr := resilient.Close(); closeErr != nil {
			log.Error("failed to close gopls connection", slog.Any("error", closeErr))
		}
	}()

	log.Info("creating proxy")
	// Create the proxy to sit between.
	serverProxy := proxy.NewServer(log, resilient, cache, diagnosticCache, args.NoPreload, args.FormatConfig)
	// After a restart, gopls needs the session re-established: the
	// original initialize plus every open generated document.
	resilient.SetReplay(func(replayCtx context.Context, target protocol.Server) error {
		if params := serverProxy.LastInitializeParams(); params != nil {
			if _, err := target.Initialize(replayCtx, params); err != nil {
				return err
			}
			if err := target.Initialized(replayCtx, &protocol.InitializedParams{}); err != nil {
				return err
			}
		}
		// Loop until the open set is stable: documents opened while the
		// replay runs join it; anything landing after the final pass is
		// covered by the supervisor's buffered notifications.
		opened := map[protocol.DocumentURI]bool{}
		for range 5 {
			progressed := false
			for _, item := range serverProxy.OpenGoDocuments() {
				if opened[item.URI] {
					continue
				}
				if err := target.DidOpen(replayCtx, &protocol.DidOpenTextDocumentParams{TextDocument: item}); err != nil {
					return err
				}
				opened[item.URI] = true
				progressed = true
			}
			if !progressed {
				break
			}
		}
		return nil
	})
	serverProxy.GoplsPath = goplsLocation
	serverProxy.GoplsVersion = goplsVersion

	// Create templ server.
	log.Info("creating templ server")
	_, templConn, templClient := protocol.NewServer(context.Background(), serverProxy, templStream, log)
	defer func() {
		if err = templConn.Close(); err != nil {
			log.Error("failed to close templ connection", slog.Any("error", err))
		}
	}()

	// Allow both the server and the client to initiate outbound requests.
	clientInit(templClient)

	// Start the web server if required.
	if args.HTTPDebug != "" {
		log.Info("starting debug http server", slog.String("addr", args.HTTPDebug))
		h := httpdebug.NewHandler(log, serverProxy)
		go func() {
			if err := http.ListenAndServe(args.HTTPDebug, h); err != nil {
				log.Error("web server failed", slog.Any("error", err))
			}
		}()
	}

	log.Info("listening")

	select {
	case <-ctx.Done():
		log.Info("context closed")
	case <-templConn.Done():
		log.Info("templConn closed")

	}
	log.Info("shutdown complete")
	return
}
