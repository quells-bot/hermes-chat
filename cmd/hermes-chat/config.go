package main

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	"github.com/kelseyhightower/envconfig"
)

// Config is read from HERMES_CHAT_* environment variables.
type Config struct {
	HTTPAddr  string `split_words:"true" default:":8080"`
	DebugAddr string `split_words:"true" default:"127.0.0.1:6060"`
	DBPath    string `split_words:"true"`
	LogLevel  string `split_words:"true" default:"info"` // debug | info | warn | error
	GinMode   string `split_words:"true" default:"release"`

	CookieSecure   bool     `split_words:"true" default:"true"` // HERMES_CHAT_COOKIE_SECURE
	TrustedProxies []string `split_words:"true"`                // HERMES_CHAT_TRUSTED_PROXIES, comma-separated IPs or CIDRs; empty trusts none
	AttachmentsDir string   `split_words:"true"`                // HERMES_CHAT_ATTACHMENTS_DIR; "" means "attachments" beside the database file
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
	return c, nil
}

// validateForServe checks the settings only the serve command uses.
func (c Config) validateForServe() error {
	if err := checkLoopback(c.DebugAddr); err != nil {
		return fmt.Errorf("HERMES_CHAT_DEBUG_ADDR %q: %w", c.DebugAddr, err)
	}
	switch c.GinMode {
	case "debug", "release", "test":
	default:
		return fmt.Errorf("HERMES_CHAT_GIN_MODE %q must be one of debug, release, test", c.GinMode)
	}
	for _, p := range c.TrustedProxies {
		if _, _, err := net.ParseCIDR(p); err != nil && net.ParseIP(p) == nil {
			return fmt.Errorf("HERMES_CHAT_TRUSTED_PROXIES entry %q is neither an IP address nor a CIDR", p)
		}
	}
	return nil
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

// attachmentsDir returns where attachment bodies live: the configured
// directory, or one named attachments beside the database file.
func (c Config) attachmentsDir() string {
	if c.AttachmentsDir != "" {
		return c.AttachmentsDir
	}
	return filepath.Join(filepath.Dir(c.DBPath), "attachments")
}

// checkLoopback requires addr to name a loopback host, so pprof is never public.
func checkLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		return nil
	}
	return fmt.Errorf("host must be a loopback address or localhost")
}
