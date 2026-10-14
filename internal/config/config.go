// Package config turns environment variables into the runtime settings.
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/yufei/feixi/internal/engine"
)

type Config struct {
	Listen   string
	DBPath   string
	SiteRoot string // absolute base used to build ping_url / badge_url in API responses
	Scheme   string
	Host     string

	PingBodyLimit int
	PingLogLimit  int
	// MaxChecks caps checks per project; 0 (the default) means no cap.
	MaxChecks int
	// MasterBadgeLabel is the left half of the all-tags badge.
	MasterBadgeLabel string

	// SecretKey signs badge URLs. Empty means "generate once and keep in the DB".
	SecretKey string

	AllowPrivateURLs bool

	NotifyConnectTimeout time.Duration
	NotifyReadTimeout    time.Duration
	NotifyDeadline       time.Duration
	NotifyConcurrency    int
	NotifyRetries        int

	AlertScanInterval time.Duration
	FlipIdleSleep     time.Duration
	// PruneInterval is how often the daemon sweeps every check for old pings,
	// notifications and flips. Zero disables the sweep, leaving only the hints
	// that arriving pings push into the queue.
	PruneInterval time.Duration

	// NagPeriod is the gap between repeated reminders for a still-down check.
	// Zero disables nagging entirely; channels must also opt in per channel.
	NagPeriod time.Duration
	// NagScanInterval is how often the daemon looks for due reminders.
	NagScanInterval time.Duration

	// FlapWindow is the rolling window in which FlapThreshold alertable
	// transitions mark a check as flapping; zero threshold disables it.
	FlapWindow time.Duration
	// FlapThreshold is the number of down/up alerts inside one window after
	// which per-flip alerts are merged into one flap notice.
	FlapThreshold int
	// FlapScanInterval is how often the daemon settles calmed-down checks.
	FlapScanInterval time.Duration

	LogLevel string
}

// Default is the single-tenant self-hosted shape: listen on all interfaces, keep
// data next to the binary, and allow notifications to reach private addresses
// because that is what a homelab monitor is pointed at.
func Default() *Config {
	return &Config{
		Listen:               env("FEIXI_LISTEN", ":8080"),
		DBPath:               env("FEIXI_DB", "data/feixi.db"),
		SiteRoot:             env("FEIXI_SITE_ROOT", ""),
		PingBodyLimit:        envInt("FEIXI_PING_BODY_LIMIT", engine.PingBodyLimit),
		PingLogLimit:         envInt("FEIXI_PING_LOG_LIMIT", engine.PingLogLimit),
		MaxChecks:            envInt("FEIXI_MAX_CHECKS", 0),
		MasterBadgeLabel:     env("FEIXI_MASTER_BADGE_LABEL", "飞檄"),
		SecretKey:            env("FEIXI_SECRET_KEY", ""),
		AllowPrivateURLs:     envBool("FEIXI_ALLOW_PRIVATE_URLS", true),
		NotifyConnectTimeout: envDuration("FEIXI_NOTIFY_CONNECT_TIMEOUT", 10*time.Second),
		NotifyReadTimeout:    envDuration("FEIXI_NOTIFY_READ_TIMEOUT", 10*time.Second),
		NotifyDeadline:       envDuration("FEIXI_NOTIFY_DEADLINE", 45*time.Second),
		NotifyConcurrency:    envInt("FEIXI_NOTIFY_CONCURRENCY", 4),
		NotifyRetries:        envInt("FEIXI_NOTIFY_RETRIES", 2),
		AlertScanInterval:    envDuration("FEIXI_ALERT_SCAN_INTERVAL", 2*time.Second),
		FlipIdleSleep:        envDuration("FEIXI_FLIP_IDLE_SLEEP", time.Second),
		PruneInterval:        envDuration("FEIXI_PRUNE_INTERVAL", 24*time.Hour),
		NagPeriod:            envDuration("FEIXI_NAG_PERIOD", 0),
		NagScanInterval:      envDuration("FEIXI_NAG_SCAN_INTERVAL", time.Minute),
		FlapWindow:           envDuration("FEIXI_FLAP_WINDOW", time.Hour),
		FlapThreshold:        envInt("FEIXI_FLAP_THRESHOLD", 3),
		FlapScanInterval:     envDuration("FEIXI_FLAP_SCAN_INTERVAL", time.Minute),
		LogLevel:             env("FEIXI_LOG", "info"),
	}
}

// Validate normalizes the site root and derives scheme/host from it.
func (c *Config) Validate() error {
	root := c.SiteRoot
	if root == "" {
		root = "http://localhost" + c.Listen
	}
	switch {
	case len(root) >= 8 && root[:7] == "http://":
		c.Scheme, root = "http", root[7:]
	case len(root) >= 9 && root[:8] == "https://":
		c.Scheme, root = "https", root[8:]
	default:
		return fmt.Errorf("FEIXI_SITE_ROOT 必须以 http:// 或 https:// 开头，收到 %q", c.SiteRoot)
	}
	for len(root) > 0 && root[len(root)-1] == '/' {
		root = root[:len(root)-1]
	}
	c.SiteRoot = c.Scheme + "://" + root
	c.Host = root
	return nil
}

// PingRoot is the base of ping URLs, kept separate from SiteRoot so pings can
// be given a hostname of their own later.
func (c *Config) PingRoot() string { return c.SiteRoot }

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			return d
		}
	}
	return fallback
}
