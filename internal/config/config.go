package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	ServiceName         string
	HTTPAddr            string
	DatabaseURL         string
	RabbitMQURL         string
	EventExchange       string
	LogLevel            string
	CookieEncryptionKey string
	WorkerToken         string
	S3Endpoint          string
	S3PublicEndpoint    string
	S3AccessKey         string
	S3SecretKey         string
	S3Region            string
	S3Bucket            string
	ShutdownTimeout     time.Duration
	PollInterval        time.Duration
}

func Load(serviceName string) (Config, error) {
	cfg := Config{
		ServiceName:   serviceName,
		HTTPAddr:      envOr("VISORAFT_HTTP_ADDR", ":8080"),
		DatabaseURL:   envOr("VISORAFT_DATABASE_URL", "postgres://visoraft:visoraft-local@localhost:5434/visoraft?sslmode=disable"),
		RabbitMQURL:   envOr("VISORAFT_RABBITMQ_URL", "amqp://visoraft:visoraft-local@localhost:5673/"),
		EventExchange: envOr("VISORAFT_EVENT_EXCHANGE", "visoraft.events"),
		LogLevel:      envOr("VISORAFT_LOG_LEVEL", "info"),
		CookieEncryptionKey: envOr(
			"VISORAFT_COOKIE_ENCRYPTION_KEY",
			"dmlzb3JhZnQtbG9jYWwtY29va2llLWtleS0wMDAwMDE=",
		),
		WorkerToken: envOr(
			"VISORAFT_WORKER_TOKEN",
			"visoraft-local-worker-token-2026",
		),
		S3Endpoint:       envOr("VISORAFT_S3_ENDPOINT", "http://localhost:8333"),
		S3PublicEndpoint: os.Getenv("VISORAFT_S3_PUBLIC_ENDPOINT"),
		S3AccessKey:      envOr("VISORAFT_S3_ACCESS_KEY", "visoraft-local"),
		S3SecretKey:      envOr("VISORAFT_S3_SECRET_KEY", "visoraft-local-secret"),
		S3Region:         envOr("VISORAFT_S3_REGION", "us-east-1"),
		S3Bucket:         envOr("VISORAFT_S3_BUCKET", "visoraft-media"),
		ShutdownTimeout:  15 * time.Second,
		PollInterval:     time.Second,
	}

	if raw := os.Getenv("VISORAFT_OUTBOX_POLL_MS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 100 || value > 60_000 {
			return Config{}, fmt.Errorf("VISORAFT_OUTBOX_POLL_MS must be between 100 and 60000")
		}
		cfg.PollInterval = time.Duration(value) * time.Millisecond
	}
	if len(cfg.WorkerToken) < 24 {
		return Config{}, fmt.Errorf("VISORAFT_WORKER_TOKEN must contain at least 24 characters")
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
