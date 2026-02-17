package main

import (
	"fmt"
	"os"
	"strconv"
)

// Config holds all quarantine service configuration.
// Everything comes from environment variables — no shared config files with the bot.
type Config struct {
	// Path to the quarantine SQLite database.
	DBPath string

	// Bearer token required for pushers to submit items.
	PushToken string

	// Bearer token required for admin operations (list, review, promote).
	// If empty, admin endpoints are disabled over HTTP (CLI-only).
	AdminToken string

	// HTTP listen address. Default ":8033".
	ListenAddr string

	// Maximum content size per item in bytes. Default 256KB.
	MaxContentBytes int

	// Maximum number of pending items before new pushes are rejected. Default 1000.
	MaxPendingItems int

	// Bot workspace directory — the target for promoted files.
	// Only used by the promote command/endpoint.
	BotWorkspaceDir string
}

// LoadConfig reads configuration from environment variables.
func LoadConfig() (*Config, error) {
	cfg := &Config{
		ListenAddr:      envOr("QUARANTINE_LISTEN", ":8033"),
		DBPath:          os.Getenv("QUARANTINE_DB_PATH"),
		PushToken:       os.Getenv("QUARANTINE_PUSH_TOKEN"),
		AdminToken:      os.Getenv("QUARANTINE_ADMIN_TOKEN"),
		MaxContentBytes: envIntOr("QUARANTINE_MAX_CONTENT_BYTES", 256*1024),
		MaxPendingItems: envIntOr("QUARANTINE_MAX_PENDING_ITEMS", 1000),
		BotWorkspaceDir: os.Getenv("QUARANTINE_BOT_WORKSPACE"),
	}

	if cfg.DBPath == "" {
		return nil, fmt.Errorf("QUARANTINE_DB_PATH is required")
	}
	if cfg.PushToken == "" {
		return nil, fmt.Errorf("QUARANTINE_PUSH_TOKEN is required")
	}

	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}
