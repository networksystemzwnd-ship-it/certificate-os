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

func TestWebOriginAndSessionDefaultsAndValidation(t *testing.T) {
	cfg, err := Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil || cfg.WebOrigin != "http://localhost:5173" || cfg.SessionTTL.Hours() != 12 || cfg.CookieSecure {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	for _, bad := range []string{"localhost:5173", "http://x/path", "ftp://x", "https://x?y=1"} {
		if _, err := Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://x", "WEB_ORIGIN": bad})); err == nil {
			t.Errorf("accepted WEB_ORIGIN %q", bad)
		}
	}
	if _, err := Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://x", "SESSION_TTL_HOURS": "0"})); err == nil {
		t.Error("accepted zero session ttl")
	}
}

func TestProductionRequiresHTTPSOriginAndSecureCookies(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x", "MIGRATE_DATABASE_URL": "postgres://owner", "APP_ENV": "production"}
	if _, err := Load(lookupFrom(base)); err == nil {
		t.Fatal("production with default http origin must fail")
	}
	base["WEB_ORIGIN"] = "https://app.example.com/"
	cfg, err := Load(lookupFrom(base))
	if err != nil || !cfg.CookieSecure || cfg.WebOrigin != "https://app.example.com" {
		t.Fatalf("%+v %v", cfg, err)
	}
}

func TestMigrateURLDefaultsAndProductionRequiresSeparateOwner(t *testing.T) {
	cfg, err := Load(lookupFrom(map[string]string{"DATABASE_URL": "postgres://app"}))
	if err != nil || cfg.MigrateURL != "postgres://app" || cfg.AppDBUser != "certos_app" {
		t.Fatalf("dev defaults: %+v %v", cfg, err)
	}
	prod := map[string]string{"APP_ENV": "production", "WEB_ORIGIN": "https://app.example.com", "DATABASE_URL": "postgres://app"}
	if _, err := Load(lookupFrom(prod)); err == nil || !strings.Contains(err.Error(), "MIGRATE_DATABASE_URL") {
		t.Fatalf("production without owner URL must fail, got %v", err)
	}
	prod["MIGRATE_DATABASE_URL"] = "postgres://app"
	if _, err := Load(lookupFrom(prod)); err == nil {
		t.Fatal("owner URL equal to runtime URL must fail in production")
	}
	prod["MIGRATE_DATABASE_URL"] = "postgres://owner"
	if cfg, err := Load(lookupFrom(prod)); err != nil || cfg.MigrateURL != "postgres://owner" {
		t.Fatalf("valid production config rejected: %v", err)
	}
}

func TestValidRoleName(t *testing.T) {
	for _, ok := range []string{"certos_app", "app_1", "abc"} {
		if !ValidRoleName(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "ab", "1abc", "Certos", "a-b-c", `x"; DROP ROLE y; --`, "postgres user", strings.Repeat("a", 41)} {
		if ValidRoleName(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestCertificateRenderingSettings(t *testing.T) {
	base := map[string]string{"DATABASE_URL": "postgres://x"}
	with := func(extra map[string]string) (Config, error) {
		m := map[string]string{}
		for k, v := range base {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return Load(func(k string) (string, bool) { v, ok := m[k]; return v, ok })
	}
	cfg, err := with(nil)
	if err != nil || cfg.PublicBaseURL != "http://localhost:5173" || cfg.PDFChromePath != "" || cfg.PDFNoSandbox || cfg.PDFMaxConcurrent != 2 {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	cfg, err = with(map[string]string{"PUBLIC_BASE_URL": "https://certs.example.com/", "PDF_CHROME_PATH": "/usr/bin/chromium", "PDF_CHROME_NO_SANDBOX": "true", "PDF_MAX_CONCURRENT": "4"})
	if err != nil || cfg.PublicBaseURL != "https://certs.example.com" || cfg.PDFChromePath != "/usr/bin/chromium" || !cfg.PDFNoSandbox || cfg.PDFMaxConcurrent != 4 {
		t.Fatalf("custom: %+v %v", cfg, err)
	}
	for name, env := range map[string]map[string]string{
		"relative chrome path": {"PDF_CHROME_PATH": "chromium"},
		"bad bool":             {"PDF_CHROME_NO_SANDBOX": "maybe"},
		"zero concurrency":     {"PDF_MAX_CONCURRENT": "0"},
		"huge concurrency":     {"PDF_MAX_CONCURRENT": "99"},
		"base url with path":   {"PUBLIC_BASE_URL": "https://certs.example.com/verify"},
		"base url not http":    {"PUBLIC_BASE_URL": "ftp://certs.example.com"},
	} {
		if _, err := with(env); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	prod := map[string]string{"APP_ENV": "production", "WEB_ORIGIN": "https://app.example.com", "MIGRATE_DATABASE_URL": "postgres://owner", "PUBLIC_BASE_URL": "http://certs.example.com"}
	if _, err := with(prod); err == nil {
		t.Error("http PUBLIC_BASE_URL accepted in production")
	}
}
