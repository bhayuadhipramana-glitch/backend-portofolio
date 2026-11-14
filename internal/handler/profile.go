package handler

import (
	"log/slog"
	"net/http"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/dto"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

// ProfileHandler exposes CRUD HTTP endpoints for the Profile entity.
type ProfileHandler struct {
	uc usecase.ProfileUsecase
}

// NewProfileHandler constructs a ProfileHandler with its required
// ProfileUsecase dependency.
func NewProfileHandler(uc usecase.ProfileUsecase) *ProfileHandler {
	return &ProfileHandler{uc: uc}
}

// Create handles POST /api/v1/profiles (admin). is_active=true atomically
// replaces the previously active profile.
func (h *ProfileHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if errs := dto.Validate(&req); errs != nil {
		response.Error(w, http.StatusBadRequest, "validation failed", errs)
		return
	}

	p := domain.Profile{
		Name:        req.Name,
		Headline:    req.Headline,
		Bio:         req.Bio,
		AvatarURL:   req.AvatarURL,
		SocialLinks: req.SocialLinks,
		IsActive:    req.IsActive,
	}

	if err := h.uc.Create(r.Context(), &p); err != nil {
		slog.ErrorContext(r.Context(), "handler: create profile failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to create profile", nil)
		return
	}

	response.Success(w, http.StatusCreated, "profile created successfully", p)
}

// List handles GET /api/v1/profiles and returns the active profile only.
func (h *ProfileHandler) List(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, domain.VisibilityPublic)
}

// AdminList handles GET /api/v1/admin/profiles and returns every profile.
func (h *ProfileHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, domain.VisibilityAll)
}

func (h *ProfileHandler) list(w http.ResponseWriter, r *http.Request, vis domain.Visibility) {
	page, ok := parsePage(w, r)
	if !ok {
		return
	}

	profiles, err := h.uc.List(r.Context(), vis, page)
	if err != nil {
		slog.ErrorContext(r.Context(), "handler: list profiles failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to fetch profiles", nil)
		return
	}

	// Guarantee a JSON array (never null) when the slice is empty.
	if profiles == nil {
		profiles = []domain.Profile{}
	}

	response.Success(w, http.StatusOK, "profiles fetched successfully", profiles)
}

// GetByID handles GET /api/v1/profiles/{id}; inactive profiles are not found.
func (h *ProfileHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	h.get(w, r, domain.VisibilityPublic)
}

// AdminGetByID handles GET /api/v1/admin/profiles/{id}.
func (h *ProfileHandler) AdminGetByID(w http.ResponseWriter, r *http.Request) {
	h.get(w, r, domain.VisibilityAll)
}

func (h *ProfileHandler) get(w http.ResponseWriter, r *http.Request, vis domain.Visibility) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	profile, err := h.uc.GetByID(r.Context(), vis, id)
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, "profile not found", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: get profile failed", "error", err, "id", id)
		response.Error(w, http.StatusInternalServerError, "failed to fetch profile", nil)
		return
	}

	response.Success(w, http.StatusOK, "profile fetched successfully", profile)
}

// Update handles PUT /api/v1/profiles/{id} (admin). is_active=true atomically
// replaces the previously active profile.
func (h *ProfileHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	var req dto.UpdateProfileRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if errs := dto.Validate(&req); errs != nil {
		response.Error(w, http.StatusBadRequest, "validation failed", errs)
		return
	}

	p := domain.Profile{
		ID:          id,
		Name:        req.Name,
		Headline:    req.Headline,
		Bio:         req.Bio,
		AvatarURL:   req.AvatarURL,
		SocialLinks: req.SocialLinks,
		IsActive:    req.IsActive,
	}

	if err := h.uc.Update(r.Context(), &p); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, "profile not found", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: update profile failed", "error", err, "id", id)
		response.Error(w, http.StatusInternalServerError, "failed to update profile", nil)
		return
	}

	response.Success(w, http.StatusOK, "profile updated successfully", p)
}

// Delete handles DELETE /api/v1/profiles/{id} (admin).
func (h *ProfileHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	if err := h.uc.Delete(r.Context(), id); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, "profile not found", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: delete profile failed", "error", err, "id", id)
		response.Error(w, http.StatusInternalServerError, "failed to delete profile", nil)
		return
	}

	response.Success(w, http.StatusOK, "profile deleted successfully", nil)
}
