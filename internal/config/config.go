package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"
)

type Config struct {
	Address       string
	DataDir       string
	AdminPassword string
	MediaRoots    []string
	MaxStreams    int
}

func Load() (Config, error) {
	cfg := Config{
		Address:       env("CINEBASE_ADDR", ":8097"),
		DataDir:       env("CINEBASE_DATA_DIR", "./data"),
		AdminPassword: os.Getenv("CINEBASE_ADMIN_PASSWORD"),
	}
	maxStreams, err := strconv.Atoi(env("CINEBASE_MAX_STREAMS", "32"))
	if err != nil || maxStreams < 1 || maxStreams > 256 {
		return Config{}, fmt.Errorf("CINEBASE_MAX_STREAMS must be between 1 and 256")
	}
	cfg.MaxStreams = maxStreams
	if utf8.RuneCountInString(cfg.AdminPassword) < 12 {
		return Config{}, fmt.Errorf("CINEBASE_ADMIN_PASSWORD must contain at least 12 characters")
	}
	for _, root := range filepath.SplitList(env("CINEBASE_MEDIA_ROOTS", "./media")) {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		abs, err := filepath.Abs(root)
		if err != nil {
			return Config{}, fmt.Errorf("resolve media root %q: %w", root, err)
		}
		cfg.MediaRoots = append(cfg.MediaRoots, filepath.Clean(abs))
	}
	if len(cfg.MediaRoots) == 0 {
		return Config{}, fmt.Errorf("CINEBASE_MEDIA_ROOTS must include at least one allowed path")
	}
	return cfg, nil
}

func (c Config) DatabasePath() string { return filepath.Join(c.DataDir, "cinebase.db") }

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
