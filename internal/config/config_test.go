package config

import (
	"os"
	"testing"
	"time"
)

func TestLoad_Defaults(t *testing.T) {
	// Clear env
	for _, k := range []string{
		"SE8_ADDR", "SE8_BASE_URL", "SE8_VOL_DIR",
		"SE8_MAX_CONCURRENT_REQUESTS", "SE8_WORKER_COUNT", "SE8_MAX_PAGE",
		"SE8_HTTP_TIMEOUT", "SE8_SESSION_TTL",
		"SE8_LOG_LEVEL", "SE8_LOG_FORMAT", "SE8_DEBUG",
	} {
		os.Unsetenv(k)
	}

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "0.0.0.0:8000" {
		t.Errorf("Addr default = %q, want 0.0.0.0:8000", cfg.Addr)
	}
	if cfg.MaxConcurrentRequests != 20 {
		t.Errorf("MaxConcurrentRequests default = %d, want 20", cfg.MaxConcurrentRequests)
	}
	if cfg.WorkerCount != 4 {
		t.Errorf("WorkerCount default = %d, want 4", cfg.WorkerCount)
	}
	if cfg.HTTPTimeout != 60*time.Second {
		t.Errorf("HTTPTimeout default = %v, want 60s", cfg.HTTPTimeout)
	}
	if cfg.SessionTTL != 720*time.Hour {
		t.Errorf("SessionTTL default = %v, want 720h", cfg.SessionTTL)
	}
}

func TestLoad_Override(t *testing.T) {
	os.Setenv("SE8_ADDR", "127.0.0.1:9999")
	os.Setenv("SE8_WORKER_COUNT", "8")
	defer os.Unsetenv("SE8_ADDR")
	defer os.Unsetenv("SE8_WORKER_COUNT")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr != "127.0.0.1:9999" {
		t.Errorf("Addr = %q", cfg.Addr)
	}
	if cfg.WorkerCount != 8 {
		t.Errorf("WorkerCount = %d", cfg.WorkerCount)
	}
}
