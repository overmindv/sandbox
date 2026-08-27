package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"overmindv/sandbox/internal/app"
	"overmindv/sandbox/internal/config"
	"overmindv/sandbox/internal/observability"
)

func main() {
	configPath := flag.String("config", "", "path to sandbox worker YAML config")
	flag.Parse()

	if *configPath != "" {
		if err := os.Setenv("SANDBOX_CONFIG_FILE", *configPath); err != nil {
			logger := observability.NewLogger("error", "console", os.Stderr)
			logger.Error(context.Background(), "config path setup failed", "error", err)
			os.Exit(1)
		}
	}

	cfg, err := config.LoadFromEnv()
	if err != nil {
		logger := observability.NewLogger("error", "console", os.Stderr)
		logger.Error(context.Background(), "config load failed", "error", err)
		os.Exit(1)
	}

	logger := observability.NewLogger(cfg.Observability.LogLevel, cfg.Observability.LogFormat, os.Stdout)
	worker, err := app.NewWorker(cfg, logger)
	if err != nil {
		logger.Error(context.Background(), "worker init failed", "error", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := worker.Run(ctx); err != nil && ctx.Err() == nil {
		logger.Error(ctx, "worker stopped with error", "error", err)
		os.Exit(1)
	}
}
