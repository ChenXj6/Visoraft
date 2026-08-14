package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/visoraft/visoraft/internal/config"
	"github.com/visoraft/visoraft/internal/database"
	"github.com/visoraft/visoraft/internal/logging"
	"github.com/visoraft/visoraft/internal/outbox"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("outbox dispatcher stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("outbox-dispatcher")
	if err != nil {
		return err
	}
	logger := logging.New(cfg.ServiceName, cfg.LogLevel)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool, logger); err != nil {
		return err
	}

	dispatcher := outbox.NewDispatcher(
		outbox.NewStore(pool),
		cfg.RabbitMQURL,
		cfg.EventExchange,
		cfg.PollInterval,
		logger,
	)
	return dispatcher.Run(ctx)
}
