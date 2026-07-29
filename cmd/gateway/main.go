// Command gateway is the entrypoint for the LLM Gateway service. It loads
// configuration, wires dependencies together, starts the HTTP server, and
// shuts down gracefully on SIGINT/SIGTERM.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Akashchandru613/llm-gateway/internal/config"
	"github.com/Akashchandru613/llm-gateway/internal/providers"
	"github.com/Akashchandru613/llm-gateway/internal/server"
)

func main() {
	// Structured JSON logging to stdout (12-factor: treat logs as an event
	// stream). slog is Go's standard structured logger.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("gateway exited with error", "error", err)
		os.Exit(1)
	}
}

// run wires dependencies and blocks until shutdown. Splitting this out of main
// keeps os.Exit in exactly one place (main) and lets the rest return errors
// normally — a common Go pattern for testable, lint-clean entrypoints.
func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Release mode quiets Gin's debug output; our slog logger is the source of
	// truth for logs.
	gin.SetMode(gin.ReleaseMode)

	provider := providers.NewOpenAIProvider(cfg.OpenAIAPIKey, cfg.OpenAIBaseURL, cfg.RequestTimeout)
	srv := server.New(cfg, provider, logger)

	httpServer := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: srv.Handler(),
		// We intentionally do NOT set WriteTimeout: it would cut off long-lived
		// SSE streams. ReadHeaderTimeout still guards against slow-loris clients
		// that open a connection but never finish sending headers.
		ReadHeaderTimeout: 10 * time.Second,
	}

	// signal.NotifyContext returns a context that is cancelled when the process
	// receives SIGINT or SIGTERM — the modern idiom for graceful shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Run the server in a goroutine so main can block on the signal context.
	// The buffered channel (size 1) lets the goroutine report a startup failure
	// without blocking if no one is reading yet.
	serverErr := make(chan error, 1)
	go func() {
		logger.Info("gateway listening", "addr", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// Block until either the server crashes or a shutdown signal arrives.
	select {
	case err := <-serverErr:
		return fmt.Errorf("server error: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received, draining in-flight requests")
	}

	// Report not-ready so Kubernetes / load balancers stop sending new traffic
	// here before we start draining in-flight requests.
	srv.SetReady(false)

	// Give in-flight requests up to 15s to finish before forcing exit.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown failed: %w", err)
	}
	logger.Info("gateway stopped cleanly")
	return nil
}
