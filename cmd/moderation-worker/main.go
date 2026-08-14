package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/visoraft/visoraft/internal/config"
	"github.com/visoraft/visoraft/internal/cookieprofiles"
	"github.com/visoraft/visoraft/internal/database"
	"github.com/visoraft/visoraft/internal/logging"
	"github.com/visoraft/visoraft/internal/moderation"
	"github.com/visoraft/visoraft/internal/objectstorage"
	"github.com/visoraft/visoraft/internal/settings"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("moderation worker stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("moderation-worker")
	if err != nil {
		return err
	}
	logger := logging.New(cfg.ServiceName, cfg.LogLevel)
	slog.SetDefault(logger)
	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := database.Migrate(ctx, pool, logger); err != nil {
		return err
	}
	secretBox, err := cookieprofiles.NewSecretBox(cfg.CookieEncryptionKey)
	if err != nil {
		return err
	}
	settingsService := settings.NewService(
		settings.NewPostgresStore(pool),
		secretBox,
	)
	storage, err := objectstorage.New(objectstorage.Config{
		Endpoint:       cfg.S3Endpoint,
		PublicEndpoint: cfg.S3PublicEndpoint,
		AccessKey:      cfg.S3AccessKey,
		SecretKey:      cfg.S3SecretKey,
		Region:         cfg.S3Region,
	})
	if err != nil {
		return err
	}
	worker := moderation.NewWorker(
		cfg.RabbitMQURL,
		cfg.EventExchange,
		settingsService,
		storage,
		logger,
	)
	logger.Info("moderation worker started")
	return worker.Run(ctx)
}
