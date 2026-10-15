package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/platform/config"
)

// Conspicuously non-production credentials for local development only.
const seedPassword = "local-dev-only-NOT-FOR-PRODUCTION"

var seedUsers = []struct {
	email string
	role  auth.PlatformRole
}{
	{"superadmin@local.test", auth.PlatformSuperAdmin},
	{"ops@local.test", auth.PlatformOps},
	{"owner@local.test", auth.PlatformNone}, // becomes an institute owner once memberships exist (COS-008)
}

// runSeed is idempotent and refuses to run in production.
func runSeed(ctx context.Context, cfg config.Config, svc *auth.Service, store auth.Store) error {
	if cfg.Env == "production" {
		return errors.New("seed is disabled when APP_ENV=production")
	}
	for _, s := range seedUsers {
		u, err := svc.Register(ctx, s.email, seedPassword)
		if errors.Is(err, auth.ErrEmailTaken) {
			if u, err = store.UserByEmail(ctx, s.email); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		if err := store.SetPlatformRole(ctx, u.ID, s.role); err != nil {
			return err
		}
		fmt.Printf("seeded %-24s role=%q\n", s.email, s.role)
	}
	fmt.Printf("local password for all seed users: %s\n", seedPassword)
	return nil
}

// runUser implements out-of-band administration (platform roles are never settable via the API).
func runUser(ctx context.Context, svc *auth.Service, store auth.Store, args []string) error {
	usage := errors.New("usage: api user grant-platform-role EMAIL super_admin|ops|none | api user suspend|reactivate EMAIL")
	if len(args) < 2 {
		return usage
	}
	email, ok := auth.NormalizeEmail(args[1])
	if !ok {
		return errors.New("invalid email")
	}
	u, err := store.UserByEmail(ctx, email)
	if err != nil {
		return fmt.Errorf("user lookup: %w", err)
	}
	switch args[0] {
	case "grant-platform-role":
		if len(args) != 3 {
			return usage
		}
		role := auth.PlatformRole(args[2])
		if args[2] == "none" {
			role = auth.PlatformNone
		}
		if !role.Valid() {
			return usage
		}
		if err := store.SetPlatformRole(ctx, u.ID, role); err != nil {
			return err
		}
		fmt.Printf("set platform role of %s to %q\n", email, role)
	case "suspend":
		if err := svc.SuspendUser(ctx, u.ID); err != nil {
			return err
		}
		fmt.Println("suspended", email)
	case "reactivate":
		if err := svc.ReactivateUser(ctx, u.ID); err != nil {
			return err
		}
		fmt.Println("reactivated", email)
	default:
		return usage
	}
	return nil
}
