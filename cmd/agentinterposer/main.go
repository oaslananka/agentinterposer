package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oaslananka/agentinterposer/internal/config"
	"github.com/oaslananka/agentinterposer/internal/gateway"
)

func main() {
	cfg, err := config.LoadFromEnv()
	if err != nil {
		slog.Error("invalid configuration", "error", err)
		os.Exit(2)
	}

	handler, err := newHandler(cfg)
	if err != nil {
		slog.Error("initialize gateway", "error", err)
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, cfg.ListenAddr, handler); err != nil {
		slog.Error("gateway stopped", "error", err)
		os.Exit(1)
	}
}

func newHandler(cfg config.Config) (http.Handler, error) {
	return gateway.NewHandler(gateway.Config{
		UpstreamURL:         cfg.UpstreamURL,
		UpstreamBearerToken: cfg.UpstreamBearerToken,
		MaxConcurrent:       cfg.MaxConcurrent,
		MaxRetries:          cfg.MaxRetries,
		RetryBaseDelay:      cfg.RetryBaseDelay,
		MaxRequestBytes:     cfg.MaxRequestBytes,
	})
}

func run(ctx context.Context, listenAddr string, handler http.Handler) error {
	server := &http.Server{
		Addr:              listenAddr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}

	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("graceful shutdown failed", "error", err)
		}
	}()

	slog.Info("AgentInterposer listening", "addr", listenAddr)
	err := server.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		<-shutdownDone
		return nil
	}
	return err
}
