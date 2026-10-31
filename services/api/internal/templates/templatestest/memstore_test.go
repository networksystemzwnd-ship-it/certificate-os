package templatestest

import (
	"fmt"
	"testing"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/tenancy"
)

func TestMemStoreContract(t *testing.T) {
	RunStoreContract(t, func(*testing.T) Harness {
		w := audittest.NewMemWriter()
		n, u := 0, 0
		return Harness{
			Store: NewMemStore(w),
			NewScope: func() tenancy.Scope {
				n++
				return tenancy.TrustedScope(fmt.Sprintf("inst-%d", n))
			},
			NewUser: func() string { u++; return fmt.Sprintf("user-%d", u) },
			AuditActions: func(et, id string) []string {
				var out []string
				for _, r := range w.ForEntity(et, id) {
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
			AuditMeta: func(et, id string) map[string]any {
				rs := w.ForEntity(et, id)
				if len(rs) == 0 {
					return nil
				}
				return rs[len(rs)-1].Metadata
			},
		}
	})
}
