package handler

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/domain"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/middleware"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/pkg/response"
	"github.com/bhayuadhipramana-glicth/backend-portofolio/internal/usecase"
)

const (
	stateCookieName = "oauth_state"
	stateCookieTTL  = 10 * time.Minute
	authCookiePath  = "/api/v1/auth"
)

// AuthHandler exposes the GitHub OAuth2 login flow and session endpoints.
type AuthHandler struct {
	uc         usecase.AuthUsecase
	appEnv     string // cookies are Secure unless this is "local" over plain http
	successURL string // where to send the browser after login; empty returns JSON
	sessionTTL time.Duration
}

// NewAuthHandler constructs an AuthHandler. sessionTTL must match the usecase
// TTL so the cookie and the server-side session expire together.
func NewAuthHandler(uc usecase.AuthUsecase, appEnv, successURL string, sessionTTL time.Duration) *AuthHandler {
	return &AuthHandler{uc: uc, appEnv: appEnv, successURL: successURL, sessionTTL: sessionTTL}
}

// Login handles GET /api/v1/auth/github/login.
// It stores a random CSRF state in a short-lived cookie and redirects to GitHub.
func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		slog.ErrorContext(r.Context(), "handler: generate oauth state failed", "error", err)
		response.Error(w, http.StatusInternalServerError, "failed to start login", nil)
		return
	}
	state := base64.RawURLEncoding.EncodeToString(b)

	http.SetCookie(w, &http.Cookie{
		Name:     stateCookieName,
		Value:    state,
		Path:     authCookiePath,
		MaxAge:   int(stateCookieTTL.Seconds()),
		HttpOnly: true,
		Secure:   middleware.IsSecureRequest(h.appEnv, r),
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, h.uc.LoginURL(state), http.StatusFound)
}

// Callback handles GET /api/v1/auth/github/callback.
// It verifies the CSRF state, completes the login and sets the session cookie.
func (h *AuthHandler) Callback(w http.ResponseWriter, r *http.Request) {
	secure := middleware.IsSecureRequest(h.appEnv, r)

	// The state cookie is single-use: clear it regardless of the outcome.
	http.SetCookie(w, &http.Cookie{
		Name: stateCookieName, Path: authCookiePath, MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})

	stateCookie, err := r.Cookie(stateCookieName)
	state := r.URL.Query().Get("state")
	if err != nil || state == "" || subtle.ConstantTimeCompare([]byte(stateCookie.Value), []byte(state)) != 1 {
		response.Error(w, http.StatusBadRequest, "invalid oauth state", nil)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		response.Error(w, http.StatusBadRequest, "missing authorization code", nil)
		return
	}

	token, session, err := h.uc.CompleteLogin(r.Context(), code)
	if err != nil {
		if errors.Is(err, domain.ErrForbidden) {
			slog.WarnContext(r.Context(), "handler: non-admin login attempt", "error", err)
			response.Error(w, http.StatusForbidden, "this github account is not allowed", nil)
			return
		}
		slog.ErrorContext(r.Context(), "handler: complete login failed", "error", err)
		response.Error(w, http.StatusBadGateway, "failed to complete github login", nil)
		return
	}

	// Path "/" and no Domain keep the cookie eligible for the __Host- prefix.
	http.SetCookie(w, &http.Cookie{
		Name:     middleware.SessionCookieName(secure),
		Value:    token,
		Path:     "/",
		Expires:  session.ExpiresAt,
		MaxAge:   int(h.sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})

	if h.successURL != "" {
		http.Redirect(w, r, h.successURL, http.StatusFound)
		return
	}
	response.Success(w, http.StatusOK, "login successful", session)
}

// Me handles GET /api/v1/auth/me. It runs behind RequireAdmin.
func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	session, ok := middleware.SessionFromContext(r.Context())
	if !ok {
		response.Error(w, http.StatusUnauthorized, "authentication required", nil)
		return
	}
	response.Success(w, http.StatusOK, "session fetched successfully", session)
}

// Logout handles POST /api/v1/auth/logout. It runs behind RequireAdmin.
func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	secure := middleware.IsSecureRequest(h.appEnv, r)
	name := middleware.SessionCookieName(secure)

	if c, err := r.Cookie(name); err == nil {
		if err := h.uc.Logout(r.Context(), c.Value); err != nil {
			slog.ErrorContext(r.Context(), "handler: logout failed", "error", err)
			response.Error(w, http.StatusInternalServerError, "failed to logout", nil)
			return
		}
	}

	http.SetCookie(w, &http.Cookie{
		Name: name, Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode,
	})
	response.Success(w, http.StatusOK, "logged out successfully", nil)
}
