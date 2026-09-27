// Command server is the Wi-Fi diagnostic server entrypoint.
package main

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"wifi-diagnostics/internal/config"
	"wifi-diagnostics/internal/diag"
	"wifi-diagnostics/internal/httpapi"
	"wifi-diagnostics/internal/session"
	"wifi-diagnostics/internal/store"
	"wifi-diagnostics/internal/unifi"
)

//go:generate echo "frontend must be built before embedding"

func main() {
	// Docker HEALTHCHECK calls the binary with -healthcheck.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		if err := doHealthCheck(); err != nil {
			fmt.Fprintf(os.Stderr, "healthcheck failed: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	logger := newLogger(cfg.LogLevel)
	slog.SetDefault(logger)

	logger.Info("wifi-diagnostics starting",
		"port", cfg.ServerPort,
		"unifi_url", cfg.UnifiURL,
		"unifi_site", cfg.UnifiSite,
		"data_dir", cfg.DataDir,
	)

	// Persistence
	st, err := store.Open(cfg.DataDir, cfg.NoPersistence, logger)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	defer st.Close()

	// UniFi client
	var uc unifi.UniFiClient
	if cfg.UnifiConfigured() {
		uc = unifi.NewRealClient(unifi.RealConfig{
			BaseURL:   cfg.UnifiURL,
			Username:  cfg.UnifiUsername,
			Password:  cfg.UnifiPassword,
			VerifyTLS: cfg.UnifiVerifyTLS,
			Site:      cfg.UnifiSite,
		}, logger)
		logger.Info("unifi: configured", "url", cfg.UnifiURL, "site", cfg.UnifiSite)
	} else {
		logger.Warn("unifi: not configured — running with mock client")
		uc = unifi.NewMockClient()
	}

	// Session manager
	sessMgr := session.NewManager(uc, logger)

	// Diagnostic WS handler
	diagH := diag.NewHandler(logger)

	// Frontend FS
	frontendFS := buildFrontendFS()

	// HTTP server
	srv := httpapi.NewServer(logger, diagH, sessMgr, st, uc, cfg.TrustedProxies, frontendFS, cfg)

	httpSrv := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.ServerPort),
		Handler:      srv.Handler(),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // streaming tests need unlimited write time
		IdleTimeout:  120 * time.Second,
	}

	// Start retention purge goroutine.
	if !cfg.NoPersistence {
		go runRetentionPurge(st, cfg, logger)
	}

	// Graceful shutdown on SIGTERM / SIGINT.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		logger.Info("http: listening", "addr", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("http: shutdown error", "error", err)
	}
	logger.Info("shutdown complete")
	return nil
}

func newLogger(level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

func buildFrontendFS() http.FileSystem {
	// Production builds embed the frontend via embed_prod.go (build tag: prod).
	if embeddedFrontend != nil {
		sub, err := fs.Sub(embeddedFrontend, "frontend/dist")
		if err == nil {
			return http.FS(sub)
		}
	}
	// Dev fallback: serve from disk if frontend/dist exists.
	if _, err := os.Stat("frontend/dist"); err == nil {
		slog.Default().Info("frontend: serving from frontend/dist (dev mode)")
		return http.Dir("frontend/dist")
	}
	return http.Dir(".")
}

func runRetentionPurge(st *store.Store, cfg *config.Config, logger *slog.Logger) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := st.Purge(ctx, cfg.RetentionDuration(), cfg.MaxStoredSessions); err != nil {
			logger.Warn("store: purge failed", "error", err)
		}
		cancel()
	}
}

func doHealthCheck() error {
	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "8080"
	}
	resp, err := http.Get("http://localhost:" + port + "/healthz") //nolint:noctx
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz returned %d", resp.StatusCode)
	}
	return nil
}
