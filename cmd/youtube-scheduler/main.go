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
	"github.com/visoraft/visoraft/internal/monitors"
	appsettings "github.com/visoraft/visoraft/internal/settings"
	"github.com/visoraft/visoraft/internal/tasks"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("youtube scheduler stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("youtube-scheduler")
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
	settingsService := appsettings.NewService(
		appsettings.NewPostgresStore(pool),
		secretBox,
	)
	taskStore := tasks.NewPostgresStore(pool)
	taskService := tasks.NewService(taskStore)
	monitorStore := monitors.NewPostgresStore(pool)
	owner, err := identity.NewUUID()
	if err != nil {
		return err
	}
	scheduler := monitors.NewScheduler(
		monitorStore,
		monitors.NewDiscoverer(settingsService),
		taskService,
		logger,
		owner,
		cfg.PollInterval,
	)
	logger.Info("youtube scheduler started", "owner", owner)
	return scheduler.Run(ctx)
}
