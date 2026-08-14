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
	"github.com/visoraft/visoraft/internal/moderation"
	"github.com/visoraft/visoraft/internal/pipeline"
	"github.com/visoraft/visoraft/internal/publishing"
	"github.com/visoraft/visoraft/internal/reviews"
	"github.com/visoraft/visoraft/internal/tasks"
	"github.com/visoraft/visoraft/internal/workflow"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("workflow consumer stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("workflow-consumer")
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

	taskStore := tasks.NewPostgresStore(pool)
	taskService := tasks.NewService(taskStore)
	publishStore := publishing.NewPostgresStore(pool)
	reviewService := reviews.NewService(
		pool,
		taskStore,
		taskService,
		publishStore,
	)
	pipelineService := pipeline.NewService(pool, reviewService)
	consumer := workflow.NewConsumer(
		cfg.RabbitMQURL,
		cfg.EventExchange,
		taskStore,
		moderation.NewPostgresStore(pool),
		pipelineService,
		logger,
	)
	return consumer.Run(ctx)
}
