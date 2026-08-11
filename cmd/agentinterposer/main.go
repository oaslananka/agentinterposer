package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/oaslananka/agentinterposer/internal/agentconfig"
	"github.com/oaslananka/agentinterposer/internal/config"
	"github.com/oaslananka/agentinterposer/internal/gateway"
)

func main() {
	if handled, exitCode := runConfigCommand(os.Args[1:], os.Stdout, os.Stderr); handled {
		if exitCode != 0 {
			os.Exit(exitCode)
		}
		return
	}

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

const defaultConfigGatewayURL = "http://127.0.0.1:11435"

func runConfigCommand(args []string, stdout, stderr io.Writer) (bool, int) {
	if len(args) == 0 || args[0] != "config" {
		return false, 0
	}
	if len(args) < 3 || len(args) > 4 {
		printConfigUsage(stderr)
		return true, 2
	}

	gatewayURL := defaultConfigGatewayURL
	if len(args) == 4 {
		gatewayURL = args[3]
	}

	var (
		output string
		err    error
	)
	switch args[1] {
	case "codex":
		output, err = agentconfig.RenderCodexConfig(args[2], gatewayURL)
	case "claude-code":
		output, err = agentconfig.RenderClaudeCodeEnv(args[2], gatewayURL)
	case "opencode":
		output, err = agentconfig.RenderOpenCodeConfig(args[2], gatewayURL)
	default:
		printConfigUsage(stderr)
		return true, 2
	}
	if err != nil {
		fmt.Fprintf(stderr, "invalid config request: %v\n", err)
		return true, 2
	}
	_, _ = io.WriteString(stdout, output)
	return true, 0
}

func printConfigUsage(w io.Writer) {
	_, _ = io.WriteString(w, "usage: agentinterposer config <codex|claude-code|opencode> <model> [gateway-url]\n")
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
