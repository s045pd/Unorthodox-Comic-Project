package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v10"
	"github.com/joho/godotenv"
)

type Config struct {
	Addr                  string        `env:"SE8_ADDR"                     envDefault:"0.0.0.0:8000"`
	BaseURL               string        `env:"SE8_BASE_URL"                 envDefault:"https://se8.us"`
	VolDir                string        `env:"SE8_VOL_DIR"                  envDefault:"./vol"`
	MaxConcurrentRequests int           `env:"SE8_MAX_CONCURRENT_REQUESTS"  envDefault:"20"`
	WorkerCount           int           `env:"SE8_WORKER_COUNT"             envDefault:"4"`
	MaxPage               int           `env:"SE8_MAX_PAGE"                 envDefault:"2000"`
	HTTPTimeout           time.Duration `env:"SE8_HTTP_TIMEOUT"             envDefault:"60s"`
	SessionTTL            time.Duration `env:"SE8_SESSION_TTL"              envDefault:"720h"`
	SecureCookie          bool          `env:"SE8_SECURE_COOKIE"            envDefault:"false"`
	LogLevel              string        `env:"SE8_LOG_LEVEL"                envDefault:"info"`
	LogFormat             string        `env:"SE8_LOG_FORMAT"               envDefault:"text"`
	Debug                 bool          `env:"SE8_DEBUG"                    envDefault:"false"`
}

// Load reads .env (if envFile is non-empty and exists) then parses env vars.
func Load(envFile string) (Config, error) {
	if envFile != "" {
		_ = godotenv.Load(envFile) // best-effort; missing file is OK
	}
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return cfg, fmt.Errorf("parse env: %w", err)
	}
	return cfg, nil
}
