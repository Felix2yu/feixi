package config

import (
	"testing"
	"time"

	"github.com/Felix2yu/feixi/internal/engine"
)

func TestDefaultReadsEnvironment(t *testing.T) {
	t.Setenv("FEIXI_LISTEN", ":9000")
	t.Setenv("FEIXI_DB", "/tmp/x.db")
	t.Setenv("FEIXI_MAX_CHECKS", "7")
	t.Setenv("FEIXI_NOTIFY_DEADLINE", "2m")
	t.Setenv("FEIXI_ALLOW_PRIVATE_URLS", "false")
	t.Setenv("FEIXI_FLAP_THRESHOLD", "5")

	c := Default()
	if c.Listen != ":9000" || c.DBPath != "/tmp/x.db" {
		t.Errorf("listen/db not read from env: %+v", c)
	}
	if c.MaxChecks != 7 || c.FlapThreshold != 5 {
		t.Errorf("int env not parsed: max_checks=%d flap=%d", c.MaxChecks, c.FlapThreshold)
	}
	if c.NotifyDeadline != 2*time.Minute {
		t.Errorf("duration env not parsed: %v", c.NotifyDeadline)
	}
	if c.AllowPrivateURLs {
		t.Error("bool env not parsed")
	}
}

// 非法的环境值一律回落到默认值：启动阶段不该因为一个拼错的数字拒绝起来。
func TestDefaultFallsBackOnInvalidEnv(t *testing.T) {
	t.Setenv("FEIXI_PING_LOG_LIMIT", "abc")
	t.Setenv("FEIXI_NOTIFY_CONNECT_TIMEOUT", "10")
	t.Setenv("FEIXI_ALLOW_PRIVATE_URLS", "maybe")

	c := Default()
	if c.PingLogLimit != 100 {
		t.Errorf("ping_log_limit = %d, want fallback 100", c.PingLogLimit)
	}
	if c.NotifyConnectTimeout != 10*time.Second {
		t.Errorf("connect timeout = %v, want fallback 10s", c.NotifyConnectTimeout)
	}
	if !c.AllowPrivateURLs {
		t.Error("allow_private should keep the true default")
	}
}

func TestDefaultValues(t *testing.T) {
	c := Default()
	if c.PingBodyLimit != engine.PingBodyLimit || c.PingLogLimit != engine.PingLogLimit {
		t.Errorf("ping limits should default to engine constants: %+v", c)
	}
	if c.MasterBadgeLabel != "飞檄" {
		t.Errorf("badge label = %q", c.MasterBadgeLabel)
	}
	if c.MaxChecks != 0 {
		t.Error("check count should default to unlimited")
	}
	if c.NagPeriod != 0 {
		t.Errorf("nag should default off, got %v", c.NagPeriod)
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name     string
		listen   string
		siteRoot string
		wantRoot string
		wantHost string
		scheme   string
		wantErr  bool
	}{
		{
			name:     "empty root derives from listen address",
			listen:   ":8088",
			wantRoot: "http://localhost:8088", wantHost: "localhost:8088", scheme: "http",
		},
		{
			name:     "explicit bind host becomes the site root instead of being glued to localhost",
			listen:   "127.0.0.1:8088",
			wantRoot: "http://127.0.0.1:8088", wantHost: "127.0.0.1:8088", scheme: "http",
		},
		{
			name:     "wildcard IPv4 reads as localhost",
			listen:   "0.0.0.0:8088",
			wantRoot: "http://localhost:8088", wantHost: "localhost:8088", scheme: "http",
		},
		{
			name:     "wildcard IPv6 reads as localhost",
			listen:   "[::]:8088",
			wantRoot: "http://localhost:8088", wantHost: "localhost:8088", scheme: "http",
		},
		{
			name:     "trailing slashes are trimmed",
			listen:   ":8080",
			siteRoot: "https://status.example.com///",
			wantRoot: "https://status.example.com", wantHost: "status.example.com", scheme: "https",
		},
		{
			name:     "path prefix is preserved",
			listen:   ":8080",
			siteRoot: "https://example.com/feixi/",
			wantRoot: "https://example.com/feixi", wantHost: "example.com/feixi", scheme: "https",
		},
		{
			name:     "missing scheme is rejected",
			listen:   ":8080",
			siteRoot: "example.com",
			wantErr:  true,
		},
		{
			name:     "scheme only is rejected",
			listen:   ":8080",
			siteRoot: "http://",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Config{Listen: tt.listen, SiteRoot: tt.siteRoot}
			err := c.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Validate(%q) should fail", tt.siteRoot)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if c.SiteRoot != tt.wantRoot || c.Host != tt.wantHost || c.Scheme != tt.scheme {
				t.Errorf("got root=%q host=%q scheme=%q, want %q/%q/%q",
					c.SiteRoot, c.Host, c.Scheme, tt.wantRoot, tt.wantHost, tt.scheme)
			}
			if c.PingRoot() != c.SiteRoot {
				t.Errorf("PingRoot = %q, want SiteRoot %q", c.PingRoot(), c.SiteRoot)
			}
		})
	}
}
