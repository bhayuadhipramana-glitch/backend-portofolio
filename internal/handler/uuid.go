package handler

import (
	"net/http"
	"regexp"

	"github.com/go-chi/chi/v5"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// parseUUIDParam extracts and validates the {id} URL parameter as a UUID.
// It writes a 400 error response and returns false when the value is invalid.
func parseUUIDParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := chi.URLParam(r, "id")
	if !uuidPattern.MatchString(id) {
		response.Error(w, http.StatusBadRequest, "invalid id", nil)
		return "", false
	}
	return id, true
}
