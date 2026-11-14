package main

import (
	"strings"
	"testing"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("APP_ENV", "local")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("GITHUB_CLIENT_ID", "id")
	t.Setenv("GITHUB_CLIENT_SECRET", "secret")
	t.Setenv("GITHUB_REDIRECT_URL", "http://localhost/cb")
	t.Setenv("ADMIN_GITHUB_ID", "1")
	t.Setenv("CONTACT_WEBHOOK_URL", "")
}

func TestLoadConfigOptionalF9Settings(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unset optional settings must be accepted: %v", err)
	}
	if cfg.contactWebhookURL != "" {
		t.Fatalf("default should be empty, got %q", cfg.contactWebhookURL)
	}

	t.Setenv("CONTACT_WEBHOOK_URL", " https://hooks.example.com/x ")
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.contactWebhookURL != "https://hooks.example.com/x" {
		t.Fatalf("webhook not loaded: %q", cfg.contactWebhookURL)
	}
}

func TestLoadConfigRejectsBadF9Settings(t *testing.T) {
	tests := []struct{ name, key, value, want string }{
		{"webhook not a URL", "CONTACT_WEBHOOK_URL", "not a url", "CONTACT_WEBHOOK_URL"},
		{"webhook wrong scheme", "CONTACT_WEBHOOK_URL", "ftp://example.com/x", "CONTACT_WEBHOOK_URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(tt.key, tt.value)
			_, err := loadConfig()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want it to name %s", err, tt.want)
			}
			if strings.Contains(err.Error(), tt.value) {
				t.Fatalf("error leaks the configured value: %v", err)
			}
		})
	}
}
