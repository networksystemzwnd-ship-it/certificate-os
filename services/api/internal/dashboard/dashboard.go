// Package dashboard serves read-only overviews (M6a): one per institute (tenant-scoped) and one for platform
// operations (aggregate counts only: no student data, no names, no addresses).
package dashboard

import (
	"context"
	"time"

	"github.com/certificate-os/api/internal/tenancy"
)

// Periods the institute dashboard offers, in days.
var Periods = []int{7, 30, 90, 365}

const DefaultDays = 30

type Records struct{ ActiveStudents, ArchivedStudents, ActiveBatches int }

type CertificateCounts struct {
	IssuedInPeriod int // issued during the period
	Issued         int // all currently valid
	Revoked        int
	Failed         int
}

type JobSummary struct {
	ID, BatchName, Status    string
	Total, Succeeded, Failed int
	CreatedAt                time.Time
}

// DeliveryCounts counts deliveries REQUESTED during the period by their current status.
type DeliveryCounts struct{ Queued, Sending, Accepted, Failed int }

type RecentCertificate struct {
	ID, Number, StudentName, CourseName, Status string
	At                                          time.Time
}

// InstituteStats is everything the institute dashboard shows; the HTTP layer drops what the member may not see.
type InstituteStats struct {
	Records      Records
	Certificates CertificateCounts
	LastJob      *JobSummary
	Deliveries   DeliveryCounts
	Recent       []RecentCertificate
}

type RecentDecision struct {
	Action string
	At     time.Time
}

// PlatformStats is aggregate only.
type PlatformStats struct {
	Applications map[string]int // by status
	Institutes   map[string]int // active, suspended, closed
	// Counts of audited events in the period.
	CertificatesIssued, CertificatesRevoked, BulkJobsStarted, EmailsAccepted, EmailsFailed int
	RecentDecisions                                                                        []RecentDecision
}

type Store interface {
	Institute(ctx context.Context, s tenancy.Scope, since time.Time) (InstituteStats, error)
	Platform(ctx context.Context, since time.Time) (PlatformStats, error)
}

// ValidDays reports whether n is one of the offered periods.
func ValidDays(n int) bool {
	for _, p := range Periods {
		if p == n {
			return true
		}
	}
	return false
}
