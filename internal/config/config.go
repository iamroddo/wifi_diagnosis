// Package config loads application configuration from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all application configuration.
type Config struct {
	// Server
	ServerPort int
	LogLevel   slog.Level

	// UniFi
	UnifiURL       string
	UnifiUsername  string
	UnifiPassword  string
	UnifiVerifyTLS bool
	UnifiSite      string

	// Persistence
	DataDir           string
	DataRetentionDays int
	NoPersistence     bool
	MaxStoredSessions int

	// Testing
	InternetTestURL string

	// UI
	SiteTitle    string
	ContactName  string
	ContactEmail string

	// Proxy
	TrustedProxies []net.IP
}

// Load reads configuration from environment variables, applying defaults where appropriate.
func Load() (*Config, error) {
	cfg := &Config{
		ServerPort:        8080,
		LogLevel:          slog.LevelInfo,
		UnifiSite:         "default",
		UnifiVerifyTLS:    false,
		DataDir:           "/app/data",
		DataRetentionDays: 30,
		MaxStoredSessions: 10000,
		InternetTestURL:   "https://one.one.one.one",
		SiteTitle:         "Wi-Fi Diagnostics",
	}

	if v := os.Getenv("SERVER_PORT"); v != "" {
		p, err := strconv.Atoi(v)
		if err != nil || p < 1 || p > 65535 {
			return nil, fmt.Errorf("SERVER_PORT: invalid value %q", v)
		}
		cfg.ServerPort = p
	}

	if v := os.Getenv("LOG_LEVEL"); v != "" {
		var l slog.Level
		if err := l.UnmarshalText([]byte(v)); err != nil {
			return nil, fmt.Errorf("LOG_LEVEL: invalid value %q", v)
		}
		cfg.LogLevel = l
	}

	cfg.UnifiURL = strings.TrimRight(os.Getenv("UNIFI_URL"), "/")
	cfg.UnifiUsername = os.Getenv("UNIFI_USERNAME")
	cfg.UnifiPassword = os.Getenv("UNIFI_PASSWORD")

	if v := os.Getenv("UNIFI_SITE"); v != "" {
		cfg.UnifiSite = v
	}

	if v := os.Getenv("UNIFI_VERIFY_TLS"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("UNIFI_VERIFY_TLS: invalid value %q", v)
		}
		cfg.UnifiVerifyTLS = b
	}

	if v := os.Getenv("DATA_DIR"); v != "" {
		cfg.DataDir = v
	}

	if v := os.Getenv("DATA_RETENTION_DAYS"); v != "" {
		d, err := strconv.Atoi(v)
		if err != nil || d < 0 {
			return nil, fmt.Errorf("DATA_RETENTION_DAYS: invalid value %q", v)
		}
		cfg.DataRetentionDays = d
	}

	if v := os.Getenv("NO_PERSISTENCE"); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return nil, fmt.Errorf("NO_PERSISTENCE: invalid value %q", v)
		}
		cfg.NoPersistence = b
	}

	if v := os.Getenv("MAX_STORED_SESSIONS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return nil, fmt.Errorf("MAX_STORED_SESSIONS: invalid value %q", v)
		}
		cfg.MaxStoredSessions = n
	}

	if v := os.Getenv("INTERNET_TEST_URL"); v != "" {
		cfg.InternetTestURL = v
	}

	if v := os.Getenv("SITE_TITLE"); v != "" {
		cfg.SiteTitle = v
	}

	cfg.ContactName = os.Getenv("CONTACT_NAME")
	cfg.ContactEmail = os.Getenv("CONTACT_EMAIL")

	if v := os.Getenv("TRUSTED_PROXIES"); v != "" {
		for _, raw := range strings.Split(v, ",") {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				continue
			}
			ip := net.ParseIP(raw)
			if ip == nil {
				return nil, fmt.Errorf("TRUSTED_PROXIES: invalid IP %q", raw)
			}
			cfg.TrustedProxies = append(cfg.TrustedProxies, ip)
		}
	}

	return cfg, nil
}

// RetentionDuration returns the data retention window, or zero if no retention limit.
func (c *Config) RetentionDuration() time.Duration {
	if c.DataRetentionDays <= 0 {
		return 0
	}
	return time.Duration(c.DataRetentionDays) * 24 * time.Hour
}

// UnifiConfigured returns true when enough UniFi config is present to attempt a connection.
func (c *Config) UnifiConfigured() bool {
	return c.UnifiURL != "" && c.UnifiUsername != "" && c.UnifiPassword != ""
}
