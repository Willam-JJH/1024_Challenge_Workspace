// Command api 是 Go 版后端的入口，最终会接管 Python 实现。
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

	"challenge-api/internal/api"
	"challenge-api/internal/config"
	"challenge-api/internal/db"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	cfg := config.Load()

	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	switch command {
	case "serve", "migrate", "create-admin":
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q，可用：serve / migrate / create-admin\n", command)
		os.Exit(2)
	}

	pools, err := db.Open(context.Background(), cfg.DatabaseURL, cfg.ReadDatabaseURL, cfg.DBMaxConns)
	if err != nil {
		logger.Error("database setup failed", "error", err)
		os.Exit(1)
	}
	defer pools.Close()

	// 一次性的运维命令：跑完就退出，不起 HTTP 服务。
	switch command {
	case "migrate":
		runMigrate(pools, logger)
		return
	case "create-admin":
		runCreateAdmin(pools, logger)
		return
	}

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
