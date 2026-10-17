package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/platform/config"
	"github.com/certificate-os/api/internal/tenancy"
)

// Conspicuously non-production credentials for local development only.
const seedPassword = "local-dev-only-NOT-FOR-PRODUCTION"

type seedMembership struct {
	email string
	role  auth.InstituteRole
}

var seedUsers = []struct {
	email string
	role  auth.PlatformRole
}{
	{"superadmin@local.test", auth.PlatformSuperAdmin},
	{"ops@local.test", auth.PlatformOps},
	{"owner@local.test", auth.PlatformNone},
	{"admin@local.test", auth.PlatformNone},
	{"staff@local.test", auth.PlatformNone},
	{"owner2@local.test", auth.PlatformNone},
	{"multi@local.test", auth.PlatformNone},
}

// Two tenants so isolation can be checked by hand; multi@ belongs to both.
var seedInstitutes = []struct {
	slug, name string
	members    []seedMembership
}{
	{"demo-institute", "Demo Institute", []seedMembership{
		{"owner@local.test", auth.InstituteOwner}, {"admin@local.test", auth.InstituteAdmin},
		{"staff@local.test", auth.InstituteStaff}, {"multi@local.test", auth.InstituteStaff}}},
	{"other-institute", "Other Institute", []seedMembership{
		{"owner2@local.test", auth.InstituteOwner}, {"multi@local.test", auth.InstituteAdmin}}},
}

// runSeed is idempotent and refuses to run in production.
func runSeed(ctx context.Context, cfg config.Config, svc *auth.Service, store auth.Store, tenants tenancy.AdminStore) error {
	if cfg.Env == "production" {
		return errors.New("seed is disabled when APP_ENV=production")
	}
	ids := map[string]string{}
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
		ids[s.email] = u.ID
		fmt.Printf("seeded user %-24s platform_role=%q\n", s.email, s.role)
	}
	for _, in := range seedInstitutes {
		inst, err := tenants.CreateInstitute(ctx, in.slug, in.name)
		if errors.Is(err, tenancy.ErrSlugTaken) {
			inst, err = tenants.InstituteBySlug(ctx, in.slug)
		}
		if err != nil {
			return err
		}
		for _, m := range in.members {
			if _, err := tenants.AddMembership(ctx, inst.ID, ids[m.email], m.role, nil); err != nil && !errors.Is(err, tenancy.ErrMembershipExists) {
				return err
			}
			fmt.Printf("seeded %-16s %-24s role=%s\n", in.slug, m.email, m.role)
		}
	}
	fmt.Printf("local password for all seed users: %s\n", seedPassword)
	return nil
}

// cliAudit records an out-of-band administrative action. The CLI path is not transactional with the
// change (D-121): if recording fails the command reports an error so the operator can reconcile.
func cliAudit(ctx context.Context, aw audit.Writer, action, entityType, entityID, instituteID string, meta map[string]any) error {
	if err := aw.Record(ctx, audit.Event{ActorType: audit.ActorCLI, Action: action, EntityType: entityType,
		EntityID: entityID, InstituteID: instituteID, Metadata: meta}); err != nil {
		return fmt.Errorf("the change was applied but its audit event could not be recorded: %w", err)
	}
	return nil
}

// runUser implements out-of-band administration (platform roles are never settable via the API).
func runUser(ctx context.Context, svc *auth.Service, store auth.Store, aw audit.Writer, args []string) error {
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
		return cliAudit(ctx, aw, "user.platform_role_changed", "user", u.ID, "", map[string]any{"role": string(role)})
	case "suspend":
		if err := svc.SuspendUser(ctx, u.ID); err != nil {
			return err
		}
		fmt.Println("suspended", email)
		return cliAudit(ctx, aw, "user.suspended", "user", u.ID, "", nil)
	case "reactivate":
		if err := svc.ReactivateUser(ctx, u.ID); err != nil {
			return err
		}
		fmt.Println("reactivated", email)
		return cliAudit(ctx, aw, "user.reactivated", "user", u.ID, "", nil)
	default:
		return usage
	}
}

// runInstitute implements out-of-band institute administration (closing is CLI-only by design).
func runInstitute(ctx context.Context, users auth.Store, tenants tenancy.AdminStore, aw audit.Writer, args []string) error {
	usage := errors.New(`usage:
  api institute create SLUG NAME OWNER_EMAIL
  api institute add-member SLUG EMAIL owner|admin|staff [grant,grant...]
  api institute remove-member SLUG EMAIL
  api institute status SLUG active|suspended|closed`)
	if len(args) < 2 {
		return usage
	}
	inst, err := func() (tenancy.Institute, error) {
		if args[0] == "create" {
			return tenancy.Institute{}, nil
		}
		return tenants.InstituteBySlug(ctx, args[1])
	}()
	if err != nil {
		return fmt.Errorf("institute lookup: %w", err)
	}
	userID := func(email string) (string, error) {
		e, ok := auth.NormalizeEmail(email)
		if !ok {
			return "", errors.New("invalid email")
		}
		u, err := users.UserByEmail(ctx, e)
		if err != nil {
			return "", fmt.Errorf("user lookup: %w", err)
		}
		return u.ID, nil
	}
	switch args[0] {
	case "create":
		if len(args) != 4 {
			return usage
		}
		owner, err := userID(args[3])
		if err != nil {
			return err
		}
		created, err := tenants.CreateInstitute(ctx, args[1], args[2])
		if err != nil {
			return err
		}
		if _, err := tenants.AddMembership(ctx, created.ID, owner, auth.InstituteOwner, nil); err != nil {
			return err
		}
		fmt.Printf("created institute %s (%s) with owner %s\n", created.Slug, created.ID, args[3])
		if err := cliAudit(ctx, aw, "institute.created", "institute", created.ID, created.ID, map[string]any{"slug": created.Slug, "via": "cli"}); err != nil {
			return err
		}
		return cliAudit(ctx, aw, "membership.created", "institute_membership", created.ID+":"+owner, created.ID,
			map[string]any{"user_id": owner, "role": string(auth.InstituteOwner), "via": "cli"})
	case "add-member":
		if len(args) < 4 || len(args) > 5 {
			return usage
		}
		uid, err := userID(args[2])
		if err != nil {
			return err
		}
		var grants []auth.Permission
		if len(args) == 5 {
			for _, g := range strings.Split(args[4], ",") {
				grants = append(grants, auth.Permission(strings.TrimSpace(g)))
			}
		}
		if _, err := tenants.AddMembership(ctx, inst.ID, uid, auth.InstituteRole(args[3]), grants); err != nil {
			return err
		}
		fmt.Printf("added %s to %s as %s\n", args[2], inst.Slug, args[3])
		return cliAudit(ctx, aw, "membership.created", "institute_membership", inst.ID+":"+uid, inst.ID,
			map[string]any{"user_id": uid, "role": args[3], "grants": grants, "via": "cli"})
	case "remove-member":
		if len(args) != 3 {
			return usage
		}
		uid, err := userID(args[2])
		if err != nil {
			return err
		}
		if err := tenants.RemoveMembership(ctx, inst.ID, uid); err != nil {
			return err
		}
		fmt.Printf("removed %s from %s\n", args[2], inst.Slug)
		return cliAudit(ctx, aw, "membership.removed", "institute_membership", inst.ID+":"+uid, inst.ID,
			map[string]any{"user_id": uid, "via": "cli"})
	case "status":
		if len(args) != 3 {
			return usage
		}
		if err := tenants.SetInstituteStatus(ctx, inst.ID, tenancy.InstituteStatus(args[2])); err != nil {
			return err
		}
		fmt.Printf("institute %s is now %s\n", inst.Slug, args[2])
		return cliAudit(ctx, aw, "institute.status_changed", "institute", inst.ID, inst.ID,
			map[string]any{"from": string(inst.Status), "to": args[2], "via": "cli"})
	default:
		return usage
	}
}
