package handler

import (
	"log/slog"
	"net/http"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/dto"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

// ResumeHandler exposes CRUD HTTP endpoints for the Resume entity.
type ResumeHandler struct {
	uc usecase.ResumeUsecase
}

// NewResumeHandler constructs a ResumeHandler with its required
// ResumeUsecase dependency.
func NewResumeHandler(uc usecase.ResumeUsecase) *ResumeHandler {
	return &ResumeHandler{uc: uc}
}

// Create handles POST /api/v1/resumes (admin). is_active=true atomically
// replaces the previously active resume.
func (h *ResumeHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateResumeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if errs := dto.Validate(&req); errs != nil {
		response.Error(w, http.StatusBadRequest, "validation failed", errs)
		return
	}

	res := domain.Resume{
		Name:     req.Name,
		FileURL:  req.FileURL,
		Content:  req.Content,
		IsActive: req.IsActive,
	}

	if err := h.uc.Create(r.Context(), &res); err != nil {
		slog.ErrorContext(r.Context(), "handler: create resume failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to create resume", nil)
		return
	}

	response.Success(w, http.StatusCreated, "resume created successfully", res)
}

// List handles GET /api/v1/resumes and returns the active resume only.
func (h *ResumeHandler) List(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, domain.VisibilityPublic)
}

// AdminList handles GET /api/v1/admin/resumes and returns every resume.
func (h *ResumeHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, domain.VisibilityAll)
}

func (h *ResumeHandler) list(w http.ResponseWriter, r *http.Request, vis domain.Visibility) {
	page, ok := parsePage(w, r)
	if !ok {
		return
	}

	resumes, err := h.uc.List(r.Context(), vis, page)
	if err != nil {
		slog.ErrorContext(r.Context(), "handler: list resumes failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to fetch resumes", nil)
		return
	}

	// Guarantee a JSON array (never null) when the slice is empty.
	if resumes == nil {
		resumes = []domain.Resume{}
	}

	response.Success(w, http.StatusOK, "resumes fetched successfully", resumes)
}

// GetByID handles GET /api/v1/resumes/{id}; inactive resumes are not found.
func (h *ResumeHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	h.get(w, r, domain.VisibilityPublic)
}

// AdminGetByID handles GET /api/v1/admin/resumes/{id}.
func (h *ResumeHandler) AdminGetByID(w http.ResponseWriter, r *http.Request) {
	h.get(w, r, domain.VisibilityAll)
}

func (h *ResumeHandler) get(w http.ResponseWriter, r *http.Request, vis domain.Visibility) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	resume, err := h.uc.GetByID(r.Context(), vis, id)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, "resume not found", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: get resume failed", "error", err, "id", id)
		response.Error(w, http.StatusInternalServerError, "failed to fetch resume", nil)
		return
	}

	response.Success(w, http.StatusOK, "resume fetched successfully", resume)
}

// Update handles PUT /api/v1/resumes/{id} (admin). is_active=true atomically
// replaces the previously active resume.
func (h *ResumeHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	var req dto.UpdateResumeRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if errs := dto.Validate(&req); errs != nil {
		response.Error(w, http.StatusBadRequest, "validation failed", errs)
		return
	}

	res := domain.Resume{
		ID:       id,
		Name:     req.Name,
		FileURL:  req.FileURL,
		Content:  req.Content,
		IsActive: req.IsActive,
	}

	if err := h.uc.Update(r.Context(), &res); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, "resume not found", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: update resume failed", "error", err, "id", id)
		response.Error(w, http.StatusInternalServerError, "failed to update resume", nil)
		return
	}

	response.Success(w, http.StatusOK, "resume updated successfully", res)
}

// Delete handles DELETE /api/v1/resumes/{id} (admin).
func (h *ResumeHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	if err := h.uc.Delete(r.Context(), id); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, "resume not found", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: delete resume failed", "error", err, "id", id)
		response.Error(w, http.StatusInternalServerError, "failed to delete resume", nil)
		return
	}

	response.Success(w, http.StatusOK, "resume deleted successfully", nil)
}
