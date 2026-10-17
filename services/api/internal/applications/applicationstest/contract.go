package applicationstest

import (
	"context"
	"errors"
	"testing"

	"github.com/certificate-os/api/internal/applications"
	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/tenancy"
)

// Harness gives the contract everything it needs from a concrete store implementation.
type Harness struct {
	Store applications.Store
	// Tenants is the same institute store the Store approves into.
	Tenants interface {
		tenancy.Store
		tenancy.AdminStore
	}
	NewUser      func(email string) string
	AuditActions func(entityType, entityID string) []string
}

// ValidDetails returns complete, valid application details for the given slug.
func ValidDetails(slug string) applications.Details {
	return applications.Details{
		Name: "Alpha Academy", Slug: slug, Category: "academy", AddressLine1: "12 Main Road", City: "Pune",
		Country: "IN", OfficialEmail: "office@alpha.example", Phone: "+91 20 5550 1234",
		RepName: "Asha Rao", RepRole: "Director", Attested: true,
	}
}

func ev(userID, action string) audit.Event {
	return audit.Event{ActorType: audit.ActorUser, ActorUserID: userID, Action: action, EntityType: "institute_application"}
}

func has(actions []string, want string) bool {
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}

// RunStoreContract verifies an applications.Store. newHarness must return an EMPTY database.
func RunStoreContract(t *testing.T, newHarness func(t *testing.T) Harness) {
	ctx := context.Background()
	submit := func(t *testing.T, h Harness, uid string, d applications.Details) applications.Application {
		t.Helper()
		a, err := h.Store.Create(ctx, uid, d, ev(uid, "application.created"))
		if err != nil {
			t.Fatal(err)
		}
		d = d.Normalize()
		a, err = h.Store.Transition(ctx, applications.Transition{ID: a.ID, ApplicantID: uid,
			From: []applications.Status{applications.Draft, applications.ChangesRequested}, To: applications.Submitted,
			ActorUserID: uid, Details: &d, Event: ev(uid, "application.submitted")})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	review := func(t *testing.T, h Harness, id, reviewer string) {
		t.Helper()
		if _, err := h.Store.Transition(ctx, applications.Transition{ID: id, From: []applications.Status{applications.Submitted},
			To: applications.UnderReview, ActorUserID: reviewer, Event: ev(reviewer, "application.review_started")}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("create, ownership, one open application", func(t *testing.T) {
		h := newHarness(t)
		u1, u2 := h.NewUser("u1@x.com"), h.NewUser("u2@x.com")
		a, err := h.Store.Create(ctx, u1, ValidDetails("alpha-academy"), ev(u1, "application.created"))
		if err != nil || a.Status != applications.Draft || a.Version != 1 || a.ID == "" {
			t.Fatalf("create: %+v %v", a, err)
		}
		if !has(h.AuditActions("institute_application", a.ID), "application.created") {
			t.Fatal("create not audited with the new entity id")
		}
		if _, err := h.Store.Create(ctx, u1, ValidDetails("other-slug"), ev(u1, "application.created")); !errors.Is(err, applications.ErrOpenApplicationExists) {
			t.Fatalf("second open application: %v", err)
		}
		if _, err := h.Store.GetForApplicant(ctx, u2, a.ID); !errors.Is(err, applications.ErrNotFound) {
			t.Fatalf("foreign get must be not found: %v", err)
		}
		if _, err := h.Store.GetForApplicant(ctx, u1, "00000000-0000-0000-0000-000000000000"); !errors.Is(err, applications.ErrNotFound) {
			t.Fatalf("unknown id: %v", err)
		}
		if _, err := h.Store.GetForApplicant(ctx, u1, "not-a-uuid"); !errors.Is(err, applications.ErrNotFound) {
			t.Fatalf("malformed id must be not found, not an error: %v", err)
		}
		if l, _ := h.Store.ListForApplicant(ctx, u2); len(l) != 0 {
			t.Fatalf("list leaked: %+v", l)
		}
	})

	t.Run("updates only while editable", func(t *testing.T) {
		h := newHarness(t)
		u := h.NewUser("u@x.com")
		a, _ := h.Store.Create(ctx, u, applications.Details{Name: "Draft Name"}, ev(u, "application.created"))
		d := ValidDetails("alpha-academy")
		a2, err := h.Store.UpdateDetails(ctx, u, a.ID, d, ev(u, "application.updated"))
		if err != nil || a2.Version != 2 || a2.Details.Slug != "alpha-academy" {
			t.Fatalf("update: %+v %v", a2, err)
		}
		if _, err := h.Store.UpdateDetails(ctx, h.NewUser("other@x.com"), a.ID, d, ev(u, "application.updated")); !errors.Is(err, applications.ErrNotFound) {
			t.Fatalf("foreign update: %v", err)
		}
		submit2 := d.Normalize()
		_, _ = h.Store.Transition(ctx, applications.Transition{ID: a.ID, ApplicantID: u, From: []applications.Status{applications.Draft},
			To: applications.Submitted, ActorUserID: u, Details: &submit2, Event: ev(u, "application.submitted")})
		if _, err := h.Store.UpdateDetails(ctx, u, a.ID, d, ev(u, "application.updated")); !errors.Is(err, applications.ErrNotEditable) {
			t.Fatalf("update after submit: %v", err)
		}
	})

	t.Run("guarded transitions are atomic with audit", func(t *testing.T) {
		h := newHarness(t)
		u, ops := h.NewUser("u@x.com"), h.NewUser("ops@x.com")
		a := submit(t, h, u, ValidDetails("alpha-academy"))
		if a.Status != applications.Submitted || a.SubmittedAt == nil {
			t.Fatalf("submit: %+v", a)
		}
		before := len(h.AuditActions("institute_application", a.ID))
		// Submitting again from submitted is invalid and must write nothing.
		d := a.Details
		if _, err := h.Store.Transition(ctx, applications.Transition{ID: a.ID, ApplicantID: u, From: []applications.Status{applications.Draft, applications.ChangesRequested},
			To: applications.Submitted, ActorUserID: u, Details: &d, Event: ev(u, "application.submitted")}); !errors.Is(err, applications.ErrInvalidTransition) {
			t.Fatalf("double submit: %v", err)
		}
		// Rejecting straight from submitted (skipping review) is invalid.
		if _, err := h.Store.Transition(ctx, applications.Transition{ID: a.ID, From: []applications.Status{applications.UnderReview},
			To: applications.Rejected, ActorUserID: ops, Reason: "no", Event: ev(ops, "application.rejected")}); !errors.Is(err, applications.ErrInvalidTransition) {
			t.Fatalf("skip review: %v", err)
		}
		if after := len(h.AuditActions("institute_application", a.ID)); after != before {
			t.Fatalf("failed transitions wrote audit rows: %d -> %d", before, after)
		}
		review(t, h, a.ID, ops)
		got, _ := h.Store.GetForReview(ctx, a.ID)
		if got.Status != applications.UnderReview || got.ReviewedBy != ops {
			t.Fatalf("review: %+v", got)
		}
		rej, err := h.Store.Transition(ctx, applications.Transition{ID: a.ID, From: []applications.Status{applications.UnderReview},
			To: applications.Rejected, ActorUserID: ops, Reason: "Documents unclear", Event: ev(ops, "application.rejected")})
		if err != nil || rej.Status != applications.Rejected || rej.DecisionReason != "Documents unclear" || rej.ReviewedAt == nil {
			t.Fatalf("reject: %+v %v", rej, err)
		}
		if _, err := h.Store.Transition(ctx, applications.Transition{ID: a.ID, From: []applications.Status{applications.UnderReview},
			To: applications.Rejected, ActorUserID: ops, Reason: "again", Event: ev(ops, "application.rejected")}); !errors.Is(err, applications.ErrInvalidTransition) {
			t.Fatalf("decide twice: %v", err)
		}
		// Rejected is terminal for editing but frees the applicant to start over.
		if _, err := h.Store.Create(ctx, u, ValidDetails("alpha-academy"), ev(u, "application.created")); err != nil {
			t.Fatalf("new application after rejection: %v", err)
		}
	})

	t.Run("changes requested allows edit and resubmission", func(t *testing.T) {
		h := newHarness(t)
		u, ops := h.NewUser("u@x.com"), h.NewUser("ops@x.com")
		a := submit(t, h, u, ValidDetails("alpha-academy"))
		review(t, h, a.ID, ops)
		cr, err := h.Store.Transition(ctx, applications.Transition{ID: a.ID, From: []applications.Status{applications.UnderReview},
			To: applications.ChangesRequested, ActorUserID: ops, Reason: "Fix the address", Event: ev(ops, "application.changes_requested")})
		if err != nil || cr.Status != applications.ChangesRequested {
			t.Fatalf("%+v %v", cr, err)
		}
		d := ValidDetails("alpha-academy")
		d.AddressLine1 = "99 New Street"
		if _, err := h.Store.UpdateDetails(ctx, u, a.ID, d, ev(u, "application.updated")); err != nil {
			t.Fatalf("edit while changes requested: %v", err)
		}
		d = d.Normalize()
		re, err := h.Store.Transition(ctx, applications.Transition{ID: a.ID, ApplicantID: u,
			From: []applications.Status{applications.Draft, applications.ChangesRequested}, To: applications.Submitted,
			ActorUserID: u, Details: &d, Event: ev(u, "application.submitted")})
		if err != nil || re.Status != applications.Submitted || re.Details.AddressLine1 != "99 New Street" {
			t.Fatalf("resubmit: %+v %v", re, err)
		}
	})

	t.Run("slug claims", func(t *testing.T) {
		h := newHarness(t)
		u1, u2, ops := h.NewUser("u1@x.com"), h.NewUser("u2@x.com"), h.NewUser("ops@x.com")
		first := submit(t, h, u1, ValidDetails("shared-slug"))

		b, _ := h.Store.Create(ctx, u2, ValidDetails("shared-slug"), ev(u2, "application.created"))
		d := b.Details.Normalize()
		trySubmit := func() error {
			_, err := h.Store.Transition(ctx, applications.Transition{ID: b.ID, ApplicantID: u2,
				From: []applications.Status{applications.Draft, applications.ChangesRequested}, To: applications.Submitted,
				ActorUserID: u2, Details: &d, Event: ev(u2, "application.submitted")})
			return err
		}
		if err := trySubmit(); !errors.Is(err, applications.ErrSlugTaken) {
			t.Fatalf("duplicate claim: %v", err)
		}
		// Rejection releases the slug.
		review(t, h, first.ID, ops)
		_, _ = h.Store.Transition(ctx, applications.Transition{ID: first.ID, From: []applications.Status{applications.UnderReview},
			To: applications.Rejected, ActorUserID: ops, Reason: "Not eligible", Event: ev(ops, "application.rejected")})
		if err := trySubmit(); err != nil {
			t.Fatalf("slug should be free after rejection: %v", err)
		}
	})

	t.Run("slug used by an existing institute", func(t *testing.T) {
		h := newHarness(t)
		u := h.NewUser("u@x.com")
		if _, err := h.Tenants.CreateInstitute(ctx, "taken-name", "Existing"); err != nil {
			t.Fatal(err)
		}
		a, _ := h.Store.Create(ctx, u, ValidDetails("taken-name"), ev(u, "application.created"))
		d := a.Details.Normalize()
		if _, err := h.Store.Transition(ctx, applications.Transition{ID: a.ID, ApplicantID: u, From: []applications.Status{applications.Draft},
			To: applications.Submitted, ActorUserID: u, Details: &d, Event: ev(u, "application.submitted")}); !errors.Is(err, applications.ErrSlugTaken) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("operations never see drafts", func(t *testing.T) {
		h := newHarness(t)
		u1, u2 := h.NewUser("u1@x.com"), h.NewUser("u2@x.com")
		draft, _ := h.Store.Create(ctx, u1, ValidDetails("draft-one"), ev(u1, "application.created"))
		sub := submit(t, h, u2, ValidDetails("submitted-one"))
		if _, err := h.Store.GetForReview(ctx, draft.ID); !errors.Is(err, applications.ErrNotFound) {
			t.Fatalf("draft visible to ops: %v", err)
		}
		list, _ := h.Store.ListForReview(ctx, applications.ListFilter{})
		if len(list) != 1 || list[0].ID != sub.ID {
			t.Fatalf("queue: %+v", list)
		}
		if _, err := h.Store.Transition(ctx, applications.Transition{ID: draft.ID, From: []applications.Status{applications.Submitted},
			To: applications.UnderReview, ActorUserID: u2, Event: ev(u2, "application.review_started")}); !errors.Is(err, applications.ErrNotFound) {
			t.Fatalf("ops transition on a draft must be not found: %v", err)
		}
		if filtered, _ := h.Store.ListForReview(ctx, applications.ListFilter{Statuses: []applications.Status{applications.Approved}}); len(filtered) != 0 {
			t.Fatalf("filter ignored: %+v", filtered)
		}
	})

	t.Run("approval creates institute and owner atomically", func(t *testing.T) {
		h := newHarness(t)
		u, ops := h.NewUser("u@x.com"), h.NewUser("ops@x.com")
		a := submit(t, h, u, ValidDetails("alpha-academy"))
		if _, _, err := h.Store.Approve(ctx, applications.ApproveInput{ID: a.ID, ReviewerID: ops}); !errors.Is(err, applications.ErrInvalidTransition) {
			t.Fatalf("approve before review: %v", err)
		}
		review(t, h, a.ID, ops)
		app, inst, err := h.Store.Approve(ctx, applications.ApproveInput{ID: a.ID, ReviewerID: ops, RequestID: "req-12345678"})
		if err != nil {
			t.Fatal(err)
		}
		if app.Status != applications.Approved || app.InstituteID != inst.ID || inst.Slug != "alpha-academy" || inst.Name != "Alpha Academy" || inst.Status != tenancy.InstituteActive {
			t.Fatalf("approved: %+v %+v", app, inst)
		}
		ms, _ := h.Tenants.MembershipsForUser(ctx, u)
		if len(ms) != 1 || ms[0].Institute.ID != inst.ID || ms[0].Role != auth.InstituteOwner {
			t.Fatalf("applicant must become owner: %+v", ms)
		}
		acts := h.AuditActions("institute_application", a.ID)
		if !has(acts, "application.approved") || !has(h.AuditActions("institute", inst.ID), "institute.created") ||
			!has(h.AuditActions("institute_membership", inst.ID+":"+u), "membership.created") {
			t.Fatalf("approval audit trail incomplete: %v", acts)
		}
		if _, _, err := h.Store.Approve(ctx, applications.ApproveInput{ID: a.ID, ReviewerID: ops}); !errors.Is(err, applications.ErrInvalidTransition) {
			t.Fatalf("repeated approval must conflict: %v", err)
		}
	})

	t.Run("approval conflict changes nothing", func(t *testing.T) {
		h := newHarness(t)
		u, ops := h.NewUser("u@x.com"), h.NewUser("ops@x.com")
		a := submit(t, h, u, ValidDetails("late-conflict"))
		review(t, h, a.ID, ops)
		// Someone creates the institute out-of-band between submission and approval.
		if _, err := h.Tenants.CreateInstitute(ctx, "late-conflict", "Created by CLI"); err != nil {
			t.Fatal(err)
		}
		auditBefore := len(h.AuditActions("institute_application", a.ID))
		if _, _, err := h.Store.Approve(ctx, applications.ApproveInput{ID: a.ID, ReviewerID: ops}); !errors.Is(err, applications.ErrSlugTaken) {
			t.Fatalf("got %v", err)
		}
		got, _ := h.Store.GetForReview(ctx, a.ID)
		if got.Status != applications.UnderReview || got.InstituteID != "" {
			t.Fatalf("application changed despite failure: %+v", got)
		}
		if ms, _ := h.Tenants.MembershipsForUser(ctx, u); len(ms) != 0 {
			t.Fatalf("owner membership created despite failure: %+v", ms)
		}
		if len(h.AuditActions("institute_application", a.ID)) != auditBefore {
			t.Fatal("audit rows written despite failure")
		}
	})
}
