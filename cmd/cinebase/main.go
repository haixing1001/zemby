package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/haixing1001/cinebase/internal/auth"
	"github.com/haixing1001/cinebase/internal/config"
	"github.com/haixing1001/cinebase/internal/database"
	"github.com/haixing1001/cinebase/internal/httpapi"
	"github.com/haixing1001/cinebase/internal/scanner"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return err
	}
	if err := os.Chmod(cfg.DataDir, 0o700); err != nil {
		return err
	}
	db, err := database.Open(cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.RequeueInterruptedJobs(context.Background()); err != nil {
		return err
	}
	authManager, err := auth.New(cfg.AdminPassword, cfg.DataDir)
	if err != nil {
		return err
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	worker := scanner.New(db, logger)
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		worker.Run(rootCtx)
	}()

	api := httpapi.New(db, authManager, cfg.MediaRoots, cfg.MaxStreams, logger)
	server := &http.Server{
		Addr:              cfg.Address,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0, // Streaming responses can be much longer than normal API calls.
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("server listening", "address", cfg.Address)
		serveErr <- server.ListenAndServe()
	}()
	select {
	case err := <-serveErr:
		stop()
		_ = server.Close()
		<-workerDone
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-rootCtx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			_ = server.Close()
			<-workerDone
			return err
		}
		<-workerDone
		return nil
	}
}

func healthcheck() int {
	address := os.Getenv("CINEBASE_ADDR")
	if address == "" {
		address = ":8097"
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil {
		return 1
	}
	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort("127.0.0.1", port) + "/api/health")
	if err != nil {
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 1
	}
	return 0
}
