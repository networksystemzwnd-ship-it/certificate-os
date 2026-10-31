package certificates

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/pdf"
	"github.com/certificate-os/api/internal/platform/metrics"
	"github.com/certificate-os/api/internal/qr"
	"github.com/certificate-os/api/internal/templates"
	"github.com/certificate-os/api/internal/tenancy"
)

// Service holds the issuing rules. Permission checks happen in the HTTP layer; the scope and the acting
// user always come from the resolved tenant.
type Service struct {
	Store    Store
	Renderer pdf.Renderer
	// PublicBaseURL is the origin printed in the QR code (https://certs.example.com, no trailing slash).
	PublicBaseURL string
	Now           func() time.Time
	// Metrics is optional (nil is fine).
	Metrics *metrics.Registry
}

func NewService(st Store, r pdf.Renderer, publicBaseURL string) *Service {
	if r == nil {
		r = pdf.Unavailable{}
	}
	return &Service{Store: st, Renderer: r, PublicBaseURL: strings.TrimRight(publicBaseURL, "/"), Now: time.Now}
}

func event(t tenancy.Tenant, action, requestID string, meta map[string]any) audit.Event {
	return audit.Event{ActorType: audit.ActorUser, ActorUserID: t.UserID, Action: action,
		EntityType: "certificate", RequestID: requestID, Metadata: meta}
}

// NewPublicID returns 32 random bytes as 43 base64url characters.
func NewPublicID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Service) renderOff() bool {
	_, off := s.Renderer.(pdf.Unavailable)
	return off
}

// VerifyURL is the link encoded in the certificate's QR code.
func (s *Service) VerifyURL(publicID string) string { return s.PublicBaseURL + "/verify/" + publicID }

type IssueRequest struct {
	EnrollmentID, TemplateID string
	TemplateVersion          int
}

// Issue creates one certificate end to end. The certificate is only reported as issued after the PDF is
// stored; on any rendering problem the record is kept as failed (its number stays used, never reused).
func (s *Service) Issue(ctx context.Context, t tenancy.Tenant, req IssueRequest, reqID string) (Certificate, error) {
	p := map[string]string{}
	if strings.TrimSpace(req.EnrollmentID) == "" {
		p["enrollment_id"] = "Choose a completed enrollment."
	}
	if strings.TrimSpace(req.TemplateID) == "" {
		p["template_id"] = "Choose a template."
	}
	if req.TemplateVersion < 0 || req.TemplateVersion > templates.MaxVersions {
		p["template_version"] = "Use a version number from the template's history."
	}
	if len(p) > 0 {
		return Certificate{}, &ValidationError{Problems: p}
	}
	if s.renderOff() {
		return Certificate{}, ErrRenderUnavailable // nothing written, no number used
	}
	publicID, err := NewPublicID()
	if err != nil {
		return Certificate{}, err
	}
	meta := map[string]any{"template_id": req.TemplateID}
	cert, err := s.Store.BeginIssue(ctx, t.Scope(), BeginInput{EnrollmentID: req.EnrollmentID, TemplateID: req.TemplateID,
		Version: req.TemplateVersion, PublicID: publicID, IssuedBy: t.UserID, Now: s.Now()},
		event(t, "certificate.issue_started", reqID, meta))
	if err != nil {
		return Certificate{}, err
	}

	return s.finish(ctx, t, cert, reqID)
}

// finish renders and stores the PDF of a pending certificate and marks it issued, or marks it failed. From
// the moment the pending record exists it completes even if the client disconnects.
func (s *Service) finish(ctx context.Context, t tenancy.Tenant, cert Certificate, reqID string) (Certificate, error) {
	bg := context.WithoutCancel(ctx)
	fail := func(reason string, cause error) (Certificate, error) {
		s.Metrics.Inc("cos_certificate_failures_total", "reason", reason)
		if _, ferr := s.Store.Fail(bg, t.Scope(), cert.ID, reason, event(t, "certificate.issue_failed", reqID, map[string]any{"reason": reason})); ferr != nil {
			return Certificate{}, fmt.Errorf("%w: %v (and marking it failed: %v)", ErrRenderFailed, cause, ferr)
		}
		return Certificate{}, fmt.Errorf("%w: %v", ErrRenderFailed, cause)
	}
	html, err := s.html(cert)
	if err != nil {
		return fail("could not build the certificate page", err)
	}
	bytes, err := s.Renderer.HTMLToPDF(ctx, html)
	if err != nil {
		return fail("pdf rendering failed", err)
	}
	done, err := s.Store.Complete(bg, t.Scope(), cert.ID, bytes, event(t, "certificate.issued", reqID,
		map[string]any{"template_id": cert.TemplateID, "template_version": cert.TemplateVersion}))
	if err != nil {
		if errors.Is(err, ErrState) {
			if cert.ReplacesID != "" {
				// The certificate being replaced is no longer valid: this replacement must not stay pending.
				_, _ = s.Store.Fail(bg, t.Scope(), cert.ID, "the certificate to replace is no longer valid", event(t, "certificate.issue_failed", reqID,
					map[string]any{"reason": "replaced certificate no longer valid"}))
			}
			return Certificate{}, err
		}
		return fail("could not store the pdf", err)
	}
	return done, nil
}

type ReplaceRequest struct {
	CertificateID   string
	Reason          string
	TemplateID      string // default: the template of the certificate being replaced
	TemplateVersion int    // 0 = the template's current version
}

// Replace issues a corrected certificate for the same enrollment. The old certificate stays valid until the new
// PDF is stored; then it is revoked with the reason and linked to the new one (public page: "replaced").
// The new certificate reflects the CURRENT student, course and template data.
func (s *Service) Replace(ctx context.Context, t tenancy.Tenant, req ReplaceRequest, reqID string) (Certificate, error) {
	p := map[string]string{}
	reason := strings.TrimSpace(req.Reason)
	if n := len([]rune(reason)); n < 3 || n > 500 {
		p["reason"] = "Explain what is being corrected (3–500 characters)."
	}
	if req.TemplateVersion < 0 || req.TemplateVersion > templates.MaxVersions {
		p["template_version"] = "Use a version number from the template's history."
	}
	if len(p) > 0 {
		return Certificate{}, &ValidationError{Problems: p}
	}
	if s.renderOff() {
		return Certificate{}, ErrRenderUnavailable
	}
	old, err := s.Store.Get(ctx, t.Scope(), req.CertificateID)
	if err != nil {
		return Certificate{}, err
	}
	if old.Status != Issued {
		return Certificate{}, ErrState
	}
	tplID := strings.TrimSpace(req.TemplateID)
	if tplID == "" {
		tplID = old.TemplateID
	}
	publicID, err := NewPublicID()
	if err != nil {
		return Certificate{}, err
	}
	cert, err := s.Store.BeginIssue(ctx, t.Scope(), BeginInput{EnrollmentID: old.EnrollmentID, TemplateID: tplID, Version: req.TemplateVersion,
		PublicID: publicID, IssuedBy: t.UserID, Now: s.Now(), Replaces: old.ID, ReplaceReason: reason},
		event(t, "certificate.replace_started", reqID, map[string]any{"replaces": old.ID, "template_id": tplID, "has_reason": true}))
	if err != nil {
		return Certificate{}, err
	}
	return s.finish(ctx, t, cert, reqID)
}

// html renders the certificate page from its immutable snapshot (never from live student data).
func (s *Service) html(c Certificate) (string, error) {
	sn := c.Snapshot
	svg, err := qr.SVG(s.VerifyURL(c.PublicID))
	if err != nil {
		return "", err
	}
	return templates.Render(sn.Config, templates.RenderData{
		StudentName: sn.StudentName, CourseName: sn.CourseName, BatchName: sn.BatchName, InstituteName: sn.InstituteName,
		CompletionDate: longDate(sn.CompletionDate), IssueDate: longDate(sn.IssueDate), CertificateNumber: sn.CertificateNumber,
		Grade: sn.Grade, QRSVG: svg,
	}, false), nil
}

func longDate(iso string) string {
	d, err := time.Parse("2006-01-02", iso)
	if err != nil {
		return iso
	}
	return d.Format("2 January 2006")
}

func (s *Service) Get(ctx context.Context, t tenancy.Tenant, id string) (Certificate, error) {
	return s.Store.Get(ctx, t.Scope(), id)
}

func (s *Service) List(ctx context.Context, t tenancy.Tenant, f Filter) ([]Certificate, error) {
	return s.Store.List(ctx, t.Scope(), f.Normalized())
}

func (s *Service) PDF(ctx context.Context, t tenancy.Tenant, id string) (PDF, error) {
	return s.Store.GetPDF(ctx, t.Scope(), id)
}

// Revoke marks an issued certificate revoked. A reason (3–500 characters) is required for accountability.
func (s *Service) Revoke(ctx context.Context, t tenancy.Tenant, id, reason, reqID string) (Certificate, error) {
	reason = strings.TrimSpace(reason)
	if n := len([]rune(reason)); n < 3 || n > 500 {
		return Certificate{}, &ValidationError{Problems: map[string]string{"reason": "Explain why (3–500 characters)."}}
	}
	return s.Store.Revoke(ctx, t.Scope(), id, reason, t.UserID, event(t, "certificate.revoked", reqID, map[string]any{"has_reason": true}))
}
