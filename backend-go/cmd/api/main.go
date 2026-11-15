// Command api 是 Go 版后端的入口，最终会接管 Python 实现。
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

	"challenge-api/internal/api"
	"challenge-api/internal/config"
	"challenge-api/internal/db"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Load()

	pools, err := db.Open(context.Background(), cfg.DatabaseURL, cfg.ReadDatabaseURL, cfg.DBMaxConns)
	if err != nil {
		logger.Error("database setup failed", "error", err)
		os.Exit(1)
	}
	defer pools.Close()

	handler, appServer, err := api.New(cfg, pools, logger)
	if err != nil {
		logger.Error("server setup failed", "error", err)
		os.Exit(1)
	}

	workerCtx, stopWorkers := context.WithCancel(context.Background())
	defer stopWorkers()
	appServer.StartWorkers(workerCtx)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("api listening", "addr", server.Addr, "environment", cfg.Environment)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server failed", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("shutdown", "error", err)
	}
	logger.Info("stopped")
}
