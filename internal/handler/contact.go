package handler

import (
	"log/slog"
	"net/http"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/dto"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

// maxContactBodyBytes caps a contact form body at 32 KB.
const maxContactBodyBytes = 32 << 10

// ContactHandler exposes the public contact form (F9) and the admin inbox.
type ContactHandler struct {
	uc usecase.ContactUsecase
}

// NewContactHandler constructs a ContactHandler.
func NewContactHandler(uc usecase.ContactUsecase) *ContactHandler {
	return &ContactHandler{uc: uc}
}

// Submit handles POST /api/v1/contact. A filled honeypot field answers 202
// without touching the database, so bots cannot tell they were dropped. The
// per-IP limit is applied by middleware before this handler runs.
func (h *ContactHandler) Submit(w http.ResponseWriter, r *http.Request) {
	var req dto.ContactRequest
	if !decodeJSONLimit(w, r, &req, maxContactBodyBytes) {
		return
	}

	if req.IsSpam() {
		slog.InfoContext(r.Context(), "handler: contact honeypot triggered; submission dropped")
		response.Success(w, http.StatusAccepted, "message received", nil)
		return
	}

	req.Normalize()
	if errs := dto.Validate(&req); errs != nil {
		response.Error(w, http.StatusBadRequest, "validation failed", errs)
		return
	}

	c := domain.Contact{Name: req.Name, Email: req.Email, Subject: req.Subject, Message: req.Message}
	// r.RemoteAddr is the client IP: middleware.ClientIP set it from
	// CF-Connecting-IP when (and only when) the Cloudflare tunnel is trusted.
	if err := h.uc.Submit(r.Context(), &c, r.RemoteAddr); err != nil {
		slog.ErrorContext(r.Context(), "handler: submit contact failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to send message", nil)
		return
	}

	response.Success(w, http.StatusAccepted, "message received", nil)
}

// AdminList handles GET /api/v1/admin/contacts?limit=&offset=.
func (h *ContactHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	page, ok := parsePage(w, r)
	if !ok {
		return
	}

	contacts, err := h.uc.List(r.Context(), page)
	if err != nil {
		slog.ErrorContext(r.Context(), "handler: list contacts failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to fetch contacts", nil)
		return
	}
	if contacts == nil {
		contacts = []domain.Contact{}
	}

	response.Success(w, http.StatusOK, "contacts fetched successfully", contacts)
}
