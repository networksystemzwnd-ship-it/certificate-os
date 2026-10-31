package deliverytest

import (
	"fmt"
	"testing"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/certificates/certtest"
	"github.com/certificate-os/api/internal/tenancy"
)

func TestMemStoreContract(t *testing.T) {
	RunStoreContract(t, func(*testing.T) Harness {
		w := audittest.NewMemWriter()
		cs := certtest.NewMemStore(w)
		ds := NewMemStore(cs, w)
		n, u := 0, 0
		return Harness{
			Harness: certtest.Harness{
				Store: cs,
				NewScope: func() tenancy.Scope {
					n++
					return tenancy.TrustedScope(fmt.Sprintf("inst-%d", n))
				},
				NewUser:    func() string { u++; return fmt.Sprintf("user-%d", u) },
				NewFixture: func(s tenancy.Scope, o certtest.FixtureOpts) certtest.Refs { return cs.AddFixture(s, o) },
			},
			Deliveries: ds,
			SetEmail:   ds.SetEmail,
			SetActive: func(s tenancy.Scope, active bool) {
				ds.SetActive(s.InstituteID(), active)
				cs.SetInstituteActive(s, active)
			},
			Audit: func(id string) ([]string, map[string]any) {
				var acts []string
				var meta map[string]any
				for _, r := range w.ForEntity("delivery", id) {
					acts = append(acts, r.Action)
					meta = r.Metadata
				}
				return acts, meta
			},
		}
	})
}
