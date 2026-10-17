package applications

import (
	"context"
	"strings"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/tenancy"
)

const entityType = "institute_application"

type Service struct {
	Store   Store
	History audit.Reader
}

func NewService(store Store, history audit.Reader) *Service {
	return &Service{Store: store, History: history}
}

func invalid(p map[string]string) error {
	if len(p) == 0 {
		return nil
	}
	return &ValidationError{Problems: p}
}

func event(actor, action, requestID string, meta map[string]any) audit.Event {
	return audit.Event{ActorType: audit.ActorUser, ActorUserID: actor, Action: action, EntityType: entityType, RequestID: requestID, Metadata: meta}
}

// Create makes a draft. Any subset of details is accepted; each provided field is validated.
func (s *Service) Create(ctx context.Context, applicantID string, p Patch, requestID string) (Application, error) {
	d := Details{}.Apply(p).Normalize()
	if err := invalid(ValidateFormat(d)); err != nil {
		return Application{}, err
	}
	return s.Store.Create(ctx, applicantID, d, event(applicantID, "application.created", requestID, nil))
}

func (s *Service) Update(ctx context.Context, applicantID, id string, p Patch, requestID string) (Application, error) {
	cur, err := s.Store.GetForApplicant(ctx, applicantID, id)
	if err != nil {
		return Application{}, err
	}
	if !cur.Status.Editable() {
		return Application{}, ErrNotEditable
	}
	d := cur.Details.Apply(p).Normalize()
	if err := invalid(ValidateFormat(d)); err != nil {
		return Application{}, err
	}
	return s.Store.UpdateDetails(ctx, applicantID, id, d, event(applicantID, "application.updated", requestID, nil))
}

func (s *Service) Submit(ctx context.Context, applicantID, id, requestID string) (Application, error) {
	cur, err := s.Store.GetForApplicant(ctx, applicantID, id)
	if err != nil {
		return Application{}, err
	}
	if !CanTransition(cur.Status, Submitted) {
		return Application{}, ErrInvalidTransition
	}
	d := cur.Details.Normalize()
	if err := invalid(CheckSubmittable(d)); err != nil {
		return Application{}, err
	}
	return s.Store.Transition(ctx, Transition{ID: id, ApplicantID: applicantID, From: SourcesOf(Submitted), To: Submitted,
		ActorUserID: applicantID, Details: &d, Event: event(applicantID, AuditAction(Submitted), requestID, map[string]any{"from": string(cur.Status)})})
}

func (s *Service) Mine(ctx context.Context, applicantID string) ([]Application, error) {
	return s.Store.ListForApplicant(ctx, applicantID)
}

func (s *Service) MineByID(ctx context.Context, applicantID, id string) (Application, error) {
	return s.Store.GetForApplicant(ctx, applicantID, id)
}

// --- Operations ---

func (s *Service) OpsList(ctx context.Context, f ListFilter) ([]Application, error) {
	return s.Store.ListForReview(ctx, f.Normalized())
}

func (s *Service) OpsGet(ctx context.Context, id string) (Application, error) {
	return s.Store.GetForReview(ctx, id)
}

func (s *Service) OpsHistory(ctx context.Context, id string) ([]audit.Record, error) {
	if _, err := s.Store.GetForReview(ctx, id); err != nil {
		return nil, err
	}
	return s.History.List(ctx, audit.Filter{EntityType: entityType, EntityID: id, Limit: 100})
}

// reviewable loads the application for a reviewer and blocks self-review (conflict of interest).
func (s *Service) reviewable(ctx context.Context, reviewerID, id string) (Application, error) {
	cur, err := s.Store.GetForReview(ctx, id)
	if err != nil {
		return Application{}, err
	}
	if cur.ApplicantUserID == reviewerID {
		return Application{}, ErrSelfReview
	}
	return cur, nil
}

func (s *Service) StartReview(ctx context.Context, reviewerID, id, requestID string) (Application, error) {
	cur, err := s.reviewable(ctx, reviewerID, id)
	if err != nil {
		return Application{}, err
	}
	return s.Store.Transition(ctx, Transition{ID: id, From: SourcesOf(UnderReview), To: UnderReview, ActorUserID: reviewerID,
		Event: event(reviewerID, AuditAction(UnderReview), requestID, map[string]any{"from": string(cur.Status)})})
}

func reasonProblem(reason string) map[string]string {
	if n := len([]rune(strings.TrimSpace(reason))); n < 10 || n > 2000 {
		return map[string]string{"reason": "Give a reason of 10–2000 characters so the applicant knows what happens next."}
	}
	return nil
}

func (s *Service) Reject(ctx context.Context, reviewerID, id, reason, requestID string) (Application, error) {
	return s.decide(ctx, reviewerID, id, reason, requestID, Rejected)
}

func (s *Service) RequestChanges(ctx context.Context, reviewerID, id, reason, requestID string) (Application, error) {
	return s.decide(ctx, reviewerID, id, reason, requestID, ChangesRequested)
}

func (s *Service) decide(ctx context.Context, reviewerID, id, reason, requestID string, to Status) (Application, error) {
	if err := invalid(reasonProblem(reason)); err != nil {
		return Application{}, err
	}
	if _, err := s.reviewable(ctx, reviewerID, id); err != nil {
		return Application{}, err
	}
	reason = strings.TrimSpace(reason)
	return s.Store.Transition(ctx, Transition{ID: id, From: SourcesOf(to), To: to, ActorUserID: reviewerID, Reason: reason,
		Event: event(reviewerID, AuditAction(to), requestID, map[string]any{"from": string(UnderReview), "reason": reason})})
}

func (s *Service) Approve(ctx context.Context, reviewerID, id, note, requestID string) (Application, tenancy.Institute, error) {
	note = strings.TrimSpace(note)
	if len([]rune(note)) > 2000 {
		return Application{}, tenancy.Institute{}, invalid(map[string]string{"note": "Note is too long (max 2000 characters)."})
	}
	if _, err := s.reviewable(ctx, reviewerID, id); err != nil {
		return Application{}, tenancy.Institute{}, err
	}
	return s.Store.Approve(ctx, ApproveInput{ID: id, ReviewerID: reviewerID, Note: note, RequestID: requestID})
}
