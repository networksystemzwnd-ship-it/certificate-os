package templates

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/tenancy"
)

// Handler serves certificate templates. Every route goes through tenancy.Handler.Guard (authenticate,
// resolve the tenant from membership, check the permission; suspended institutes lose write permissions).
// The institute id is never read from the path, query or body. Previews only ever use sample data.
type Handler struct {
	Svc     *Service
	Log     *slog.Logger
	Tenancy *tenancy.Handler
}

func (h *Handler) Register(mux *http.ServeMux) {
	g := h.Tenancy.Guard
	r, w := auth.PermTemplatesRead, auth.PermTemplatesWrite
	mux.Handle("GET /api/v1/certificate-presets", g(r, http.HandlerFunc(h.presets)))
	mux.Handle("GET /api/v1/certificate-templates", g(r, http.HandlerFunc(h.list)))
	mux.Handle("POST /api/v1/certificate-templates", g(w, http.HandlerFunc(h.create)))
	mux.Handle("POST /api/v1/certificate-templates/preview", g(r, http.HandlerFunc(h.previewDraft)))
	mux.Handle("GET /api/v1/certificate-templates/{id}", g(r, http.HandlerFunc(h.get)))
	mux.Handle("PATCH /api/v1/certificate-templates/{id}", g(w, http.HandlerFunc(h.rename)))
	mux.Handle("DELETE /api/v1/certificate-templates/{id}", g(w, h.archive(true)))
	mux.Handle("POST /api/v1/certificate-templates/{id}/restore", g(w, h.archive(false)))
	mux.Handle("POST /api/v1/certificate-templates/{id}/versions", g(w, http.HandlerFunc(h.addVersion)))
	mux.Handle("GET /api/v1/certificate-templates/{id}/versions/{n}", g(r, http.HandlerFunc(h.getVersion)))
	mux.Handle("POST /api/v1/certificate-templates/{id}/preview", g(r, http.HandlerFunc(h.preview)))
}

type versionDTO struct {
	Number    int       `json:"number"`
	Config    *Config   `json:"config,omitempty"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

type templateDTO struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	Current    versionDTO   `json:"current_version"`
	Versions   []versionDTO `json:"versions,omitempty"`
	ArchivedAt *time.Time   `json:"archived_at"`
	CreatedAt  time.Time    `json:"created_at"`
	UpdatedAt  time.Time    `json:"updated_at"`
}

func toVersion(v Version) versionDTO {
	c := v.Config
	return versionDTO{Number: v.Number, Config: &c, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt}
}

func toTemplate(t Template) templateDTO {
	d := templateDTO{ID: t.ID, Name: t.Name, Current: toVersion(t.Current), ArchivedAt: t.ArchivedAt,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt}
	for _, v := range t.Versions {
		d.Versions = append(d.Versions, versionDTO{Number: v.Number, CreatedBy: v.CreatedBy, CreatedAt: v.CreatedAt})
	}
	return d
}

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var ve *ValidationError
	var ce *ConflictError
	switch {
	case errors.As(err, &ve):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the highlighted fields.", ve.Problems)
	case errors.As(err, &ce):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That name is already in use.",
			map[string]string{ce.Field: "That name is already in use."})
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w, r)
	case errors.Is(err, ErrArchived):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "That template is archived. Restore it first.", nil)
	case errors.Is(err, ErrTooManyVersions):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "This template has reached the version limit. Create a new template instead.", nil)
	default:
		h.Log.Error("templates request failed", "request_id", httpx.RequestID(r.Context()), "err", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "An unexpected error occurred.", nil)
	}
}

func (h *Handler) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := httpx.DecodeJSON(w, r, dst); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, err.Error(), nil)
		return false
	}
	return true
}

func tenant(r *http.Request) tenancy.Tenant { t, _ := tenancy.TenantFrom(r.Context()); return t }
func rid(r *http.Request) string            { return httpx.RequestID(r.Context()) }

func bad(w http.ResponseWriter, r *http.Request, field, msg string) {
	httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the highlighted fields.", map[string]string{field: msg})
}

func (h *Handler) presets(w http.ResponseWriter, r *http.Request) {
	type item struct {
		Preset string `json:"preset"`
		Label  string `json:"label"`
		Config Config `json:"config"`
	}
	out := []item{}
	for _, p := range Presets() {
		out = append(out, item{p.Preset, p.Label, p.Config})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"presets": out, "placeholders": Placeholders})
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	p := Page{Q: q.Get("q"), Archived: Archive(q.Get("archived"))}
	if !p.Archived.Valid() {
		bad(w, r, "archived", "Use only or all.")
		return
	}
	var err error
	if v := q.Get("limit"); v != "" {
		if p.Limit, err = strconv.Atoi(v); err != nil || p.Limit < 1 {
			bad(w, r, "limit", "Use a positive number.")
			return
		}
	}
	if v := q.Get("offset"); v != "" {
		if p.Offset, err = strconv.Atoi(v); err != nil || p.Offset < 0 {
			bad(w, r, "offset", "Use zero or a positive number.")
			return
		}
	}
	p = p.Normalized()
	items, err := h.Svc.List(r.Context(), tenant(r), p)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	more := len(items) > p.Limit
	if more {
		items = items[:p.Limit]
	}
	out := make([]templateDTO, 0, len(items))
	for _, t := range items {
		out = append(out, toTemplate(t))
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"templates": out, "has_more": more, "limit": p.Limit, "offset": p.Offset})
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name   string `json:"name"`
		Config Config `json:"config"`
	}
	if !h.decode(w, r, &b) {
		return
	}
	t, err := h.Svc.Create(r.Context(), tenant(r), b.Name, b.Config, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"template": toTemplate(t)})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	t, err := h.Svc.Get(r.Context(), tenant(r), r.PathValue("id"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"template": toTemplate(t)})
}

func (h *Handler) rename(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name string `json:"name"`
	}
	if !h.decode(w, r, &b) {
		return
	}
	t, err := h.Svc.Rename(r.Context(), tenant(r), r.PathValue("id"), b.Name, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"template": toTemplate(t)})
}

func (h *Handler) archive(archived bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t, err := h.Svc.Archive(r.Context(), tenant(r), r.PathValue("id"), archived, rid(r))
		if err != nil {
			h.fail(w, r, err)
			return
		}
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"template": toTemplate(t)})
	})
}

func (h *Handler) addVersion(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Config Config `json:"config"`
	}
	if !h.decode(w, r, &b) {
		return
	}
	v, err := h.Svc.AddVersion(r.Context(), tenant(r), r.PathValue("id"), b.Config, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"version": toVersion(v)})
}

func versionNumber(w http.ResponseWriter, r *http.Request, s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > MaxVersions {
		httpx.NotFound(w, r)
		return 0, false
	}
	return n, true
}

func (h *Handler) getVersion(w http.ResponseWriter, r *http.Request) {
	n, ok := versionNumber(w, r, r.PathValue("n"))
	if !ok {
		return
	}
	v, err := h.Svc.GetVersion(r.Context(), tenant(r), r.PathValue("id"), n)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"version": toVersion(v)})
}

// preview renders a stored version (default: the current one) or, when "config" is sent, an unsaved draft.
func (h *Handler) preview(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Version *int    `json:"version"`
		Config  *Config `json:"config"`
	}
	if !h.decode(w, r, &b) {
		return
	}
	id := r.PathValue("id")
	if b.Config != nil {
		// A draft is checked against the template so another institute's id never previews anything.
		if _, err := h.Svc.Get(r.Context(), tenant(r), id); err != nil {
			h.fail(w, r, err)
			return
		}
		h.writePreview(w, r, *b.Config)
		return
	}
	n := 0
	if b.Version != nil {
		var ok bool
		if n, ok = versionNumber(w, r, strconv.Itoa(*b.Version)); !ok {
			return
		}
	}
	out, err := h.Svc.PreviewVersion(r.Context(), tenant(r), id, n)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"html": out})
}

func (h *Handler) previewDraft(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Config Config `json:"config"`
	}
	if !h.decode(w, r, &b) {
		return
	}
	h.writePreview(w, r, b.Config)
}

func (h *Handler) writePreview(w http.ResponseWriter, r *http.Request, c Config) {
	out, err := h.Svc.PreviewDraft(c)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"html": out})
}
