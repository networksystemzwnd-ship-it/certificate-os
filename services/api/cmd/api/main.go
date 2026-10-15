// Command api runs the CertificateOS HTTP API and migration commands.
//
//	api            start the server
//	api migrate up|down|status
//	api seed                                   local dev accounts (refused in production)
//	api user grant-platform-role EMAIL ROLE    ROLE: super_admin | ops | none
//	api user suspend|reactivate EMAIL
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authhttp"
	"github.com/certificate-os/api/internal/auth/pgstore"
	"github.com/certificate-os/api/internal/platform/config"
	"github.com/certificate-os/api/internal/platform/health"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/platform/migrate"
	"github.com/certificate-os/api/migrations"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func newLogger(level, env string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts)).With("service", "api", "env", env)
}

func run(args []string) error {
	cfg, err := config.Load(os.LookupEnv)
	if err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}
	log := newLogger(cfg.LogLevel, cfg.Env)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("database config: %w", err)
	}
	defer pool.Close()

	migs, err := migrate.Load(migrations.FS)
	if err != nil {
		return err
	}
	runner := &migrate.Runner{Pool: pool, Migs: migs}

	if len(args) > 0 && args[0] == "migrate" {
		return runMigrate(ctx, runner, args[1:])
	}

	authSvc, err := auth.NewService(pgstore.New(pool), auth.NewHasher(), cfg.SessionTTL)
	if err != nil {
		return err
	}
	if len(args) > 0 {
		switch args[0] {
		case "seed":
			return runSeed(ctx, cfg, authSvc, pgstore.New(pool))
		case "user":
			return runUser(ctx, authSvc, pgstore.New(pool), args[1:])
		default:
			return fmt.Errorf("unknown command %q (expected: migrate, seed, user)", args[0])
		}
	}

	if cfg.MigrateOnStart {
		if err := waitForDB(ctx, pool, 30*time.Second); err != nil {
			return err
		}
		n, err := runner.Up(ctx)
		if err != nil {
			return err
		}
		log.Info("migrations applied on start", "count", n)
	}

	mux := http.NewServeMux()
	health.Register(mux, pool)
	authhttp.NewHandler(authSvc, authhttp.CookieConfig{Secure: cfg.CookieSecure, TTL: cfg.SessionTTL}, log).Register(mux)
	mux.HandleFunc("/", httpx.NotFound)

	srv := &http.Server{
		Addr:              ":" + strconv.Itoa(cfg.Port),
		Handler:           httpx.Chain(log, httpx.CORS(cfg.WebOrigin, httpx.CSRF(cfg.WebOrigin, mux))),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", srv.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
	return nil
}

func runMigrate(ctx context.Context, r *migrate.Runner, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: api migrate up|down|status")
	}
	switch args[0] {
	case "up":
		n, err := r.Up(ctx)
		fmt.Printf("applied %d migration(s)\n", n)
		return err
	case "down":
		n, err := r.Down(ctx)
		fmt.Printf("reverted %d migration(s)\n", n)
		return err
	case "status":
		st, err := r.Status(ctx)
		for _, s := range st {
			mark := "pending"
			if s.Applied {
				mark = "applied"
			}
			fmt.Printf("%04d  %-30s %s\n", s.Version, s.Name, mark)
		}
		return err
	default:
		return fmt.Errorf("unknown migrate command %q", args[0])
	}
}

func waitForDB(ctx context.Context, pool *pgxpool.Pool, d time.Duration) error {
	deadline := time.Now().Add(d)
	for {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := pool.Ping(pctx)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("database not reachable before timeout")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
