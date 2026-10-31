package dashboard

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/auth/authhttp"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/tenancy"
)

// Handler serves GET /api/v1/dashboard (institute members) and GET /api/v1/ops/dashboard (platform operations).
type Handler struct {
	Store   Store
	Log     *slog.Logger
	Tenancy *tenancy.Handler
	Authn   func(http.Handler) http.Handler
	Now     func() time.Time
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/dashboard", h.Tenancy.Guard(auth.PermInstituteRead, http.HandlerFunc(h.institute)))
	mux.Handle("GET /api/v1/ops/dashboard", h.Authn(authhttp.RequirePlatformPermission(auth.PermOpsApplicationsRead, http.HandlerFunc(h.platform))))
}

func (h *Handler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

func (h *Handler) days(w http.ResponseWriter, r *http.Request) (int, bool) {
	v := r.URL.Query().Get("days")
	if v == "" {
		return DefaultDays, true
	}
	n, err := strconv.Atoi(v)
	if err != nil || !ValidDays(n) {
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the highlighted fields.", map[string]string{"days": "Use 7, 30, 90 or 365."})
		return 0, false
	}
	return n, true
}

func (h *Handler) internal(w http.ResponseWriter, r *http.Request, err error) {
	h.Log.Error("dashboard request failed", "request_id", httpx.RequestID(r.Context()), "err", err)
	httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "An unexpected error occurred.", nil)
}

func (h *Handler) institute(w http.ResponseWriter, r *http.Request) {
	t, _ := tenancy.TenantFrom(r.Context())
	days, ok := h.days(w, r)
	if !ok {
		return
	}
	since := h.now().AddDate(0, 0, -days)
	st, err := h.Store.Institute(r.Context(), t.Scope(), since)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	out := Build(t, days, since, st)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"dashboard": out})
}

func (h *Handler) platform(w http.ResponseWriter, r *http.Request) {
	days, ok := h.days(w, r)
	if !ok {
		return
	}
	since := h.now().AddDate(0, 0, -days)
	st, err := h.Store.Platform(r.Context(), since)
	if err != nil {
		h.internal(w, r, err)
		return
	}
	dec := make([]map[string]any, 0, len(st.RecentDecisions))
	for _, d := range st.RecentDecisions {
		dec = append(dec, map[string]any{"action": d.Action, "at": d.At})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"dashboard": map[string]any{
		"days": days, "since": since.UTC(), "applications": st.Applications, "institutes": st.Institutes,
		"volume": map[string]int{"certificates_issued": st.CertificatesIssued, "certificates_revoked": st.CertificatesRevoked,
			"bulk_jobs_started": st.BulkJobsStarted, "emails_accepted": st.EmailsAccepted, "emails_failed": st.EmailsFailed},
		"recent_decisions": dec,
	}})
}

// Build assembles the institute dashboard payload. Sections follow the member's permissions: counts of students
// need students.read; anything about certificates, jobs, emails and the recent list needs certificates.read.
func Build(t tenancy.Tenant, days int, since time.Time, st InstituteStats) map[string]any {
	out := map[string]any{"days": days, "since": since.UTC()}
	if t.Can(auth.PermStudentsRead) {
		out["records"] = map[string]int{"active_students": st.Records.ActiveStudents, "archived_students": st.Records.ArchivedStudents, "active_batches": st.Records.ActiveBatches}
	}
	if t.Can(auth.PermCertsRead) {
		out["certificates"] = map[string]int{"issued_in_period": st.Certificates.IssuedInPeriod, "issued": st.Certificates.Issued,
			"revoked": st.Certificates.Revoked, "failed": st.Certificates.Failed}
		if st.LastJob != nil {
			j := st.LastJob
			out["last_job"] = map[string]any{"id": j.ID, "status": j.Status, "batch_name": j.BatchName, "total": j.Total,
				"succeeded": j.Succeeded, "failed": j.Failed, "created_at": j.CreatedAt}
		} else {
			out["last_job"] = nil
		}
		out["deliveries"] = map[string]int{"queued": st.Deliveries.Queued, "sending": st.Deliveries.Sending, "accepted": st.Deliveries.Accepted, "failed": st.Deliveries.Failed}
		recent := make([]map[string]any, 0, len(st.Recent))
		for _, c := range st.Recent {
			recent = append(recent, map[string]any{"id": c.ID, "certificate_number": c.Number, "student_name": c.StudentName,
				"course_name": c.CourseName, "status": c.Status, "at": c.At})
		}
		out["recent"] = recent
	}
	return out
}
