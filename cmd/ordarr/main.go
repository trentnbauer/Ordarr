package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/trentnbauer/Ordarr/internal/app"
	"github.com/trentnbauer/Ordarr/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("Ordarr stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	c, err := app.LoadConfig()
	if err != nil {
		return err
	}
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		host, port, err := net.SplitHostPort(c.Address)
		if err != nil {
			return err
		}
		if host == "" || host == "0.0.0.0" {
			host = "127.0.0.1"
		}
		if host == "::" {
			host = "::1"
		}
		client := http.Client{Timeout: 3 * time.Second}
		res, err := client.Get("http://" + net.JoinHostPort(host, port) + "/healthz")
		if err != nil {
			return err
		}
		defer res.Body.Close()
		if res.StatusCode != 200 {
			return fmt.Errorf("healthcheck: HTTP %d", res.StatusCode)
		}
		return nil
	}
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	s, err := store.Open(filepath.Join(c.DataDir, "ordarr.db"))
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer s.DB.Close()
	a, err := app.New(s, c, logger)
	if err != nil {
		return err
	}
	server := http.Server{Addr: c.Address, Handler: a.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { logger.Info("Ordarr listening", "address", c.Address); done <- server.ListenAndServe() }()
	select {
	case err := <-done:
		if err != http.ErrServerClosed {
			return err
		}
		return nil
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			server.Close()
			return err
		}
		logger.Info("Ordarr stopped gracefully")
		return nil
	}
}
