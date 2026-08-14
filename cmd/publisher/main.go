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
	"github.com/visoraft/visoraft/internal/identity"
	"github.com/visoraft/visoraft/internal/logging"
	"github.com/visoraft/visoraft/internal/objectstorage"
	"github.com/visoraft/visoraft/internal/publishing"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("publisher stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("publisher")
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
	cookieService := cookieprofiles.NewService(
		cookieprofiles.NewPostgresStore(pool),
		secretBox,
		cookieprofiles.NewHTTPCookieCloudClient(),
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
	owner, err := identity.NewUUID()
	if err != nil {
		return err
	}
	worker := publishing.NewWorker(
		publishing.NewPostgresStore(pool),
		cookieService,
		storage,
		owner,
		cfg.PollInterval,
		logger,
		publishing.NewFixtureGateway(publishing.PlatformAcFun),
		publishing.NewFixtureGateway(publishing.PlatformBilibili),
		publishing.NewAcFunWebAdapter(),
		publishing.NewBilibiliWebAdapter(),
	)
	logger.Info("publisher started", "owner", owner)
	return worker.Run(ctx)
}
