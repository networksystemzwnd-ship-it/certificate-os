package recordstest

import (
	"fmt"
	"testing"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/tenancy"
)

func TestMemStoreContract(t *testing.T) {
	RunStoreContract(t, func(*testing.T) Harness {
		w := audittest.NewMemWriter()
		n := 0
		return Harness{
			Store: NewMemStore(w),
			NewScope: func() tenancy.Scope {
				n++
				return tenancy.TrustedScope(fmt.Sprintf("inst-%d", n))
			},
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
		}
	})
}
