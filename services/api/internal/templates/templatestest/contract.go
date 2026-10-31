package templatestest

import (
	"context"
	"errors"
	"testing"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/templates"
	"github.com/certificate-os/api/internal/tenancy"
)

// Harness wires one store implementation into the shared contract.
type Harness struct {
	Store templates.Store
	// NewScope returns the Scope of a brand-new institute (a real row for Postgres).
	NewScope func() tenancy.Scope
	// NewUser returns the id of a real user (a row for Postgres) to use as created_by.
	NewUser      func() string
	AuditActions func(entityType, entityID string) []string
	AuditTotal   func(instituteID string) int
	// AuditMeta returns the metadata of the newest audit row of the entity.
	AuditMeta func(entityType, entityID string) map[string]any
}

func ev(action string) audit.Event {
	return audit.Event{ActorType: audit.ActorSystem, Action: action, EntityType: "certificate_template"}
}

func is(t *testing.T, err, want error, what string) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: got %v, want %v", what, err, want)
	}
}

func cfg(title string) templates.Config {
	c := templates.Presets()[0].Config
	c.Title = title
	return c
}

// RunStoreContract is the executable specification every templates.Store must satisfy.
func RunStoreContract(t *testing.T, newHarness func(t *testing.T) Harness) {
	ctx := context.Background()
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	t.Run("create makes version 1; get returns config and versions; audit has the version", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		tp, err := h.Store.CreateTemplate(ctx, s, "Course completion", cfg("Certificate of Completion"), u, ev("template.created"))
		must(t, err)
		if tp.Current.Number != 1 || tp.Current.Config.Title != "Certificate of Completion" || tp.Current.CreatedBy != u {
			t.Fatalf("bad create result: %+v", tp)
		}
		got, err := h.Store.GetTemplate(ctx, s, tp.ID)
		must(t, err)
		if got.Name != "Course completion" || len(got.Versions) != 1 || got.Versions[0].Number != 1 {
			t.Fatalf("bad get: %+v", got)
		}
		if got.Current.Config != tp.Current.Config {
			t.Fatal("config did not round-trip")
		}
		if a := h.AuditActions("certificate_template", tp.ID); len(a) != 1 || a[0] != "template.created" {
			t.Fatalf("audit = %v", a)
		}
		if v := h.AuditMeta("certificate_template", tp.ID)["version"]; v != float64(1) && v != 1 {
			t.Fatalf("audit metadata version = %v", v)
		}
	})

	t.Run("versions are append-only and numbered; old versions stay readable and unchanged", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		tp, err := h.Store.CreateTemplate(ctx, s, "T", cfg("First title"), u, ev("template.created"))
		must(t, err)
		v2, err := h.Store.AddVersion(ctx, s, tp.ID, cfg("Second title"), u, ev("template.version_created"))
		must(t, err)
		v3, err := h.Store.AddVersion(ctx, s, tp.ID, cfg("Third title"), u, ev("template.version_created"))
		must(t, err)
		if v2.Number != 2 || v3.Number != 3 {
			t.Fatalf("numbers = %d, %d", v2.Number, v3.Number)
		}
		v1, err := h.Store.GetVersion(ctx, s, tp.ID, 1)
		must(t, err)
		if v1.Config.Title != "First title" {
			t.Fatalf("version 1 changed: %q", v1.Config.Title)
		}
		got, err := h.Store.GetTemplate(ctx, s, tp.ID)
		must(t, err)
		if got.Current.Number != 3 || got.Current.Config.Title != "Third title" || len(got.Versions) != 3 || got.Versions[0].Number != 3 {
			t.Fatalf("bad current/versions: %+v", got)
		}
		_, err = h.Store.GetVersion(ctx, s, tp.ID, 4)
		is(t, err, templates.ErrNotFound, "missing version")
		_, err = h.Store.GetVersion(ctx, s, tp.ID, 0)
		is(t, err, templates.ErrNotFound, "version 0")
	})

	t.Run("duplicate active names conflict (case-insensitive); archived names are free; restore re-checks", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		a, err := h.Store.CreateTemplate(ctx, s, "Diploma", cfg("A title"), u, ev("template.created"))
		must(t, err)
		_, err = h.Store.CreateTemplate(ctx, s, "DIPLOMA", cfg("A title"), u, ev("template.created"))
		is(t, err, templates.ErrConflict, "duplicate name")
		_, err = h.Store.SetArchived(ctx, s, a.ID, true, ev("template.archived"))
		must(t, err)
		b, err := h.Store.CreateTemplate(ctx, s, "Diploma", cfg("A title"), u, ev("template.created"))
		must(t, err)
		_, err = h.Store.SetArchived(ctx, s, a.ID, false, ev("template.restored"))
		is(t, err, templates.ErrConflict, "restore into a taken name")
		_, err = h.Store.RenameTemplate(ctx, s, b.ID, "Diploma 2", ev("template.renamed"))
		must(t, err)
		_, err = h.Store.SetArchived(ctx, s, a.ID, false, ev("template.restored"))
		must(t, err)
	})

	t.Run("archived templates accept no new versions but stay readable", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		tp, err := h.Store.CreateTemplate(ctx, s, "Old", cfg("A title"), u, ev("template.created"))
		must(t, err)
		_, err = h.Store.SetArchived(ctx, s, tp.ID, true, ev("template.archived"))
		must(t, err)
		_, err = h.Store.AddVersion(ctx, s, tp.ID, cfg("B title"), u, ev("template.version_created"))
		is(t, err, templates.ErrArchived, "version on archived")
		if _, err := h.Store.GetVersion(ctx, s, tp.ID, 1); err != nil {
			t.Fatal(err)
		}
		got, err := h.Store.GetTemplate(ctx, s, tp.ID)
		must(t, err)
		if got.ArchivedAt == nil {
			t.Fatal("not archived")
		}
	})

	t.Run("list: filters, search, paging probe row", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		var ids []string
		for _, n := range []string{"Beta", "Alpha", "Gamma"} {
			tp, err := h.Store.CreateTemplate(ctx, s, n, cfg("A title"), u, ev("template.created"))
			must(t, err)
			ids = append(ids, tp.ID)
		}
		_, err := h.Store.SetArchived(ctx, s, ids[2], true, ev("template.archived"))
		must(t, err)
		l, err := h.Store.ListTemplates(ctx, s, templates.Page{Limit: 50})
		must(t, err)
		if len(l) != 2 || l[0].Name != "Alpha" || l[1].Name != "Beta" || l[0].Current.Number != 1 {
			t.Fatalf("active list: %+v", l)
		}
		l, _ = h.Store.ListTemplates(ctx, s, templates.Page{Archived: templates.ArchivedOnly, Limit: 50})
		if len(l) != 1 || l[0].Name != "Gamma" {
			t.Fatalf("archived list: %+v", l)
		}
		l, _ = h.Store.ListTemplates(ctx, s, templates.Page{Archived: templates.IncludeAll, Limit: 50})
		if len(l) != 3 {
			t.Fatalf("all list: %d", len(l))
		}
		l, _ = h.Store.ListTemplates(ctx, s, templates.Page{Q: "ALP", Limit: 50})
		if len(l) != 1 || l[0].Name != "Alpha" {
			t.Fatalf("search: %+v", l)
		}
		l, _ = h.Store.ListTemplates(ctx, s, templates.Page{Archived: templates.IncludeAll, Limit: 2})
		if len(l) != 3 { // limit+1 probe row
			t.Fatalf("probe row: got %d", len(l))
		}
		l, _ = h.Store.ListTemplates(ctx, s, templates.Page{Archived: templates.IncludeAll, Limit: 2, Offset: 2})
		if len(l) != 1 {
			t.Fatalf("offset: got %d", len(l))
		}
	})

	t.Run("version limit", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		tp, err := h.Store.CreateTemplate(ctx, s, "Busy", cfg("A title"), u, ev("template.created"))
		must(t, err)
		for i := 2; i <= templates.MaxVersions; i++ {
			if _, err := h.Store.AddVersion(ctx, s, tp.ID, cfg("A title"), u, ev("template.version_created")); err != nil {
				t.Fatalf("version %d: %v", i, err)
			}
		}
		_, err = h.Store.AddVersion(ctx, s, tp.ID, cfg("A title"), u, ev("template.version_created"))
		is(t, err, templates.ErrTooManyVersions, "past the limit")
	})

	t.Run("a failed call writes no audit event", func(t *testing.T) {
		h := newHarness(t)
		s, u := h.NewScope(), h.NewUser()
		_, err := h.Store.CreateTemplate(ctx, s, "Once", cfg("A title"), u, ev("template.created"))
		must(t, err)
		before := h.AuditTotal(s.InstituteID())
		_, err = h.Store.CreateTemplate(ctx, s, "once", cfg("A title"), u, ev("template.created"))
		is(t, err, templates.ErrConflict, "duplicate")
		_, err = h.Store.AddVersion(ctx, s, "no-such-id", cfg("A title"), u, ev("template.version_created"))
		is(t, err, templates.ErrNotFound, "missing template")
		if after := h.AuditTotal(s.InstituteID()); after != before {
			t.Fatalf("audit rows %d -> %d after failures", before, after)
		}
	})

	t.Run("isolation: another institute sees nothing and changes nothing", func(t *testing.T) {
		h := newHarness(t)
		a, b := h.NewScope(), h.NewScope()
		ua, ub := h.NewUser(), h.NewUser()
		tp, err := h.Store.CreateTemplate(ctx, a, "Shared name", cfg("A title"), ua, ev("template.created"))
		must(t, err)
		_, err = h.Store.GetTemplate(ctx, b, tp.ID)
		is(t, err, templates.ErrNotFound, "get across institutes")
		_, err = h.Store.GetVersion(ctx, b, tp.ID, 1)
		is(t, err, templates.ErrNotFound, "version across institutes")
		_, err = h.Store.AddVersion(ctx, b, tp.ID, cfg("Hijack"), ub, ev("template.version_created"))
		is(t, err, templates.ErrNotFound, "add version across institutes")
		_, err = h.Store.RenameTemplate(ctx, b, tp.ID, "Hijacked", ev("template.renamed"))
		is(t, err, templates.ErrNotFound, "rename across institutes")
		_, err = h.Store.SetArchived(ctx, b, tp.ID, true, ev("template.archived"))
		is(t, err, templates.ErrNotFound, "archive across institutes")
		l, err := h.Store.ListTemplates(ctx, b, templates.Page{Archived: templates.IncludeAll, Limit: 50})
		must(t, err)
		if len(l) != 0 {
			t.Fatalf("institute B lists %d templates", len(l))
		}
		// The same name is free in another institute.
		if _, err := h.Store.CreateTemplate(ctx, b, "Shared name", cfg("A title"), ub, ev("template.created")); err != nil {
			t.Fatal(err)
		}
		got, err := h.Store.GetTemplate(ctx, a, tp.ID)
		must(t, err)
		if got.Name != "Shared name" || len(got.Versions) != 1 {
			t.Fatalf("institute A's template changed: %+v", got)
		}
	})
}
