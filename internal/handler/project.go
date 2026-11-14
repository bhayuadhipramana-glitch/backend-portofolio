// Package handler contains HTTP handlers that sit at the outermost layer
// of the application. Handlers decode requests, delegate to the usecase
// layer, and encode standardised JSON responses.
package handler

import (
	"errors"
	"log/slog"
	"net/http"

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
func (h *ProjectHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateProjectRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	// Validate the DTO; nil means all rules pass.
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

// List handles GET /api/v1/projects?limit=&offset=.
func (h *ProjectHandler) List(w http.ResponseWriter, r *http.Request) {
	page, ok := parsePage(w, r)
	if !ok {
		return
	}

	projects, err := h.uc.List(r.Context(), page)
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
func (h *ProjectHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return // parseUUIDParam already wrote the error response
	}

	project, err := h.uc.GetByID(r.Context(), id)
	if err != nil {
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
func (h *ProjectHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	var req dto.UpdateProjectRequest
	if !decodeJSON(w, r, &req) {
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
func (h *ProjectHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
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

// isNotFound reports whether err wraps domain.ErrNotFound.
func isNotFound(err error) bool {
	return errors.Is(err, domain.ErrNotFound)
}
