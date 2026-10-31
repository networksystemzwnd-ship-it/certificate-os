package delivery

import (
	"context"
	"errors"

	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/tenancy"
)

// JobCertificates returns the function the HTTP handler uses to list the certificates a bulk job issued.
func JobCertificates(jobs *certificates.Jobs) func(ctx context.Context, t tenancy.Tenant, jobID string) ([]string, error) {
	return func(ctx context.Context, t tenancy.Tenant, jobID string) ([]string, error) {
		if _, err := jobs.Get(ctx, t, jobID); err != nil {
			if errors.Is(err, certificates.ErrNotFound) {
				return nil, ErrNotFound
			}
			return nil, err
		}
		var ids []string
		for offset := 0; offset < certificates.MaxJobItems; offset += 200 {
			items, err := jobs.Items(ctx, t, jobID, certificates.ItemSucceeded, 200, offset)
			if err != nil {
				return nil, err
			}
			for _, it := range items {
				if it.CertificateID != "" {
					ids = append(ids, it.CertificateID)
				}
			}
			if len(items) < 200 {
				break
			}
		}
		return ids, nil
	}
}
