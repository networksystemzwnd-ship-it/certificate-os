package delivery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/delivery/mail"
	"github.com/certificate-os/api/internal/tenancy"
)

// Service queues deliveries (on behalf of a signed-in user) and sends them (the worker, as the system).
// Permission checks happen in the HTTP layer.
type Service struct {
	Store  Store
	Certs  certificates.Store
	Mailer mail.Mailer // nil: sending is disabled (requests are refused with ErrDisabled)
	From   string
	// PublicBaseURL is the origin of verification links (same value the certificates use).
	PublicBaseURL string
	Log           *slog.Logger
	Now           func() time.Time
}

func NewService(st Store, certs certificates.Store, m mail.Mailer, from, publicBaseURL string, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{Store: st, Certs: certs, Mailer: m, From: from, PublicBaseURL: strings.TrimRight(publicBaseURL, "/"), Log: log, Now: time.Now}
}

func (s *Service) Enabled() bool { return s.Mailer != nil }

func userEvent(t tenancy.Tenant, action, reqID string) audit.Event {
	return audit.Event{ActorType: audit.ActorUser, ActorUserID: t.UserID, Action: action, EntityType: "delivery", RequestID: reqID}
}

func systemEvent(action string, meta map[string]any) audit.Event {
	return audit.Event{ActorType: audit.ActorSystem, Action: action, EntityType: "delivery", Metadata: meta}
}

// Send queues one delivery of an issued certificate.
func (s *Service) Send(ctx context.Context, t tenancy.Tenant, certID, reqID string) (Delivery, error) {
	if !s.Enabled() {
		return Delivery{}, ErrDisabled
	}
	return s.Store.Request(ctx, t.Scope(), RequestInput{CertificateID: certID, CreatedBy: t.UserID, Now: s.Now()}, userEvent(t, "delivery.requested", reqID))
}

// Retry queues a failed delivery again.
func (s *Service) Retry(ctx context.Context, t tenancy.Tenant, id, reqID string) (Delivery, error) {
	if !s.Enabled() {
		return Delivery{}, ErrDisabled
	}
	return s.Store.Retry(ctx, t.Scope(), id, s.Now(), userEvent(t, "delivery.retry_requested", reqID))
}

// ManyResult counts what a bulk request did; nothing in it is personal data.
type ManyResult struct {
	Queued, NoEmail, AlreadyDelivered, InProgress, NotDeliverable, TooMany int
}

// SendMany queues deliveries for several certificates. Certificates already delivered, with a delivery in
// progress, or without an email address are skipped and counted, never mailed twice.
func (s *Service) SendMany(ctx context.Context, t tenancy.Tenant, certIDs []string, reqID string) (ManyResult, error) {
	var r ManyResult
	if !s.Enabled() {
		return r, ErrDisabled
	}
	for _, id := range certIDs {
		_, err := s.Store.Request(ctx, t.Scope(), RequestInput{CertificateID: id, CreatedBy: t.UserID, Now: s.Now(), SkipAccepted: true}, userEvent(t, "delivery.requested", reqID))
		switch {
		case err == nil:
			r.Queued++
		case errors.Is(err, ErrNoRecipient):
			r.NoEmail++
		case errors.Is(err, ErrAlreadyAccepted):
			r.AlreadyDelivered++
		case errors.Is(err, ErrActive):
			r.InProgress++
		case errors.Is(err, ErrNotFound):
			r.NotDeliverable++
		case errors.Is(err, ErrLimit):
			r.TooMany++
		default:
			return r, err
		}
	}
	return r, nil
}

func (s *Service) Get(ctx context.Context, t tenancy.Tenant, id string) (Delivery, error) {
	return s.Store.Get(ctx, t.Scope(), id)
}

func (s *Service) List(ctx context.Context, t tenancy.Tenant, f Filter) ([]Delivery, error) {
	if f.Limit <= 0 || f.Limit > 100 {
		f.Limit = 50
	}
	return s.Store.List(ctx, t.Scope(), f)
}

func (s *Service) Attempts(ctx context.Context, t tenancy.Tenant, id string) ([]Attempt, error) {
	return s.Store.Attempts(ctx, t.Scope(), id)
}

// ---- worker ----

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// ProcessDue sends the deliveries that are due now and returns how many it handled. It is safe to run from
// several replicas: Claim lets exactly one of them own a delivery.
func (s *Service) ProcessDue(ctx context.Context, batch int) (int, error) {
	if !s.Enabled() {
		return 0, nil
	}
	refs, err := s.Store.Due(ctx, s.Now(), batch)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, ref := range refs {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		if s.process(ctx, ref) {
			n++
		}
	}
	return n, nil
}

// process handles one delivery. It reports whether an attempt was made.
func (s *Service) process(ctx context.Context, ref DueRef) bool {
	// The scope comes from the work queue row itself, not from a request: this is the one place outside tests
	// that builds a Scope without a signed-in user (the worker acts as the system on rows it just found).
	sc := tenancy.TrustedScope(ref.InstituteID)
	tok, err := newToken()
	if err != nil {
		s.Log.Error("delivery worker could not create a token", "err", err)
		return false
	}
	now := s.Now()
	cl, err := s.Store.Claim(ctx, sc, ref.DeliveryID, tok, now, Lease)
	switch {
	case errors.Is(err, ErrSuspended):
		_ = s.Store.Postpone(ctx, sc, ref.DeliveryID, now.Add(SuspendedRecheck))
		return false
	case errors.Is(err, ErrState), errors.Is(err, ErrNotFound):
		return false // somebody else has it, or it is no longer due
	case err != nil:
		s.Log.Error("delivery claim failed", "delivery_id", ref.DeliveryID, "err", err)
		return false
	}
	d := cl.Delivery
	fin := FinishInput{Provider: s.Mailer.Name(), StartedAt: now}

	cert, cerr := s.Certs.Get(ctx, sc, d.CertificateID)
	var file certificates.PDF
	if cerr == nil && cert.Status == certificates.Issued {
		file, cerr = s.Certs.GetPDF(ctx, sc, d.CertificateID)
	}
	switch {
	case errors.Is(cerr, certificates.ErrNotFound) || (cerr == nil && cert.Status != certificates.Issued):
		fin.Outcome, fin.Code = OutcomeFailed, CodeNotIssued
	case cerr != nil:
		// Our own storage failed: not the provider's fault and not permanent.
		s.Log.Error("delivery could not load the certificate", "delivery_id", d.ID, "err", cerr)
		s.settle(&fin, d, true, "storage_error")
	default:
		sendErr := s.Mailer.Send(ctx, s.message(d, cert, file))
		if sendErr == nil {
			fin.Outcome = OutcomeAccepted
		} else {
			transient, code := mail.Classify(sendErr)
			if ctx.Err() != nil {
				// Shutting down: leave it for the next lease instead of counting a provider failure.
				return false
			}
			s.Log.Warn("email attempt failed", "delivery_id", d.ID, "attempt", d.Attempts, "code", code, "transient", transient)
			s.settle(&fin, d, transient, code)
		}
	}
	fin.At = s.Now()
	if fin.Outcome == OutcomeRetry {
		fin.NextAttemptAt = fin.At.Add(BackoffAfter(d.Attempts))
	}
	action, meta := "delivery.accepted", map[string]any{"attempt": d.Attempts, "provider": fin.Provider}
	if fin.Outcome != OutcomeAccepted {
		action, meta = "delivery.attempt_failed", map[string]any{"attempt": d.Attempts, "provider": fin.Provider, "code": fin.Code}
		if fin.Outcome == OutcomeFailed {
			action = "delivery.failed"
		}
	}
	if _, err := s.Store.Finish(ctx, sc, d.ID, tok, fin, systemEvent(action, meta)); err != nil {
		// The mail may have been accepted without being recorded: the delivery will be picked up again once the
		// lease expires (at-least-once). Documented in D-167.
		s.Log.Error("delivery outcome could not be recorded", "delivery_id", d.ID, "err", err)
	}
	return true
}

// settle turns a classified failure into a retry or a final failure.
func (s *Service) settle(f *FinishInput, d Delivery, transient bool, code string) {
	f.Code = code
	switch {
	case !transient:
		f.Outcome = OutcomeFailed
	case d.Attempts >= d.MaxAttempts:
		f.Outcome = OutcomeFailed
		f.Code = code // keep the last real cause; exhaustion is visible from attempts == max
	default:
		f.Outcome = OutcomeRetry
	}
}

func (s *Service) message(d Delivery, c certificates.Certificate, f certificates.PDF) mail.Message {
	sn := c.Snapshot
	var b strings.Builder
	fmt.Fprintf(&b, "Dear %s,\n\n", oneLine(sn.StudentName))
	fmt.Fprintf(&b, "Congratulations on completing %s. Your certificate %s from %s is attached to this email as a PDF.\n\n",
		oneLine(sn.CourseName), c.Number, oneLine(sn.InstituteName))
	fmt.Fprintf(&b, "Anyone can check that it is genuine at:\n%s\n\n", s.PublicBaseURL+"/verify/"+c.PublicID)
	b.WriteString("This message was sent automatically; please do not reply to it.\n")
	return mail.Message{
		From: s.From, To: d.Recipient,
		Subject:     "Your certificate from " + oneLine(sn.InstituteName),
		Text:        b.String(),
		Attachments: []mail.Attachment{{Filename: c.Number + ".pdf", ContentType: "application/pdf", Data: f.Bytes}},
	}
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// Run polls for due deliveries until ctx ends.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	if !s.Enabled() {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := s.ProcessDue(ctx, 20); err != nil && ctx.Err() == nil {
			s.Log.Error("delivery worker scan failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
