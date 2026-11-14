package handler

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
)

// maxBodyBytes caps every JSON request body at 1 MiB.
const maxBodyBytes = 1 << 20

// decodeJSON reads the request body into dst. The body is capped at
// maxBodyBytes, unknown fields are rejected and anything after the first JSON
// value is an error. On failure it writes the error response (413 for an
// oversized body, 400 otherwise) and returns false.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSONLimit(w, r, dst, maxBodyBytes)
}

// decodeJSONLimit is decodeJSON with an explicit body cap, for endpoints that
// need a tighter limit than the default (such as F3 event batches).
func decodeJSONLimit(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	err := dec.Decode(dst)
	if err == nil {
		// A second value (or garbage) after the document is not allowed.
		if _, trailErr := dec.Token(); !errors.Is(trailErr, io.EOF) {
			err = errors.New("unexpected data after JSON body")
		}
	}
	if err == nil {
		return true
	}

	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		response.Error(w, http.StatusRequestEntityTooLarge, "request body too large", nil)
		return false
	}

	slog.WarnContext(r.Context(), "handler: invalid JSON body", "error", err)
	response.Error(w, http.StatusBadRequest, "invalid request body", nil)
	return false
}

// parsePage reads ?limit= and ?offset= from the query string. limit defaults
// to domain.DefaultPageLimit and is capped at domain.MaxPageLimit. It writes a
// 400 response and returns false for non-numeric or out-of-range values.
func parsePage(w http.ResponseWriter, r *http.Request) (domain.Page, bool) {
	page := domain.Page{Limit: domain.DefaultPageLimit}
	q := r.URL.Query()

	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			response.Error(w, http.StatusBadRequest, "limit must be a positive integer", nil)
			return domain.Page{}, false
		}
		page.Limit = min(n, domain.MaxPageLimit)
	}
	if raw := q.Get("offset"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			response.Error(w, http.StatusBadRequest, "offset must be a non-negative integer", nil)
			return domain.Page{}, false
		}
		page.Offset = n
	}
	return page, true
}
