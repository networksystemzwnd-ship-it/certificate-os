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
	DatabaseURL    string // runtime connection: MUST be a restricted role in production (ADR-001)
	MigrateURL     string // owner connection used only for migrations and role provisioning
	AppDBUser      string // login role created by `api db provision-runtime-role`
	AppDBPassword  string // used only by provisioning; never logged
	MigrateOnStart bool
	WebOrigin      string        // the single allowed browser origin (CORS/CSRF), e.g. http://localhost:5173
	SessionTTL     time.Duration // absolute session lifetime
	CookieSecure   bool          // derived: true in production

	PublicBaseURL    string // origin used in certificate QR codes (verification links); defaults to WebOrigin
	PDFChromePath    string // absolute path of a pinned headless Chromium; empty disables certificate issuing
	PDFNoSandbox     bool   // pass --no-sandbox to Chromium (only for containers running as root)
	PDFMaxConcurrent int    // simultaneous PDF renders
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

	cfg.PublicBaseURL = strings.TrimRight(get("PUBLIC_BASE_URL", cfg.WebOrigin), "/")
	if u, err := url.Parse(cfg.PublicBaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		errs = append(errs, errors.New("PUBLIC_BASE_URL must be an origin like https://certs.example.com (scheme and host only)"))
	} else if cfg.Env == "production" && u.Scheme != "https" {
		errs = append(errs, errors.New("PUBLIC_BASE_URL must use https in production (it is printed in certificate QR codes)"))
	}
	cfg.PDFChromePath = get("PDF_CHROME_PATH", "")
	if cfg.PDFChromePath != "" && !strings.HasPrefix(cfg.PDFChromePath, "/") {
		errs = append(errs, errors.New("PDF_CHROME_PATH must be an absolute path"))
	}
	noSandbox, err := strconv.ParseBool(get("PDF_CHROME_NO_SANDBOX", "false"))
	if err != nil {
		errs = append(errs, errors.New("PDF_CHROME_NO_SANDBOX must be a boolean"))
	}
	cfg.PDFNoSandbox = noSandbox
	conc, err := strconv.Atoi(get("PDF_MAX_CONCURRENT", "2"))
	if err != nil || conc < 1 || conc > 16 {
		errs = append(errs, errors.New("PDF_MAX_CONCURRENT must be an integer between 1 and 16"))
	}
	cfg.PDFMaxConcurrent = conc

	hours, err := strconv.Atoi(get("SESSION_TTL_HOURS", "12"))
	if err != nil || hours < 1 || hours > 24*30 {
		errs = append(errs, errors.New("SESSION_TTL_HOURS must be an integer between 1 and 720"))
	}
	cfg.SessionTTL = time.Duration(hours) * time.Hour
	cfg.CookieSecure = cfg.Env == "production"

	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	cfg.MigrateURL = get("MIGRATE_DATABASE_URL", cfg.DatabaseURL)
	cfg.AppDBUser = get("APP_DB_USER", "certos_app")
	cfg.AppDBPassword = get("APP_DB_PASSWORD", "")
	if !ValidRoleName(cfg.AppDBUser) {
		errs = append(errs, errors.New("APP_DB_USER must be 3-40 characters: lowercase letters, digits and underscores, starting with a letter"))
	}
	if cfg.Env == "production" && (cfg.MigrateURL == cfg.DatabaseURL) {
		errs = append(errs, errors.New("MIGRATE_DATABASE_URL must be set to the owner connection and differ from DATABASE_URL in production (the runtime role must not own the schema)"))
	}
	if cfg.Env == "production" && cfg.MigrateOnStart {
		errs = append(errs, errors.New("MIGRATE_ON_START must not be enabled in production; run migrations as a reviewed step"))
	}

	if len(errs) > 0 {
		return Config{}, errors.Join(errs...)
	}
	return cfg, nil
}

// ValidRoleName reports whether s is a safe PostgreSQL role name for the provisioning command.
func ValidRoleName(s string) bool {
	if len(s) < 3 || len(s) > 40 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			return false
		}
	}
	return true
}
