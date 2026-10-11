package main

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/kelseyhightower/envconfig"
)

// Config is read from HERMES_CHAT_* environment variables.
type Config struct {
	HTTPAddr  string `envconfig:"HTTP_ADDR"  default:":8080"`
	DebugAddr string `envconfig:"DEBUG_ADDR" default:"127.0.0.1:6060"`
	DBPath    string `envconfig:"DB_PATH"    required:"true"`
	LogLevel  string `envconfig:"LOG_LEVEL"  default:"info"` // debug | info | warn | error
	GinMode   string `envconfig:"GIN_MODE"   default:"release"`
}

func loadConfig() (Config, error) {
	// envconfig reports a missing key without its prefix, and accepts an empty one.
	if os.Getenv("HERMES_CHAT_DB_PATH") == "" {
		return Config{}, fmt.Errorf("HERMES_CHAT_DB_PATH must be set and not empty")
	}
	var c Config
	if err := envconfig.Process("HERMES_CHAT", &c); err != nil {
		return Config{}, err
	}
	if _, ok := parseLevel(c.LogLevel); !ok {
		return Config{}, fmt.Errorf("HERMES_CHAT_LOG_LEVEL %q must be one of debug, info, warn, error", c.LogLevel)
	}
	switch c.GinMode {
	case "debug", "release", "test":
	default:
		return Config{}, fmt.Errorf("HERMES_CHAT_GIN_MODE %q must be one of debug, release, test", c.GinMode)
	}
	return c, nil
}

func parseLevel(s string) (slog.Level, bool) {
	switch s {
	case "debug":
		return slog.LevelDebug, true
	case "info":
		return slog.LevelInfo, true
	case "warn":
		return slog.LevelWarn, true
	case "error":
		return slog.LevelError, true
	}
	return slog.LevelInfo, false
}

func (c Config) slogLevel() slog.Level {
	l, _ := parseLevel(c.LogLevel)
	return l
}
