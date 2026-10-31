package certtest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/certificates"
	"github.com/certificate-os/api/internal/tenancy"
)

// Harness wires one store implementation into the shared contract.
type Harness struct {
	Store        certificates.Store
	NewScope     func() tenancy.Scope
	NewUser      func() string
	NewFixture   func(s tenancy.Scope, o FixtureOpts) Refs
	AuditActions func(entityID string) []string
	AuditTotal   func(instituteID string) int
	AuditMeta    func(entityID string) map[string]any // metadata of the newest row of the entity
}

func ev(action string) audit.Event {
	return audit.Event{ActorType: audit.ActorSystem, Action: action, EntityType: "certificate"}
}

// EventForTest is a minimal valid audit event for tests of other packages.
func EventForTest() audit.Event { return ev("certificate.test") }

func is(t *testing.T, err, want error, what string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", what, err, want)
	}
}

var t0 = time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)

var pubSeq = 0

func pub() string {
	pubSeq++
	s := strings.Repeat("a", 43)
	n := []byte(s)
	for i, v := 0, pubSeq; v > 0; i, v = i+1, v/26 {
		n[i] = byte('a' + v%26)
	}
	return string(n)
}

// RunStoreContract is the executable specification every certificates.Store must satisfy.
func RunStoreContract(t *testing.T, newHarness func(t *testing.T) Harness) {
	ctx := context.Background()
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	begin := func(t *testing.T, h Harness, s tenancy.Scope, r Refs, u string, at time.Time) (certificates.Certificate, error) {
		return h.Store.BeginIssue(ctx, s, certificates.BeginInput{EnrollmentID: r.EnrollmentID, TemplateID: r.TemplateID, PublicID: pub(), IssuedBy: u, Now: at}, ev("certificate.issue_started"))
	}
	issue := func(t *testing.T, h Harness, s tenancy.Scope, r Refs, u string, at time.Time) certificates.Certificate {
		t.Helper()
		c, err := begin(t, h, s, r, u, at)
		must(t, err)
		c, err = h.Store.Complete(ctx, s, c.ID, []byte("%PDF-1.4 fake "+c.Number), ev("certificate.issued"))
		must(t, err)
		return c
	}

	t.Run("begin makes a pending certificate with a snapshot; complete stores the pdf and checksum; audit trail", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		r := h.NewFixture(s, FixtureOpts{Grade: "A"})
		c, err := begin(t, h, s, r, u, t0)
		must(t, err)
		if c.Status != certificates.Pending || c.Number != "CERT-2026-000001" || c.TemplateVersion != 1 || c.PDFSHA256 != "" {
			t.Fatalf("bad pending certificate: %+v", c)
		}
		sn := c.Snapshot
		if sn.CertificateNumber != c.Number || sn.StudentName != "Asha Verma" || !strings.HasPrefix(sn.CourseName, "Web Design") || sn.BatchName != "Batch A" ||
			sn.CompletionDate != "2026-03-15" || sn.IssueDate != "2026-10-24" || sn.Grade != "A" || sn.TemplateVersion != 1 || sn.Config.Title != "Certificate v1" {
			t.Fatalf("bad snapshot: %+v", sn)
		}
		_, err = h.Store.GetPDF(ctx, s, c.ID)
		is(t, err, certificates.ErrNotFound, "pdf of a pending certificate")

		pdf := []byte("%PDF-1.4 hello")
		done, err := h.Store.Complete(ctx, s, c.ID, pdf, ev("certificate.issued"))
		must(t, err)
		sum := sha256.Sum256(pdf)
		if done.Status != certificates.Issued || done.PDFSHA256 != hex.EncodeToString(sum[:]) || done.PDFSize != len(pdf) || done.IssuedAt == nil {
			t.Fatalf("bad issued certificate: %+v", done)
		}
		got, err := h.Store.GetPDF(ctx, s, c.ID)
		must(t, err)
		if !bytes.Equal(got.Bytes, pdf) || got.SHA256 != done.PDFSHA256 || got.Number != c.Number {
			t.Fatalf("pdf round trip failed")
		}
		g, err := h.Store.Get(ctx, s, c.ID)
		must(t, err)
		if g.Status != certificates.Issued || g.Snapshot.StudentName != "Asha Verma" || g.PublicID != c.PublicID {
			t.Fatalf("bad get: %+v", g)
		}
		if a := strings.Join(h.AuditActions(c.ID), ","); a != "certificate.issue_started,certificate.issued" {
			t.Fatalf("audit = %s", a)
		}
		if n := h.AuditMeta(c.ID)["number"]; n != c.Number {
			t.Fatalf("audit metadata number = %v", n)
		}
	})

	t.Run("numbers are sequential per institute and year, and independent between institutes", func(t *testing.T) {
		h := newHarness(t)
		a, b, u := h.NewScope(), h.NewScope(), h.NewUser()
		var nums []string
		for i := 0; i < 3; i++ {
			nums = append(nums, issue(t, h, a, h.NewFixture(a, FixtureOpts{}), u, t0).Number)
		}
		if strings.Join(nums, ",") != "CERT-2026-000001,CERT-2026-000002,CERT-2026-000003" {
			t.Fatalf("numbers = %v", nums)
		}
		if n := issue(t, h, b, h.NewFixture(b, FixtureOpts{}), u, t0).Number; n != "CERT-2026-000001" {
			t.Fatalf("institute B starts at %s", n)
		}
		if n := issue(t, h, a, h.NewFixture(a, FixtureOpts{}), u, time.Date(2027, 1, 2, 0, 0, 0, 0, time.UTC)).Number; n != "CERT-2027-000001" {
			t.Fatalf("new year starts at %s", n)
		}
		if n := issue(t, h, a, h.NewFixture(a, FixtureOpts{}), u, t0).Number; n != "CERT-2026-000004" {
			t.Fatalf("old year continues at %s", n)
		}
	})

	t.Run("one live certificate per enrollment; failed, revoked and abandoned ones free the slot", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		r := h.NewFixture(s, FixtureOpts{})
		c1, err := begin(t, h, s, r, u, t0)
		must(t, err)
		_, err = begin(t, h, s, r, u, t0.Add(time.Minute))
		is(t, err, certificates.ErrAlreadyIssued, "second begin while pending")
		_, err = h.Store.Fail(ctx, s, c1.ID, "pdf rendering failed", ev("certificate.issue_failed"))
		must(t, err)
		c2, err := begin(t, h, s, r, u, t0.Add(time.Minute))
		must(t, err)
		if c2.Number != "CERT-2026-000002" {
			t.Fatalf("failed numbers are never reused, got %s", c2.Number)
		}
		_, err = h.Store.Complete(ctx, s, c2.ID, []byte("%PDF-x"), ev("certificate.issued"))
		must(t, err)
		_, err = begin(t, h, s, r, u, t0.Add(time.Hour))
		is(t, err, certificates.ErrAlreadyIssued, "begin while issued")
		_, err = h.Store.Revoke(ctx, s, c2.ID, "issued by mistake", u, ev("certificate.revoked"))
		must(t, err)
		c3, err := begin(t, h, s, r, u, t0.Add(2*time.Hour))
		must(t, err)
		if c3.Number != "CERT-2026-000003" {
			t.Fatalf("got %s", c3.Number)
		}
		// A pending certificate abandoned for longer than StalePendingAfter is replaced.
		r2 := h.NewFixture(s, FixtureOpts{})
		old, err := begin(t, h, s, r2, u, t0)
		must(t, err)
		_, err = begin(t, h, s, r2, u, t0.Add(certificates.StalePendingAfter-time.Second))
		is(t, err, certificates.ErrAlreadyIssued, "fresh pending still blocks")
		fresh, err := begin(t, h, s, r2, u, t0.Add(certificates.StalePendingAfter+time.Second))
		must(t, err)
		g, _ := h.Store.Get(ctx, s, old.ID)
		if g.Status != certificates.Failed || g.Failure != "abandoned" || fresh.Status != certificates.Pending {
			t.Fatalf("stale pending not abandoned: %+v", g)
		}
	})

	t.Run("ineligible or unknown inputs are refused without using a number or writing audit rows", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		before := h.AuditTotal(s.InstituteID())
		var el *certificates.EligibilityError
		for name, o := range map[string]FixtureOpts{"incomplete": {Incomplete: true}, "archived student": {StudentArchived: true}, "archived template": {TemplateArchived: true}} {
			_, err := begin(t, h, s, h.NewFixture(s, o), u, t0)
			if !errors.As(err, &el) || el.Reason == "" || el.Field == "" {
				t.Fatalf("%s: got %v", name, err)
			}
		}
		good := h.NewFixture(s, FixtureOpts{})
		_, err := h.Store.BeginIssue(ctx, s, certificates.BeginInput{EnrollmentID: "00000000-0000-0000-0000-00000000dead", TemplateID: good.TemplateID, PublicID: pub(), IssuedBy: u, Now: t0}, ev("certificate.test"))
		is(t, err, certificates.ErrNotFound, "unknown enrollment")
		_, err = h.Store.BeginIssue(ctx, s, certificates.BeginInput{EnrollmentID: good.EnrollmentID, TemplateID: "00000000-0000-0000-0000-00000000dead", PublicID: pub(), IssuedBy: u, Now: t0}, ev("certificate.test"))
		is(t, err, certificates.ErrNotFound, "unknown template")
		_, err = h.Store.BeginIssue(ctx, s, certificates.BeginInput{EnrollmentID: good.EnrollmentID, TemplateID: good.TemplateID, Version: 9, PublicID: pub(), IssuedBy: u, Now: t0}, ev("certificate.test"))
		is(t, err, certificates.ErrNotFound, "unknown template version")
		if after := h.AuditTotal(s.InstituteID()); after != before {
			t.Fatalf("audit rows %d -> %d after refusals", before, after)
		}
		if c := issue(t, h, s, good, u, t0); c.Number != "CERT-2026-000001" {
			t.Fatalf("a refusal consumed a number: %s", c.Number)
		}
	})

	t.Run("a pinned template version is snapshotted and never changes afterwards", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		r := h.NewFixture(s, FixtureOpts{Versions: 3})
		c, err := h.Store.BeginIssue(ctx, s, certificates.BeginInput{EnrollmentID: r.EnrollmentID, TemplateID: r.TemplateID, Version: 2, PublicID: pub(), IssuedBy: u, Now: t0}, ev("certificate.test"))
		must(t, err)
		if c.TemplateVersion != 2 || c.Snapshot.Config.Title != "Certificate v2" || c.Snapshot.TemplateVersion != 2 {
			t.Fatalf("wrong version: %+v", c.Snapshot)
		}
		cur := h.NewFixture(s, FixtureOpts{Versions: 3})
		d, err := begin(t, h, s, cur, u, t0)
		must(t, err)
		if d.TemplateVersion != 3 || d.Snapshot.Config.Title != "Certificate v3" {
			t.Fatalf("default should be the current version: %+v", d.Snapshot.Config.Title)
		}
	})

	t.Run("complete and fail only work on pending certificates", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		a := issue(t, h, s, h.NewFixture(s, FixtureOpts{}), u, t0)
		_, err := h.Store.Complete(ctx, s, a.ID, []byte("%PDF-2"), ev("certificate.test"))
		is(t, err, certificates.ErrState, "complete twice")
		_, err = h.Store.Fail(ctx, s, a.ID, "late", ev("certificate.test"))
		is(t, err, certificates.ErrState, "fail after issued")
		got, _ := h.Store.GetPDF(ctx, s, a.ID)
		if !bytes.HasPrefix(got.Bytes, []byte("%PDF-1.4 fake")) {
			t.Fatal("the first pdf must stay")
		}
		p, err := begin(t, h, s, h.NewFixture(s, FixtureOpts{}), u, t0)
		must(t, err)
		_, err = h.Store.Complete(ctx, s, p.ID, nil, ev("certificate.test"))
		if err == nil {
			t.Fatal("an empty pdf must be refused")
		}
		g, _ := h.Store.Get(ctx, s, p.ID)
		if g.Status != certificates.Pending {
			t.Fatalf("a refused pdf changed the status to %s", g.Status)
		}
		_, err = h.Store.Fail(ctx, s, p.ID, "pdf rendering failed", ev("certificate.test"))
		must(t, err)
		_, err = h.Store.Complete(ctx, s, p.ID, []byte("%PDF-3"), ev("certificate.test"))
		is(t, err, certificates.ErrState, "complete after failed")
		_, err = h.Store.GetPDF(ctx, s, p.ID)
		is(t, err, certificates.ErrNotFound, "pdf of a failed certificate")
	})

	t.Run("revoke: only issued, keeps the pdf, records the audit event", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		c := issue(t, h, s, h.NewFixture(s, FixtureOpts{}), u, t0)
		r, err := h.Store.Revoke(ctx, s, c.ID, "Wrong name printed", u, ev("certificate.revoked"))
		must(t, err)
		if r.Status != certificates.Revoked || r.RevokedAt == nil || r.RevokeReason != "Wrong name printed" || r.PDFSHA256 != c.PDFSHA256 {
			t.Fatalf("bad revoked: %+v", r)
		}
		if _, err := h.Store.GetPDF(ctx, s, c.ID); err != nil {
			t.Fatal("revoked certificates keep their pdf:", err)
		}
		_, err = h.Store.Revoke(ctx, s, c.ID, "again", u, ev("certificate.test"))
		is(t, err, certificates.ErrState, "revoke twice")
		p, err := begin(t, h, s, h.NewFixture(s, FixtureOpts{}), u, t0)
		must(t, err)
		_, err = h.Store.Revoke(ctx, s, p.ID, "pending", u, ev("certificate.test"))
		is(t, err, certificates.ErrState, "revoke pending")
		if a := strings.Join(h.AuditActions(c.ID), ","); a != "certificate.issue_started,certificate.issued,certificate.revoked" {
			t.Fatalf("audit = %s", a)
		}
	})

	t.Run("list: newest first, filters and search, probe row", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		r1 := h.NewFixture(s, FixtureOpts{StudentName: "Asha Verma"})
		r2 := h.NewFixture(s, FixtureOpts{StudentName: "Ravi Kumar"})
		r3 := h.NewFixture(s, FixtureOpts{StudentName: "Meera Nair"})
		c1 := issue(t, h, s, r1, u, t0)
		c2 := issue(t, h, s, r2, u, t0.Add(time.Minute))
		c3, err := begin(t, h, s, r3, u, t0.Add(2*time.Minute))
		must(t, err)
		_, err = h.Store.Revoke(ctx, s, c2.ID, "mistake", u, ev("certificate.test"))
		must(t, err)
		l, err := h.Store.List(ctx, s, certificates.Filter{Limit: 50})
		must(t, err)
		if len(l) != 3 || l[0].ID != c3.ID || l[2].ID != c1.ID {
			t.Fatalf("order: %+v", l)
		}
		l, _ = h.Store.List(ctx, s, certificates.Filter{Status: certificates.Revoked, Limit: 50})
		if len(l) != 1 || l[0].ID != c2.ID {
			t.Fatalf("status filter: %+v", l)
		}
		l, _ = h.Store.List(ctx, s, certificates.Filter{StudentID: r1.StudentID, Limit: 50})
		if len(l) != 1 || l[0].ID != c1.ID {
			t.Fatalf("student filter: %+v", l)
		}
		l, _ = h.Store.List(ctx, s, certificates.Filter{Q: "ravi", Limit: 50})
		if len(l) != 1 || l[0].ID != c2.ID {
			t.Fatalf("name search: %+v", l)
		}
		l, _ = h.Store.List(ctx, s, certificates.Filter{Q: "cert-2026-000001", Limit: 50})
		if len(l) != 1 || l[0].ID != c1.ID {
			t.Fatalf("number search: %+v", l)
		}
		l, _ = h.Store.List(ctx, s, certificates.Filter{Q: "100%", Limit: 50}) // wildcard characters are literal
		if len(l) != 0 {
			t.Fatalf("wildcard search matched %d", len(l))
		}
		l, _ = h.Store.List(ctx, s, certificates.Filter{Limit: 2})
		if len(l) != 3 {
			t.Fatalf("probe row: got %d", len(l))
		}
		l, _ = h.Store.List(ctx, s, certificates.Filter{Limit: 2, Offset: 2})
		if len(l) != 1 {
			t.Fatalf("offset: got %d", len(l))
		}
	})

	t.Run("isolation: another institute sees nothing and changes nothing", func(t *testing.T) {
		h := newHarness(t)
		a, b, ua, ub := h.NewScope(), h.NewScope(), h.NewUser(), h.NewUser()
		ra := h.NewFixture(a, FixtureOpts{})
		c := issue(t, h, a, ra, ua, t0)
		pend, err := begin(t, h, a, h.NewFixture(a, FixtureOpts{}), ua, t0)
		must(t, err)
		_, err = h.Store.Get(ctx, b, c.ID)
		is(t, err, certificates.ErrNotFound, "get")
		_, err = h.Store.GetPDF(ctx, b, c.ID)
		is(t, err, certificates.ErrNotFound, "pdf")
		_, err = h.Store.Revoke(ctx, b, c.ID, "hijack", ub, ev("certificate.test"))
		is(t, err, certificates.ErrNotFound, "revoke")
		_, err = h.Store.Complete(ctx, b, pend.ID, []byte("%PDF-x"), ev("certificate.test"))
		is(t, err, certificates.ErrNotFound, "complete")
		_, err = h.Store.Fail(ctx, b, pend.ID, "x", ev("certificate.test"))
		is(t, err, certificates.ErrNotFound, "fail")
		_, err = begin(t, h, b, ra, ub, t0)
		is(t, err, certificates.ErrNotFound, "issue from another institute's enrollment")
		l, err := h.Store.List(ctx, b, certificates.Filter{Limit: 50})
		must(t, err)
		if len(l) != 0 {
			t.Fatalf("B lists %d certificates", len(l))
		}
		g, _ := h.Store.Get(ctx, a, c.ID)
		if g.Status != certificates.Issued {
			t.Fatalf("A's certificate changed: %s", g.Status)
		}
		gp, _ := h.Store.Get(ctx, a, pend.ID)
		if gp.Status != certificates.Pending {
			t.Fatalf("A's pending certificate changed: %s", gp.Status)
		}
	})

	replace := func(t *testing.T, h Harness, s tenancy.Scope, old certificates.Certificate, r Refs, u string, at time.Time) (certificates.Certificate, error) {
		return h.Store.BeginIssue(ctx, s, certificates.BeginInput{EnrollmentID: old.EnrollmentID, TemplateID: r.TemplateID, PublicID: pub(), IssuedBy: u, Now: at,
			Replaces: old.ID, ReplaceReason: "name corrected"}, ev("certificate.replace_started"))
	}

	t.Run("replacement: the old certificate stays valid until the new PDF is stored, then it is revoked and linked in one step", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		r := h.NewFixture(s, FixtureOpts{})
		old := issue(t, h, s, r, u, t0)
		nu, err := replace(t, h, s, old, r, u, t0.Add(time.Minute))
		must(t, err)
		if nu.Status != certificates.Pending || nu.ReplacesID != old.ID || nu.ReplaceReason != "name corrected" || nu.Number == old.Number || nu.PublicID == old.PublicID {
			t.Fatalf("pending replacement %+v", nu)
		}
		g, _ := h.Store.Get(ctx, s, old.ID)
		if g.Status != certificates.Issued || g.Replaced() {
			t.Fatalf("old certificate must stay valid while the replacement is made: %+v", g)
		}
		done, err := h.Store.Complete(ctx, s, nu.ID, []byte("%PDF-1.4 new"), ev("certificate.issued"))
		must(t, err)
		if done.Status != certificates.Issued || done.ReplacesID != old.ID {
			t.Fatalf("completed replacement %+v", done)
		}
		g, _ = h.Store.Get(ctx, s, old.ID)
		if g.Status != certificates.Revoked || !g.Replaced() || g.ReplacedByID != nu.ID || g.ReplacedByPublicID != nu.PublicID || g.RevokedAt == nil || g.RevokeReason != "name corrected" {
			t.Fatalf("old after replacement %+v", g)
		}
		if p, err := h.Store.GetPDF(ctx, s, old.ID); err != nil || len(p.Bytes) == 0 {
			t.Fatalf("the old PDF must stay stored: %v", err)
		}
		acts := h.AuditActions(old.ID)
		if len(acts) == 0 || acts[len(acts)-1] != "certificate.replaced" {
			t.Fatalf("old certificate audit: %v", acts)
		}
		if m := h.AuditMeta(old.ID); m["replaced_by"] != nu.ID || m["number"] != old.Number {
			t.Fatalf("replaced audit metadata %v", m)
		}
		// Exactly one valid certificate for the enrollment, and the chain can continue.
		l, _ := h.Store.List(ctx, s, certificates.Filter{Status: certificates.Issued, Limit: 50})
		if len(l) != 1 || l[0].ID != nu.ID {
			t.Fatalf("valid certificates: %+v", l)
		}
		again, err := replace(t, h, s, done, r, u, t0.Add(2*time.Minute))
		must(t, err)
		if _, err := h.Store.Complete(ctx, s, again.ID, []byte("%PDF-1.4 third"), ev("certificate.issued")); err != nil {
			t.Fatal(err)
		}
		g, _ = h.Store.Get(ctx, s, nu.ID)
		if g.Status != certificates.Revoked || g.ReplacedByID != again.ID {
			t.Fatalf("second replacement %+v", g)
		}
	})

	t.Run("replacement refusals: only an issued certificate of the same enrollment, one at a time, never two valid ones", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		r := h.NewFixture(s, FixtureOpts{})
		pend, err := begin(t, h, s, r, u, t0)
		must(t, err)
		_, err = replace(t, h, s, pend, r, u, t0)
		is(t, err, certificates.ErrState, "replace a pending certificate")
		old, err := h.Store.Complete(ctx, s, pend.ID, []byte("%PDF-1.4 a"), ev("certificate.issued"))
		must(t, err)
		other := h.NewFixture(s, FixtureOpts{})
		_, err = h.Store.BeginIssue(ctx, s, certificates.BeginInput{EnrollmentID: other.EnrollmentID, TemplateID: r.TemplateID, PublicID: pub(), IssuedBy: u, Now: t0,
			Replaces: old.ID, ReplaceReason: "x"}, ev("certificate.replace_started"))
		is(t, err, certificates.ErrState, "replace with another enrollment")
		_, err = h.Store.BeginIssue(ctx, s, certificates.BeginInput{EnrollmentID: old.EnrollmentID, TemplateID: r.TemplateID, PublicID: pub(), IssuedBy: u, Now: t0,
			Replaces: "00000000-0000-0000-0000-000000000000", ReplaceReason: "x"}, ev("certificate.replace_started"))
		is(t, err, certificates.ErrNotFound, "replace an unknown certificate")
		first, err := replace(t, h, s, old, r, u, t0.Add(time.Minute))
		must(t, err)
		_, err = replace(t, h, s, old, r, u, t0.Add(2*time.Minute))
		is(t, err, certificates.ErrReplacing, "second replacement in progress")
		_, err = begin(t, h, s, r, u, t0.Add(2*time.Minute))
		is(t, err, certificates.ErrAlreadyIssued, "plain issue while a replacement is pending")
		// A failed replacement leaves the old certificate valid and replaceable again.
		_, err = h.Store.Fail(ctx, s, first.ID, "pdf rendering failed", ev("certificate.issue_failed"))
		must(t, err)
		if g, _ := h.Store.Get(ctx, s, old.ID); g.Status != certificates.Issued || g.Replaced() {
			t.Fatalf("old after a failed replacement: %+v", g)
		}
		second, err := replace(t, h, s, old, r, u, t0.Add(3*time.Minute))
		must(t, err)
		// The old certificate is revoked meanwhile: the replacement cannot complete and the old one is untouched.
		_, err = h.Store.Revoke(ctx, s, old.ID, "withdrawn", u, ev("certificate.revoked"))
		must(t, err)
		_, err = h.Store.Complete(ctx, s, second.ID, []byte("%PDF-1.4 b"), ev("certificate.issued"))
		is(t, err, certificates.ErrState, "complete after the old one was revoked")
		if g, _ := h.Store.Get(ctx, s, second.ID); g.Status != certificates.Pending {
			t.Fatalf("a refused completion must not change the replacement: %s", g.Status)
		}
		_, err = replace(t, h, s, old, r, u, t0.Add(4*time.Minute))
		is(t, err, certificates.ErrState, "replace a revoked certificate")
	})

	t.Run("a stale pending replacement is abandoned by the next attempt", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		r := h.NewFixture(s, FixtureOpts{})
		old := issue(t, h, s, r, u, t0)
		stuck, err := replace(t, h, s, old, r, u, t0.Add(time.Minute))
		must(t, err)
		next, err := replace(t, h, s, old, r, u, t0.Add(time.Minute+certificates.StalePendingAfter+time.Second))
		must(t, err)
		if g, _ := h.Store.Get(ctx, s, stuck.ID); g.Status != certificates.Failed {
			t.Fatalf("stale pending replacement: %s", g.Status)
		}
		if next.ID == stuck.ID {
			t.Fatal("expected a new attempt")
		}
	})

	t.Run("another institute cannot replace or see the link", func(t *testing.T) {
		h := newHarness(t)
		a, b := h.NewScope(), h.NewScope()
		ua, ub := h.NewUser(), h.NewUser()
		r := h.NewFixture(a, FixtureOpts{})
		old := issue(t, h, a, r, ua, t0)
		rb := h.NewFixture(b, FixtureOpts{})
		_, err := h.Store.BeginIssue(ctx, b, certificates.BeginInput{EnrollmentID: rb.EnrollmentID, TemplateID: rb.TemplateID, PublicID: pub(), IssuedBy: ub, Now: t0,
			Replaces: old.ID, ReplaceReason: "x"}, ev("certificate.replace_started"))
		is(t, err, certificates.ErrNotFound, "foreign replace")
		if g, _ := h.Store.Get(ctx, a, old.ID); g.Status != certificates.Issued {
			t.Fatal("A's certificate changed")
		}
	})
}
