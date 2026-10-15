// Package config loads and validates process configuration from the environment.
// Missing or invalid required configuration fails startup (fail safe).
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env            string // development | test | production
	Port           int
	LogLevel       string
	DatabaseURL    string
	MigrateOnStart bool
	WebOrigin      string        // the single allowed browser origin (CORS/CSRF), e.g. http://localhost:5173
	SessionTTL     time.Duration // absolute session lifetime
	CookieSecure   bool          // derived: true in production
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

	cfg.WebOrigin = strings.TrimRight(get("WEB_ORIGIN", "http://localhost:5173"), "/")
	if u, err := url.Parse(cfg.WebOrigin); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		errs = append(errs, errors.New("WEB_ORIGIN must be an origin like https://app.example.com (scheme and host only)"))
	} else if cfg.Env == "production" && u.Scheme != "https" {
		errs = append(errs, errors.New("WEB_ORIGIN must use https in production"))
	}

	hours, err := strconv.Atoi(get("SESSION_TTL_HOURS", "12"))
	if err != nil || hours < 1 || hours > 24*30 {
		errs = append(errs, errors.New("SESSION_TTL_HOURS must be an integer between 1 and 720"))
	}
	cfg.SessionTTL = time.Duration(hours) * time.Hour
	cfg.CookieSecure = cfg.Env == "production"

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
