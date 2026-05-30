// Package config loads gateway configuration from environment variables.
//
// We follow the 12-factor app principle: every deployment-specific value
// (ports, API keys, timeouts) comes from the environment, never from code.
// This is the Go equivalent of reading os.environ in Python or process.env in
// Node — but we centralize it into one typed struct so the rest of the code
// depends on a value, not on scattered global env lookups.
package config

import (
	"fmt"
	"os"
	"time"
)

// Config holds all runtime configuration for the gateway.
type Config struct {
	Port string // HTTP port the gateway listens on (e.g. "8080")

	OpenAIAPIKey   string        // secret: API key used to authenticate to OpenAI
	OpenAIBaseURL  string        // base URL for OpenAI's API; overridable for tests/proxies
	RequestTimeout time.Duration // per-request timeout for upstream provider calls
}

// Load reads configuration from the environment and validates it.
//
// It returns an error (rather than calling log.Fatal itself) so the caller
// decides how to handle failure. This is a core Go idiom: libraries return
// errors; only main() decides whether to exit.
func Load() (*Config, error) {
	cfg := &Config{
		Port:           getEnv("PORT", "8080"),
		OpenAIAPIKey:   os.Getenv("OPENAI_API_KEY"),
		OpenAIBaseURL:  getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		RequestTimeout: getDurationEnv("REQUEST_TIMEOUT", 60*time.Second),
	}

	if cfg.OpenAIAPIKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY is required")
	}
	return cfg, nil
}

// getEnv returns the value of the env var named key, or def if it is unset/empty.
func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getDurationEnv parses a Go duration env var (e.g. "30s", "2m"); falls back to def.
func getDurationEnv(key string, def time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return def
}
