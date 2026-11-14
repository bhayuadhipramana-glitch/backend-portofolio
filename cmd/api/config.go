package main

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	sessionTTL = 24 * time.Hour

	// envLocal is the only APP_ENV value that permits plain-http cookies.
	envLocal = "local"

	// defaultCORSOrigin is used in local mode when CORS_ALLOWED_ORIGINS is unset.
	defaultCORSOrigin = "http://localhost:3000"

	defaultPort = "8080"

	// defaultValkeyAddr is the loopback address of the docker-compose Valkey.
	defaultValkeyAddr = "127.0.0.1:6379"
)

// config holds every setting read from the environment.
type config struct {
	appEnv      string
	port        string
	databaseURL string
	valkeyAddr  string // host:port of the L2 cache

	// trustCloudflare makes the API take the client IP from CF-Connecting-IP.
	// Enable it only when the API is reachable exclusively through the tunnel.
	trustCloudflare bool

	clientID      string
	clientSecret  string
	redirectURL   string   // must equal the callback URL registered in the GitHub OAuth app
	adminGithubID int64    // numeric GitHub user id of the single admin
	allowedOrigin []string // exact origins allowed for CORS and for cookie-authenticated mutations
	successURL    string   // optional browser redirect after login
}

// loadConfig reads and validates the environment. It fails fast when a
// required secret is missing so the process never starts half-configured.
//
//	APP_ENV                  "local" (default) or any other value for deployed environments
//	PORT                     listen port (default 8080)
//	DATABASE_URL             PostgreSQL connection string (required)
//	VALKEY_ADDR              Valkey host:port for the L2 cache (default 127.0.0.1:6379)
//	GITHUB_CLIENT_ID, GITHUB_CLIENT_SECRET, GITHUB_REDIRECT_URL, ADMIN_GITHUB_ID  (required)
//	CORS_ALLOWED_ORIGINS     comma-separated exact origins (required unless APP_ENV is local)
//	TRUST_CLOUDFLARE_TUNNEL  "true" to trust CF-Connecting-IP (default false)
//	AUTH_SUCCESS_URL         where to redirect after a successful login (default: JSON response)
func loadConfig() (config, error) {
	cfg := config{
		appEnv:          strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))),
		port:            os.Getenv("PORT"),
		databaseURL:     os.Getenv("DATABASE_URL"),
		valkeyAddr:      strings.TrimSpace(os.Getenv("VALKEY_ADDR")),
		trustCloudflare: strings.EqualFold(os.Getenv("TRUST_CLOUDFLARE_TUNNEL"), "true"),
		clientID:        os.Getenv("GITHUB_CLIENT_ID"),
		clientSecret:    os.Getenv("GITHUB_CLIENT_SECRET"),
		redirectURL:     os.Getenv("GITHUB_REDIRECT_URL"),
		successURL:      os.Getenv("AUTH_SUCCESS_URL"),
	}
	if cfg.appEnv == "" {
		cfg.appEnv = envLocal
	}
	if cfg.valkeyAddr == "" {
		cfg.valkeyAddr = defaultValkeyAddr
	}
	if cfg.port == "" {
		cfg.port = defaultPort
	}

	required := []struct{ name, value string }{
		{"DATABASE_URL", cfg.databaseURL},
		{"GITHUB_CLIENT_ID", cfg.clientID},
		{"GITHUB_CLIENT_SECRET", cfg.clientSecret},
		{"GITHUB_REDIRECT_URL", cfg.redirectURL},
		{"ADMIN_GITHUB_ID", os.Getenv("ADMIN_GITHUB_ID")},
	}
	for _, r := range required {
		if strings.TrimSpace(r.value) == "" {
			return config{}, fmt.Errorf("environment variable %s is not set", r.name)
		}
	}

	id, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("ADMIN_GITHUB_ID")), 10, 64)
	if err != nil || id <= 0 {
		return config{}, errors.New("environment variable ADMIN_GITHUB_ID must be a positive integer")
	}
	cfg.adminGithubID = id

	origins := os.Getenv("CORS_ALLOWED_ORIGINS")
	if strings.TrimSpace(origins) == "" {
		if cfg.appEnv != envLocal {
			return config{}, errors.New("environment variable CORS_ALLOWED_ORIGINS is required when APP_ENV is not local")
		}
		origins = defaultCORSOrigin
	}
	for _, o := range strings.Split(origins, ",") {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		if o == "*" {
			return config{}, errors.New("CORS_ALLOWED_ORIGINS must list exact origins; a wildcard is not allowed with credentials")
		}
		cfg.allowedOrigin = append(cfg.allowedOrigin, o)
	}

	return cfg, nil
}
