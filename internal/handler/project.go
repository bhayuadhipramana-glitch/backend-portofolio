// Package handler contains HTTP handlers that sit at the outermost layer
// of the Clean Architecture onion. Handlers decode requests, delegate to
// the usecase layer, and encode standardised JSON responses.
package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/dto"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

// ProjectHandler exposes CRUD HTTP endpoints for the Project entity.
type ProjectHandler struct {
	uc usecase.ProjectUsecase
}

// NewProjectHandler constructs a ProjectHandler with its required
// ProjectUsecase dependency.
func NewProjectHandler(uc usecase.ProjectUsecase) *ProjectHandler {
	return &ProjectHandler{uc: uc}
}

// Create handles POST /api/v1/projects.
//
// It decodes the request body into a CreateProjectRequest DTO, validates it,
// persists the entity through the usecase, and returns the created project.
func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.WarnContext(r.Context(), "handler: invalid JSON body", "error", err)
		response.Error(w, http.StatusBadRequest, "invalid request body", nil)
		return
	}

	// Validate the DTO — returns nil when all rules pass.
	if errs := dto.Validate(&req); errs != nil {
		response.Error(w, http.StatusBadRequest, "validation failed", errs)
		return
	}

	p := domain.Project{
		Title:       req.Title,
		Description: req.Description,
		TechStack:   req.TechStack,
	}

	if err := h.uc.Create(r.Context(), &p); err != nil {
		slog.ErrorContext(r.Context(), "handler: create project failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to create project", nil)
		return
	}

	response.Success(w, http.StatusCreated, "project created successfully", p)
}

// List handles GET /api/v1/projects.
//
// It retrieves every project from the usecase and returns them as a JSON
// array inside the standard success envelope.
func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	projects, err := h.uc.List(r.Context())
	if err != nil {
		slog.ErrorContext(r.Context(), "handler: list projects failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to fetch projects", nil)
		return
	}

	// Guarantee a JSON array (never null) when the slice is empty.
	if projects == nil {
		projects = []domain.Project{}
	}

	response.Success(w, http.StatusOK, "projects fetched successfully", projects)
}

// GetByID handles GET /api/v1/projects/{id}.
//
// It parses the path parameter, queries the usecase, and returns either
// the project or an appropriate error response.
func (h *ProjectHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return // parseIDParam already wrote the error response
	}

	project, err := h.uc.GetByID(r.Context(), id)
	if err != nil {
		// Differentiate between "not found" and unexpected DB errors.
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, "project not found", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: get project failed", "error", err, "id", id)
		response.Error(w, http.StatusInternalServerError, "failed to fetch project", nil)
		return
	}

	response.Success(w, http.StatusOK, "project fetched successfully", project)
}

// Update handles PUT /api/v1/projects/{id}.
//
// It parses the path parameter, decodes & validates the request body,
// then updates the entity through the usecase.
func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	var req dto.UpdateProjectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		slog.WarnContext(r.Context(), "handler: invalid JSON body", "error", err)
		response.Error(w, http.StatusBadRequest, "invalid request body", nil)
		return
	}

	if errs := dto.Validate(&req); errs != nil {
		response.Error(w, http.StatusBadRequest, "validation failed", errs)
		return
	}

	p := domain.Project{
		ID:          id,
		Title:       req.Title,
		Description: req.Description,
		TechStack:   req.TechStack,
	}

	if err := h.uc.Update(r.Context(), &p); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, "project not found", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: update project failed", "error", err, "id", id)
		response.Error(w, http.StatusInternalServerError, "failed to update project", nil)
		return
	}

	response.Success(w, http.StatusOK, "project updated successfully", p)
}

// Delete handles DELETE /api/v1/projects/{id}.
//
// It parses the path parameter, deletes the entity through the usecase,
// and returns a confirmation message.
func (h *ProjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseIDParam(w, r)
	if !ok {
		return
	}

	if err := h.uc.Delete(r.Context(), id); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, "project not found", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: delete project failed", "error", err, "id", id)
		response.Error(w, http.StatusInternalServerError, "failed to delete project", nil)
		return
	}

	response.Success(w, http.StatusOK, "project deleted successfully", nil)
}

// ---------- internal helpers ----------

// parseIDParam extracts and validates the {id} URL parameter.
// It writes a 400 error response and returns false when the value is invalid.
func parseIDParam(w http.ResponseWriter, r *http.Request) (int, bool) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.Atoi(idStr)
	if err != nil || id <= 0 {
		response.Error(w, http.StatusBadRequest, "invalid project id", nil)
		return 0, false
	}
	return id, true
}

// isNotFound returns true when the error message indicates the requested
// entity does not exist.  This is a pragmatic approach that avoids coupling
// the handler to concrete repository error types while the project is small.
// In a larger code-base you would define sentinel errors in the domain layer.
func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, domain.ErrNotFound)
}
