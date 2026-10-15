package authtest

import (
	"testing"

	"github.com/certificate-os/api/internal/auth"
)

func TestMemStoreContract(t *testing.T) {
	RunStoreContract(t, func(*testing.T) auth.Store { return NewMemStore() })
}
