package handler

import (
	"log/slog"
	"net/http"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/dto"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

// BenchmarkHandler exposes the benchmark history (F8).
type BenchmarkHandler struct {
	uc usecase.BenchmarkUsecase
}

// NewBenchmarkHandler constructs a BenchmarkHandler.
func NewBenchmarkHandler(uc usecase.BenchmarkUsecase) *BenchmarkHandler {
	return &BenchmarkHandler{uc: uc}
}

// List handles GET /api/v1/benchmarks?limit=&offset= (public, cached).
func (h *BenchmarkHandler) List(w http.ResponseWriter, r *http.Request) {
	page, ok := parsePage(w, r)
	if !ok {
		return
	}

	items, err := h.uc.List(r.Context(), page)
	if err != nil {
		slog.ErrorContext(r.Context(), "handler: list benchmarks failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to fetch benchmarks", nil)
		return
	}
	if items == nil {
		items = []domain.Benchmark{}
	}

	response.Success(w, http.StatusOK, "benchmarks fetched successfully", items)
}

// Create handles POST /api/v1/benchmarks. Authorization (admin session or the
// CI bearer token) is enforced by middleware before this handler runs.
func (h *BenchmarkHandler) Create(w http.ResponseWriter, r *http.Request) {
	var req dto.BenchmarkRequest
	if !decodeJSON(w, r, &req) {
		return
	}
	if errs := dto.Validate(&req); errs != nil {
		response.Error(w, http.StatusBadRequest, "validation failed", errs)
		return
	}

	b := req.ToDomain()
	if err := h.uc.Create(r.Context(), &b); err != nil {
		slog.ErrorContext(r.Context(), "handler: create benchmark failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to store benchmark", nil)
		return
	}

	response.Success(w, http.StatusCreated, "benchmark stored successfully", b)
}
