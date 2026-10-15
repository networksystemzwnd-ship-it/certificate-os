package config

import (
	"strings"
	"testing"
)

func lookupFrom(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8080 || cfg.Env != "development" || cfg.MigrateOnStart {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadMissingDatabaseURLFailsSafely(t *testing.T) {
	_, err := Load(lookupFrom(nil))
	if err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("expected DATABASE_URL error, got %v", err)
	}
}

func TestLoadRejectsBadValues(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{
		"DATABASE_URL": "postgres://x", "API_PORT": "abc", "APP_ENV": "staging",
	}))
	if err == nil || !strings.Contains(err.Error(), "API_PORT") || !strings.Contains(err.Error(), "APP_ENV") {
		t.Fatalf("expected both errors, got %v", err)
	}
}

func TestProductionForbidsMigrateOnStart(t *testing.T) {
	_, err := Load(lookupFrom(map[string]string{
		"DATABASE_URL": "postgres://x", "APP_ENV": "production", "MIGRATE_ON_START": "true",
	}))
	if err == nil {
		t.Fatal("expected error")
	}
}
