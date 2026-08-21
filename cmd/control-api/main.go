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

	"github.com/visoraft/visoraft/internal/buildinfo"
	"github.com/visoraft/visoraft/internal/config"
	"github.com/visoraft/visoraft/internal/cookieprofiles"
	"github.com/visoraft/visoraft/internal/database"
	"github.com/visoraft/visoraft/internal/httpapi"
	"github.com/visoraft/visoraft/internal/logging"
	"github.com/visoraft/visoraft/internal/medialibrary"
	"github.com/visoraft/visoraft/internal/monitors"
	"github.com/visoraft/visoraft/internal/objectstorage"
	"github.com/visoraft/visoraft/internal/publishing"
	"github.com/visoraft/visoraft/internal/reviews"
	appsettings "github.com/visoraft/visoraft/internal/settings"
	"github.com/visoraft/visoraft/internal/tasks"
)

func main() {
	if err := run(); err != nil {
		slog.Error("control api stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("control-api")
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

	store := tasks.NewPostgresStore(pool)
	service := tasks.NewService(store)
	secretBox, err := cookieprofiles.NewSecretBox(cfg.CookieEncryptionKey)
	if err != nil {
		return err
	}
	cookieService := cookieprofiles.NewService(
		cookieprofiles.NewPostgresStore(pool),
		secretBox,
		cookieprofiles.NewHTTPCookieCloudClient(),
	)
	settingsService := appsettings.NewService(
		appsettings.NewPostgresStore(pool),
		secretBox,
	)
	objectStorage, err := objectstorage.New(objectstorage.Config{
		Endpoint:       cfg.S3Endpoint,
		PublicEndpoint: cfg.S3PublicEndpoint,
		AccessKey:      cfg.S3AccessKey,
		SecretKey:      cfg.S3SecretKey,
		Region:         cfg.S3Region,
	})
	if err != nil {
		return err
	}
	libraryService := medialibrary.NewService(
		pool,
		objectStorage,
		cfg.LibraryRoot,
		cfg.LibraryHostPath,
		logger,
	)
	go libraryService.Run(ctx, 5*time.Second)
	publishStore := publishing.NewPostgresStore(pool)
	publishService := publishing.NewService(
		publishStore,
		cookieService,
		publishing.NewFixtureGateway(publishing.PlatformAcFun),
		publishing.NewFixtureGateway(publishing.PlatformBilibili),
		publishing.NewAcFunWebAdapter(),
		publishing.NewBilibiliWebAdapter(),
	)
	publishService.ConfigureCoverImport(objectStorage, cfg.S3Bucket)
	reviewService := reviews.NewService(pool, store, service, publishStore)
	reviewService.ConfigureSubtitleArtifacts(objectStorage, cfg.S3Bucket)
	monitorService := monitors.NewService(
		monitors.NewPostgresStore(pool),
		settingsService,
		service,
	)
	api := httpapi.NewServer(
		service,
		cookieService,
		settingsService,
		reviewService,
		monitorService,
		publishService,
		objectStorage,
		libraryService,
		pool,
		logger,
		buildinfo.Version,
		cfg.WorkerToken,
	)

	server := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	errChannel := make(chan error, 1)
	go func() {
		logger.Info("control api listening", "address", cfg.HTTPAddr, "version", buildinfo.Version)
		errChannel <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
