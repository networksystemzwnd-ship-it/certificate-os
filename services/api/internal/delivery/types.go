// Package delivery sends issued certificates by email (M5b). Delivery is independent of issuing: the certificate
// is never changed by a delivery, a failed delivery is never rolled back into the certificate and a certificate
// can be (re)delivered. Statuses are honest: "accepted" means the mail provider took the message, not that it
// reached an inbox.
package delivery

import (
	"context"
	"errors"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/tenancy"
)

var (
	ErrNotFound    = errors.New("delivery not found")
	ErrState       = errors.New("delivery is not in a state that allows this")
	ErrActive      = errors.New("a delivery for this certificate is already in progress")
	ErrNoRecipient = errors.New("the student has no email address on file")
	ErrLimit       = errors.New("too many deliveries for this certificate")
	// ErrAlreadyAccepted is returned for requests that ask to skip certificates already delivered.
	ErrAlreadyAccepted = errors.New("this certificate was already delivered")
	ErrSuspended       = errors.New("institute is suspended")
	ErrDisabled        = errors.New("email delivery is not configured")
)

const (
	// MaxAttempts is how many sends one round makes before the delivery is marked failed.
	MaxAttempts = 5
	// MaxPerCertificate bounds how often one certificate can be sent (a resend after "accepted" is allowed).
	MaxPerCertificate = 10
	// Lease is how long a worker owns a delivery it is sending; a crashed worker's delivery is picked up after it.
	Lease = 5 * time.Minute
	// SuspendedRecheck is how long deliveries of a suspended institute wait before being looked at again.
	SuspendedRecheck = 15 * time.Minute
)

// Backoff is the wait after the n-th failed transient attempt (n starts at 1); the last value repeats.
var Backoff = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}

func BackoffAfter(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > len(Backoff) {
		attempt = len(Backoff)
	}
	return Backoff[attempt-1]
}

type Status string

const (
	Queued   Status = "queued"
	Sending  Status = "sending"
	Accepted Status = "accepted"
	Failed   Status = "failed"
)

func (s Status) Valid() bool  { return s == Queued || s == Sending || s == Accepted || s == Failed }
func (s Status) Active() bool { return s == Queued || s == Sending }

// Stable failure codes recorded besides the mail package's own codes.
const (
	CodeNotIssued   = "certificate_not_issued" // revoked, replaced or otherwise no longer deliverable
	CodeExhausted   = "attempts_exhausted"
	CodeInterrupted = "interrupted" // the worker stopped repeatedly while sending
)

// Delivery is one request to email one certificate. Recipient is personal data: DTOs mask it.
type Delivery struct {
	ID                string
	CertificateID     string
	CertificateNumber string
	Channel           string
	Recipient         string
	Status            Status
	Attempts          int
	MaxAttempts       int
	NextAttemptAt     *time.Time // only while queued
	LastError         string     // short code, never provider text
	CreatedBy         string
	CreatedAt         time.Time
	UpdatedAt         time.Time
	FinishedAt        *time.Time
}

// Attempt is one try, kept for good.
type Attempt struct {
	No         int
	Provider   string
	Status     string // accepted | failed
	ErrorCode  string
	StartedAt  time.Time
	FinishedAt time.Time
}

type RequestInput struct {
	CertificateID string
	CreatedBy     string
	Now           time.Time
	// SkipAccepted refuses (ErrAlreadyAccepted) when the certificate was already delivered successfully;
	// bulk sending sets it so "email everyone" never mails a student twice.
	SkipAccepted bool
}

type Filter struct {
	CertificateID string
	Status        Status
	Limit         int
}

// DueRef identifies work found by the cross-institute scan. It carries no personal data.
type DueRef struct{ InstituteID, DeliveryID string }

// Claim is what a worker needs to send one delivery.
type Claim struct {
	Delivery Delivery
	Token    string
}

type Outcome string

const (
	OutcomeAccepted Outcome = "accepted"
	OutcomeRetry    Outcome = "retry"  // transient failure, try again at NextAttemptAt
	OutcomeFailed   Outcome = "failed" // permanent failure or attempts exhausted
)

type FinishInput struct {
	Outcome       Outcome
	Provider      string
	Code          string // required unless accepted
	StartedAt     time.Time
	At            time.Time
	NextAttemptAt time.Time // OutcomeRetry
}

// Depth describes the work queue across all institutes (ids and times only): Active deliveries not finished, Overdue
// of them due now or earlier, and how long the oldest overdue one has been waiting.
type Depth struct {
	Active, Overdue int
	OldestOverdue   time.Duration
}

// Store persists deliveries. Like the other stores, every method takes the Scope and may only touch that
// institute's rows (another institute's id is indistinguishable from a missing one); mutations run in one
// transaction with their audit event. Due is the one cross-institute call and returns ids only.
type Store interface {
	// Request queues a delivery for an ISSUED certificate to the student's current email address.
	// ErrNotFound (unknown, foreign or not issued), ErrNoRecipient, ErrActive, ErrLimit, ErrAlreadyAccepted.
	Request(ctx context.Context, s tenancy.Scope, in RequestInput, ev audit.Event) (Delivery, error)
	Get(ctx context.Context, s tenancy.Scope, id string) (Delivery, error)
	// List returns up to Limit+1 deliveries, newest first.
	List(ctx context.Context, s tenancy.Scope, f Filter) ([]Delivery, error)
	Attempts(ctx context.Context, s tenancy.Scope, id string) ([]Attempt, error)
	// Retry queues a FAILED delivery again, for the student's current email address, with a fresh round of
	// attempts. ErrState unless failed; ErrNoRecipient; ErrNotFound if the certificate is no longer issued.
	Retry(ctx context.Context, s tenancy.Scope, id string, now time.Time, ev audit.Event) (Delivery, error)

	// Due lists deliveries ready to send across all institutes (queued and due, or sending with an expired lease).
	Due(ctx context.Context, now time.Time, limit int) ([]DueRef, error)
	// Depth reports queue depth for monitoring.
	Depth(ctx context.Context, now time.Time) (Depth, error)
	// Claim takes ownership of a due delivery: status sending, attempts+1, a lease. ErrState when it is not due
	// or someone else holds it; ErrSuspended when the institute is not active (nothing changes).
	Claim(ctx context.Context, s tenancy.Scope, id, token string, now time.Time, lease time.Duration) (Claim, error)
	// Finish records the attempt and the outcome. ErrState when the token no longer owns the delivery.
	Finish(ctx context.Context, s tenancy.Scope, id, token string, in FinishInput, ev audit.Event) (Delivery, error)
	// Postpone moves the next look at a delivery to "until" without counting an attempt.
	Postpone(ctx context.Context, s tenancy.Scope, id string, until time.Time) error
}
