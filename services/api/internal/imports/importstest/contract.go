package importstest

import (
	"context"
	"testing"

	"github.com/certificate-os/api/internal/audit"
	"github.com/certificate-os/api/internal/imports"
	"github.com/certificate-os/api/internal/tenancy"
)

// Harness wires one store implementation into the shared contract.
type Harness struct {
	Store imports.Store
	// NewScope returns the Scope of a brand-new institute and the id of a user who may own imports.
	NewScope func() (tenancy.Scope, string)
	// AuditTotal counts audit rows written for an institute.
	AuditTotal func(instituteID string) int
}

func ev(action string) audit.Event {
	return audit.Event{ActorType: audit.ActorSystem, Action: action, EntityType: "student_import"}
}

func sample(user string) imports.Import {
	return imports.Import{CreatedBy: user, Status: imports.Uploaded, Filename: "students.csv",
		Headers: []string{"Name", "Email"}, Rows: [][]string{{"Asha Rao", "asha@example.com"}, {"Ravi", ""}},
		Mapping: imports.Mapping{"full_name": 0, "email": 1}, DateFormat: "iso", Results: []imports.RowResult{}}
}

// RunStoreContract is the executable specification every imports.Store must satisfy.
func RunStoreContract(t *testing.T, newHarness func(t *testing.T) Harness) {
	ctx := context.Background()
	must := func(t *testing.T, err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	t.Run("create then get round-trips every field", func(t *testing.T) {
		h := newHarness(t)
		a, user := h.NewScope()
		got, err := h.Store.Create(ctx, a, sample(user), ev("student_import.uploaded"))
		must(t, err)
		if got.ID == "" || got.CreatedAt.IsZero() || got.Status != imports.Uploaded {
			t.Fatalf("unexpected %+v", got)
		}
		read, err := h.Store.Get(ctx, a, got.ID)
		must(t, err)
		if read.Filename != "students.csv" || len(read.Headers) != 2 || len(read.Rows) != 2 || read.Rows[0][0] != "Asha Rao" ||
			read.Mapping["email"] != 1 || read.DateFormat != "iso" || read.CreatedBy != user {
			t.Fatalf("round trip lost data: %+v", read)
		}
		if h.AuditTotal(a.InstituteID()) != 1 {
			t.Fatalf("create must write exactly one audit event, got %d", h.AuditTotal(a.InstituteID()))
		}
	})

	t.Run("save is guarded by the expected status and keeps immutable fields", func(t *testing.T) {
		h := newHarness(t)
		a, user := h.NewScope()
		imp, err := h.Store.Create(ctx, a, sample(user), ev("student_import.uploaded"))
		must(t, err)
		imp.Status = imports.Validated
		imp.Results = []imports.RowResult{{Row: 2, State: imports.RowValid}, {Row: 3, State: imports.RowError, Message: "full_name: too short"}}
		imp.Counts = imports.Tally(imp.Results)
		imp.Filename, imp.CreatedBy = "tampered.csv", "someone-else"
		saved, err := h.Store.Save(ctx, a, imp, imports.Uploaded, &[]audit.Event{ev("student_import.validated")}[0])
		must(t, err)
		if saved.Status != imports.Validated || saved.Counts.Valid != 1 || saved.Counts.Errors != 1 || len(saved.Results) != 2 {
			t.Fatalf("unexpected %+v", saved)
		}
		if saved.Filename != "students.csv" || saved.CreatedBy != user {
			t.Fatalf("filename and creator are immutable: %+v", saved)
		}
		before := h.AuditTotal(a.InstituteID())
		imp.Status = imports.Confirming
		if _, err := h.Store.Save(ctx, a, imp, imports.Uploaded, &[]audit.Event{ev("student_import.validated")}[0]); err != imports.ErrState {
			t.Fatalf("stale `from` must give ErrState, got %v", err)
		}
		if h.AuditTotal(a.InstituteID()) != before {
			t.Fatal("a refused save must not write an audit event")
		}
		got, _ := h.Store.Get(ctx, a, imp.ID)
		if got.Status != imports.Validated {
			t.Fatalf("refused save changed the job: %+v", got)
		}
	})

	t.Run("a job claimed for confirming cannot be claimed twice", func(t *testing.T) {
		h := newHarness(t)
		a, user := h.NewScope()
		imp, err := h.Store.Create(ctx, a, sample(user), ev("student_import.uploaded"))
		must(t, err)
		imp.Status = imports.Validated
		imp, err = h.Store.Save(ctx, a, imp, imports.Uploaded, nil)
		must(t, err)
		imp.Status = imports.Confirming
		_, err = h.Store.Save(ctx, a, imp, imports.Validated, nil)
		must(t, err)
		if _, err := h.Store.Save(ctx, a, imp, imports.Validated, nil); err != imports.ErrState {
			t.Fatalf("second claim must fail with ErrState, got %v", err)
		}
	})

	t.Run("isolation: another institute can neither read nor save a job", func(t *testing.T) {
		h := newHarness(t)
		a, user := h.NewScope()
		b, _ := h.NewScope()
		imp, err := h.Store.Create(ctx, a, sample(user), ev("student_import.uploaded"))
		must(t, err)
		if _, err := h.Store.Get(ctx, b, imp.ID); err != imports.ErrNotFound {
			t.Fatalf("foreign Get must be ErrNotFound, got %v", err)
		}
		imp.Status = imports.Cancelled
		if _, err := h.Store.Save(ctx, b, imp, imports.Uploaded, nil); err != imports.ErrNotFound {
			t.Fatalf("foreign Save must be ErrNotFound, got %v", err)
		}
		if _, err := h.Store.Get(ctx, b, "00000000-0000-0000-0000-000000000000"); err != imports.ErrNotFound {
			t.Fatalf("unknown id must be ErrNotFound, got %v", err)
		}
		still, err := h.Store.Get(ctx, a, imp.ID)
		must(t, err)
		if still.Status != imports.Uploaded {
			t.Fatalf("foreign Save modified the job: %+v", still)
		}
	})
}
