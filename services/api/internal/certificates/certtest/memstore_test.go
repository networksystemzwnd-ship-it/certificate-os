package certtest

import (
	"fmt"
	"testing"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/tenancy"
)

func TestMemStoreContract(t *testing.T) {
	RunStoreContract(t, func(*testing.T) Harness {
		w := audittest.NewMemWriter()
		m := NewMemStore(w)
		n, u := 0, 0
		return Harness{
			Store: m,
			NewScope: func() tenancy.Scope {
				n++
				return tenancy.TrustedScope(fmt.Sprintf("inst-%d", n))
			},
			NewUser:    func() string { u++; return fmt.Sprintf("user-%d", u) },
			NewFixture: func(s tenancy.Scope, o FixtureOpts) Refs { return m.AddFixture(s, o) },
			AuditActions: func(id string) []string {
				var out []string
				for _, r := range w.ForEntity("certificate", id) {
					out = append(out, r.Action)
				}
				return out
			},
			AuditTotal: func(inst string) int {
				c := 0
				for _, r := range w.Records() {
					if r.InstituteID == inst {
						c++
					}
				}
				return c
			},
			AuditMeta: func(id string) map[string]any {
				rs := w.ForEntity("certificate", id)
				if len(rs) == 0 {
					return nil
				}
				return rs[len(rs)-1].Metadata
			},
		}
	})
}

func TestMemStoreJobContract(t *testing.T) {
	RunJobStoreContract(t, func(*testing.T) JobHarness {
		w := audittest.NewMemWriter()
		m := NewMemStore(w)
		n, u := 0, 0
		return JobHarness{
			Harness: Harness{
				Store: m,
				NewScope: func() tenancy.Scope {
					n++
					return tenancy.TrustedScope(fmt.Sprintf("inst-%d", n))
				},
				NewUser:    func() string { u++; return fmt.Sprintf("user-%d", u) },
				NewFixture: func(s tenancy.Scope, o FixtureOpts) Refs { return m.AddFixture(s, o) },
			},
			Jobs:      m,
			SetActive: m.SetInstituteActive,
			JobAudit: func(id string) ([]string, map[string]any) {
				var acts []string
				var meta map[string]any
				for _, r := range w.ForEntity("bulk_job", id) {
					acts = append(acts, r.Action)
					meta = r.Metadata
				}
				return acts, meta
			},
		}
	})
}
