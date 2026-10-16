package tenancytest

import (
	"fmt"
	"testing"
)

func TestMemStoreContract(t *testing.T) {
	RunStoreContract(t, func(*testing.T) (Store, func(string) string) {
		return NewMemStore(), func(email string) string { return fmt.Sprintf("user-%s", email) }
	})
}
