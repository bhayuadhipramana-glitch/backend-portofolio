package handler

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/dto"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

// ArticleHandler exposes CRUD HTTP endpoints for the Article entity.
type ArticleHandler struct {
	uc usecase.ArticleUsecase
}

// NewArticleHandler constructs an ArticleHandler with its required
// ArticleUsecase dependency.
func NewArticleHandler(uc usecase.ArticleUsecase) *ArticleHandler {
	return &ArticleHandler{uc: uc}
}

// Create handles POST /api/v1/articles (admin).
func (h *ArticleHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.CreateArticleRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if errs := dto.Validate(&req); errs != nil {
		response.Error(w, http.StatusBadRequest, "validation failed", errs)
		return
	}

	a := domain.Article{
		Title:       req.Title,
		Slug:        req.Slug,
		Excerpt:     req.Excerpt,
		Content:     req.Content,
		Tags:        nonNilTags(req.Tags),
		Status:      req.Status,
		PublishedAt: req.PublishedAt,
	}

	if err := h.uc.Create(r.Context(), &a); err != nil {
		if errors.Is(err, domain.ErrConflict) {
			response.Error(w, http.StatusConflict, "article slug already exists", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: create article failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to create article", nil)
		return
	}

	response.Success(w, http.StatusCreated, "article created successfully", a)
}

// List handles GET /api/v1/articles?limit=&offset= and returns published articles only.
func (h *ArticleHandler) List(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, domain.VisibilityPublic)
}

// AdminList handles GET /api/v1/admin/articles?limit=&offset= and returns every status.
func (h *ArticleHandler) AdminList(w http.ResponseWriter, r *http.Request) {
	h.list(w, r, domain.VisibilityAll)
}

func (h *ArticleHandler) list(w http.ResponseWriter, r *http.Request, vis domain.Visibility) {
	page, ok := parsePage(w, r)
	if !ok {
		return
	}

	articles, err := h.uc.List(r.Context(), vis, page)
	if err != nil {
		slog.ErrorContext(r.Context(), "handler: list articles failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to fetch articles", nil)
		return
	}

	// Guarantee a JSON array (never null) when the slice is empty.
	if articles == nil {
		articles = []domain.Article{}
	}

	response.Success(w, http.StatusOK, "articles fetched successfully", articles)
}

// GetByID handles GET /api/v1/articles/{id}. {id} is either a UUID or a slug;
// only published articles are returned.
func (h *ArticleHandler) GetByID(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "id")

	var (
		article *domain.Article
		err     error
	)
	switch {
	case uuidPattern.MatchString(key):
		article, err = h.uc.GetByID(r.Context(), domain.VisibilityPublic, key)
	case dto.IsSlug(key):
		article, err = h.uc.GetBySlug(r.Context(), domain.VisibilityPublic, key)
	default:
		response.Error(w, http.StatusBadRequest, "invalid id or slug", nil)
		return
	}
	h.respondArticle(w, r, article, err, key)
}

// AdminGetByID handles GET /api/v1/admin/articles/{id}; any status is returned.
func (h *ArticleHandler) AdminGetByID(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}
	article, err := h.uc.GetByID(r.Context(), domain.VisibilityAll, id)
	h.respondArticle(w, r, article, err, id)
}

func (h *ArticleHandler) respondArticle(w http.ResponseWriter, r *http.Request, article *domain.Article, err error, key string) {
	if err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, "article not found", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: get article failed", "error", err, "key", key)
		response.Error(w, http.StatusInternalServerError, "failed to fetch article", nil)
		return
	}
	response.Success(w, http.StatusOK, "article fetched successfully", article)
}

// Update handles PUT /api/v1/articles/{id} (admin).
func (h *ArticleHandler) Update(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	var req dto.UpdateArticleRequest
	if !decodeJSON(w, r, &req) {
		return
	}

	if errs := dto.Validate(&req); errs != nil {
		response.Error(w, http.StatusBadRequest, "validation failed", errs)
		return
	}

	a := domain.Article{
		ID:          id,
		Title:       req.Title,
		Slug:        req.Slug,
		Excerpt:     req.Excerpt,
		Content:     req.Content,
		Tags:        nonNilTags(req.Tags),
		Status:      req.Status,
		PublishedAt: req.PublishedAt,
	}

	if err := h.uc.Update(r.Context(), &a); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, "article not found", nil)
			return
		}
		if errors.Is(err, domain.ErrConflict) {
			response.Error(w, http.StatusConflict, "article slug already exists", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: update article failed", "error", err, "id", id)
		response.Error(w, http.StatusInternalServerError, "failed to update article", nil)
		return
	}

	response.Success(w, http.StatusOK, "article updated successfully", a)
}

// Delete handles DELETE /api/v1/articles/{id} (admin).
func (h *ArticleHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUIDParam(w, r)
	if !ok {
		return
	}

	if err := h.uc.Delete(r.Context(), id); err != nil {
		if isNotFound(err) {
			response.Error(w, http.StatusNotFound, "article not found", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: delete article failed", "error", err, "id", id)
		response.Error(w, http.StatusInternalServerError, "failed to delete article", nil)
		return
	}

	response.Success(w, http.StatusOK, "article deleted successfully", nil)
}

// nonNilTags ensures tags serialise as [] and are stored as an empty array
// rather than NULL.
func nonNilTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}
