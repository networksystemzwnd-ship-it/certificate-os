package tenancy

import (
	"context"
	"sort"

	"github.com/certificate-os/api/internal/auth"
)

type Service struct{ Store Store }

// Resolve determines the tenant for an authenticated principal.
//
// The requested institute ID (client-supplied) is only ever matched against the user's OWN active
// memberships; it is never used to look anything up directly. A non-member and a non-existent
// institute are indistinguishable (both ErrNotMember).
func (s *Service) Resolve(ctx context.Context, p auth.Principal, requested string) (Tenant, error) {
	ms, err := s.Store.MembershipsForUser(ctx, p.UserID)
	if err != nil {
		return Tenant{}, err
	}
	if requested != "" {
		for _, m := range ms {
			if m.Institute.ID == requested {
				return newTenant(p.UserID, m), nil
			}
		}
		return Tenant{}, ErrNotMember
	}
	switch len(ms) {
	case 0:
		return Tenant{}, ErrNotMember
	case 1:
		return newTenant(p.UserID, ms[0]), nil
	default:
		return Tenant{}, ErrAmbiguous
	}
}

func (s *Service) Memberships(ctx context.Context, p auth.Principal) ([]MembershipView, error) {
	return s.Store.MembershipsForUser(ctx, p.UserID)
}

func sortPerms(ps []auth.Permission) {
	sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
}
