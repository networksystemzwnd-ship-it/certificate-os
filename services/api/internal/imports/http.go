package imports

import (
	"encoding/csv"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/certificate-os/api/internal/auth"
	"github.com/certificate-os/api/internal/platform/httpx"
	"github.com/certificate-os/api/internal/tenancy"
)

// Handler serves the student import wizard. Every route needs students.import (so a suspended
// institute, which loses write permissions, cannot import) and resolves the tenant from membership.
type Handler struct {
	Svc     *Service
	Log     *slog.Logger
	Tenancy *tenancy.Handler
}

func (h *Handler) Register(mux *http.ServeMux) {
	g := func(f http.HandlerFunc) http.Handler { return h.Tenancy.Guard(auth.PermStudentsImport, f) }
	mux.Handle("POST /api/v1/students/imports", g(h.upload))
	mux.Handle("GET /api/v1/students/imports/{iid}", g(h.get))
	mux.Handle("PUT /api/v1/students/imports/{iid}/mapping", g(h.setMapping))
	mux.Handle("GET /api/v1/students/imports/{iid}/errors", g(h.errorsCSV))
	mux.Handle("POST /api/v1/students/imports/{iid}/confirm", g(h.confirm))
	mux.Handle("DELETE /api/v1/students/imports/{iid}", g(h.cancel))
}

const (
	previewRows = 10
	maxIssues   = 100
)

type issueDTO struct {
	Row     int    `json:"row"`
	State   string `json:"state"`
	Message string `json:"message"`
}

type previewDTO struct {
	Row     int      `json:"row"`
	Cells   []string `json:"cells"`
	State   string   `json:"state,omitempty"`
	Message string   `json:"message,omitempty"`
}

type importDTO struct {
	ID               string         `json:"id"`
	Status           string         `json:"status"`
	Filename         string         `json:"filename"`
	Headers          []string       `json:"headers"`
	Mapping          map[string]int `json:"mapping"`
	SuggestedMapping map[string]int `json:"suggested_mapping,omitempty"`
	DateFormat       string         `json:"date_format"`
	Counts           Counts         `json:"counts"`
	Preview          []previewDTO   `json:"preview"`
	Issues           []issueDTO     `json:"issues"`
	IssuesTruncated  bool           `json:"issues_truncated"`
	CreatedAt        time.Time      `json:"created_at"`
	CompletedAt      *time.Time     `json:"completed_at"`
}

func toDTO(imp Import) importDTO {
	d := importDTO{ID: imp.ID, Status: string(imp.Status), Filename: imp.Filename, Headers: imp.Headers,
		Mapping: imp.Mapping, DateFormat: imp.DateFormat, Counts: imp.Counts, Preview: []previewDTO{}, Issues: []issueDTO{},
		CreatedAt: imp.CreatedAt, CompletedAt: imp.CompletedAt}
	if d.Mapping == nil {
		d.Mapping = map[string]int{}
	}
	if d.Headers == nil {
		d.Headers = []string{}
	}
	for i := 0; i < len(imp.Rows) && len(d.Preview) < previewRows; i++ {
		if len(imp.Rows[i]) == 0 { // purged
			continue
		}
		p := previewDTO{Row: i + 2, Cells: imp.Rows[i]}
		if i < len(imp.Results) {
			p.State, p.Message = string(imp.Results[i].State), imp.Results[i].Message
		}
		d.Preview = append(d.Preview, p)
	}
	for _, r := range imp.Results {
		if r.State == RowValid || r.State == RowImported {
			if r.Message == "" {
				continue
			}
		}
		if len(d.Issues) == maxIssues {
			d.IssuesTruncated = true
			break
		}
		d.Issues = append(d.Issues, issueDTO{r.Row, string(r.State), r.Message})
	}
	return d
}

func tenant(r *http.Request) tenancy.Tenant { t, _ := tenancy.TenantFrom(r.Context()); return t }
func rid(r *http.Request) string            { return httpx.RequestID(r.Context()) }

func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	var fe *FileError
	var me *MappingError
	switch {
	case errors.As(err, &fe):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, fe.Msg, map[string]string{"file": fe.Msg})
	case errors.As(err, &me):
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, httpx.CodeValidation, "Please correct the column mapping.", me.Problems)
	case errors.Is(err, ErrNotFound):
		httpx.NotFound(w, r)
	case errors.Is(err, ErrState):
		httpx.WriteError(w, r, http.StatusConflict, httpx.CodeConflict, "This import cannot be changed in its current state.", nil)
	default:
		h.Log.Error("import request failed", "request_id", rid(r), "err", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "An unexpected error occurred.", nil)
	}
}

func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBytes+(256<<10))
	if err := r.ParseMultipartForm(1 << 20); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			httpx.WriteError(w, r, http.StatusRequestEntityTooLarge, httpx.CodeValidation, "The file is too large.", map[string]string{"file": "The file is too large."})
			return
		}
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "Send the file as multipart/form-data in a field named file.", nil)
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()
	f, hdr, err := r.FormFile("file")
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "Choose a CSV file to upload.", map[string]string{"file": "Choose a CSV file to upload."})
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, "The file could not be read.", nil)
		return
	}
	name := hdr.Filename
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	imp, err := h.Svc.Upload(r.Context(), tenant(r), name, data, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	d := toDTO(imp)
	d.SuggestedMapping = imp.Mapping
	httpx.WriteJSON(w, http.StatusCreated, map[string]any{"import": d})
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	imp, err := h.Svc.Get(r.Context(), tenant(r), r.PathValue("iid"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"import": toDTO(imp)})
}

func (h *Handler) setMapping(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mapping    map[string]int `json:"mapping"`
		DateFormat string         `json:"date_format"`
	}
	if err := httpx.DecodeJSON(w, r, &body); err != nil {
		httpx.WriteError(w, r, http.StatusBadRequest, httpx.CodeBadRequest, err.Error(), nil)
		return
	}
	imp, err := h.Svc.SetMapping(r.Context(), tenant(r), r.PathValue("iid"), Mapping(body.Mapping), body.DateFormat, rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"import": toDTO(imp)})
}

func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) {
	imp, err := h.Svc.Confirm(r.Context(), tenant(r), r.PathValue("iid"), rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"import": toDTO(imp)})
}

func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	imp, err := h.Svc.Cancel(r.Context(), tenant(r), r.PathValue("iid"), rid(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"import": toDTO(imp)})
}

func (h *Handler) errorsCSV(w http.ResponseWriter, r *http.Request) {
	imp, err := h.Svc.Get(r.Context(), tenant(r), r.PathValue("iid"))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="import-errors.csv"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write([]byte("\xef\xbb\xbf")) // BOM so Excel reads UTF-8
	cw := csv.NewWriter(w)
	_ = cw.WriteAll(ErrorReport(imp))
}
