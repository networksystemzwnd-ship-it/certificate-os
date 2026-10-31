package importstest

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
		ms := NewMemStore(w)
		return Harness{
			Store: ms,
			Age:   ms.Age,
			NewScope: func() (tenancy.Scope, string) {
				n++
				return tenancy.TrustedScope(fmt.Sprintf("inst-%d", n)), "user-1"
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
