// Package config loads and validates process configuration from the environment.
// Missing or invalid required configuration fails startup (fail safe).
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type Config struct {
	Env            string // development | test | production
	Port           int
	LogLevel       string
	DatabaseURL    string
	MigrateOnStart bool
}

// Load reads configuration using the provided lookup function (os.LookupEnv in production).
func Load(lookup func(string) (string, bool)) (Config, error) {
	get := func(key, def string) string {
		if v, ok := lookup(key); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return def
	}

	var errs []error
	cfg := Config{
		Env:         get("APP_ENV", "development"),
		LogLevel:    get("LOG_LEVEL", "info"),
		DatabaseURL: get("DATABASE_URL", ""),
	}

	switch cfg.Env {
	case "development", "test", "production":
	default:
		errs = append(errs, fmt.Errorf("APP_ENV must be development, test or production (got %q)", cfg.Env))
	}

	port, err := strconv.Atoi(get("API_PORT", "8080"))
	if err != nil || port < 1 || port > 65535 {
		errs = append(errs, errors.New("API_PORT must be an integer between 1 and 65535"))
	}
	cfg.Port = port

	migrate, err := strconv.ParseBool(get("MIGRATE_ON_START", "false"))
	if err != nil {
		errs = append(errs, errors.New("MIGRATE_ON_START must be a boolean"))
	}
	cfg.MigrateOnStart = migrate

	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if cfg.Env == "production" && cfg.MigrateOnStart {
		errs = append(errs, errors.New("MIGRATE_ON_START must not be enabled in production; run migrations as a reviewed step"))
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}
