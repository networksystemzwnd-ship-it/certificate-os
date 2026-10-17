package applicationstest

import (
	"fmt"
	"testing"

	"github.com/certificate-os/api/internal/audit/audittest"
	"github.com/certificate-os/api/internal/tenancy/tenancytest"
)

func TestMemStoreContract(t *testing.T) {
	RunStoreContract(t, func(*testing.T) Harness {
		tenants := tenancytest.NewMemStore()
		w := audittest.NewMemWriter()
		return Harness{
			Store: NewMemStore(tenants, w), Tenants: tenants,
			NewUser: func(email string) string { return "user-" + email },
			AuditActions: func(et, id string) []string {
				var out []string
				for _, r := range w.ForEntity(et, id) {
					out = append(out, r.Action)
				}
				return out
			},
		}
	})
}

var _ = fmt.Sprintf
