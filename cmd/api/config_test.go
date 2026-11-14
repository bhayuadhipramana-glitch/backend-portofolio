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
	t.Setenv("CI_BENCHMARK_TOKEN", "")
}

func TestLoadConfigOptionalF8F9Settings(t *testing.T) {
	setRequiredEnv(t)
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unset optional settings must be accepted: %v", err)
	}
	if cfg.contactWebhookURL != "" || cfg.ciBenchmarkToken != "" {
		t.Fatalf("defaults should be empty, got %q / %q", cfg.contactWebhookURL, cfg.ciBenchmarkToken)
	}

	t.Setenv("CONTACT_WEBHOOK_URL", " https://hooks.example.com/x ")
	t.Setenv("CI_BENCHMARK_TOKEN", strings.Repeat("a", 32))
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.contactWebhookURL != "https://hooks.example.com/x" || len(cfg.ciBenchmarkToken) != 32 {
		t.Fatalf("settings not loaded: %q / %d chars", cfg.contactWebhookURL, len(cfg.ciBenchmarkToken))
	}
}

func TestLoadConfigRejectsBadF8F9Settings(t *testing.T) {
	tests := []struct{ name, key, value, want string }{
		{"webhook not a URL", "CONTACT_WEBHOOK_URL", "not a url", "CONTACT_WEBHOOK_URL"},
		{"webhook wrong scheme", "CONTACT_WEBHOOK_URL", "ftp://example.com/x", "CONTACT_WEBHOOK_URL"},
		{"short CI token", "CI_BENCHMARK_TOKEN", "short", "CI_BENCHMARK_TOKEN"},
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

func TestLoadConfigF6ObservabilitySettings(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("METRICS_TOKEN", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unset observability settings must be accepted: %v", err)
	}
	if cfg.metricsToken != "" || cfg.otlpEndpoint != "" {
		t.Fatalf("defaults should be empty, got %q / %q", cfg.metricsToken, cfg.otlpEndpoint)
	}

	t.Setenv("METRICS_TOKEN", " "+strings.Repeat("m", 32)+" ")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", " http://otel-collector:4318 ")
	cfg, err = loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.metricsToken) != 32 || cfg.otlpEndpoint != "http://otel-collector:4318" {
		t.Fatalf("settings not loaded: %d chars / %q", len(cfg.metricsToken), cfg.otlpEndpoint)
	}
}

func TestLoadConfigRejectsBadF6Settings(t *testing.T) {
	tests := []struct{ name, key, value string }{
		{"short metrics token", "METRICS_TOKEN", "short"},
		{"endpoint not a URL", "OTEL_EXPORTER_OTLP_ENDPOINT", "collector:4318"},
		{"endpoint wrong scheme", "OTEL_EXPORTER_OTLP_ENDPOINT", "grpc://collector:4317"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("METRICS_TOKEN", "")
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
			t.Setenv(tt.key, tt.value)
			_, err := loadConfig()
			if err == nil || !strings.Contains(err.Error(), tt.key) {
				t.Fatalf("err = %v, want it to name %s", err, tt.key)
			}
			if strings.Contains(err.Error(), tt.value) {
				t.Fatalf("error leaks the configured value: %v", err)
			}
		})
	}
}
