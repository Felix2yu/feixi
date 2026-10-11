// Package safenet installs the outbound transport used by every notification.
//
// apprise-go clones http.DefaultTransport and overwrites its DialContext, so a
// custom transport would normally be ignored. It keeps the original object when
// http.DefaultTransport is not a *http.Transport (internal/notify/http_client.go),
// which is exactly the hook this package uses: one guarded RoundTripper then
// covers apprise-go's own client and the http.DefaultClient that matrix, slack,
// sns and emby reach for directly.
package safenet

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

type Policy struct {
	// AllowPrivate permits targets that resolve to private, loopback or
	// link-local addresses. It is on by default because a self-hosted monitor
	// usually watches services that live on the same LAN; set
	// FEIXI_ALLOW_PRIVATE_URLS=false to refuse them.
	AllowPrivate bool
	Connect      time.Duration
	Read         time.Duration
}

type guard struct {
	policy   Policy
	inner    *http.Transport
	resolver *net.Resolver
}

// BlockedError reports a refused destination.
type BlockedError struct {
	Host string
	IP   string
}

func (e *BlockedError) Error() string {
	if e.IP == "" {
		return fmt.Sprintf("不允许连接到 %q（目标解析不到公网地址）", e.Host)
	}
	return fmt.Sprintf("不允许连接到私有地址 %s", e.IP)
}

func Install(p Policy) {
	if p.Connect <= 0 {
		p.Connect = 10 * time.Second
	}
	if p.Read <= 0 {
		p.Read = 10 * time.Second
	}

	g := &guard{policy: p, resolver: net.DefaultResolver}
	g.inner = &http.Transport{
		DialContext:           g.dial,
		ResponseHeaderTimeout: p.Read,
		TLSHandshakeTimeout:   p.Connect,
		ExpectContinueTimeout: time.Second,
		MaxIdleConnsPerHost:   4,
		ForceAttemptHTTP2:     true,
	}
	http.DefaultTransport = g
}

func (g *guard) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, fmt.Errorf("只允许 http/https，收到 %q", req.URL.Scheme)
	}
	// Every redirect hop is a fresh RoundTrip, so the guard applies to the
	// whole chain rather than only the first URL.
	return g.inner.RoundTrip(req)
}

// dial resolves the name itself and connects to the chosen address, so a DNS
// answer that flips between public and private names cannot slip through after
// the check.
func (g *guard) dial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if ip := net.ParseIP(host); ip != nil {
		if !g.allowed(ip) {
			return nil, &BlockedError{Host: host, IP: ip.String()}
		}
	}

	dialer := &net.Dialer{Timeout: g.policy.Connect, KeepAlive: 30 * time.Second}
	if g.policy.AllowPrivate {
		return dialer.DialContext(ctx, network, addr)
	}

	if net.ParseIP(host) != nil {
		return dialer.DialContext(ctx, network, net.JoinHostPort(host, port))
	}

	ips, err := g.resolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	var lastErr error
	for _, candidate := range ips {
		if !g.allowed(candidate.IP) {
			lastErr = &BlockedError{Host: host, IP: candidate.IP.String()}
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
		if err != nil {
			lastErr = err
			continue
		}
		return conn, nil
	}
	if lastErr == nil {
		lastErr = &BlockedError{Host: host}
	}
	return nil, lastErr
}

func (g *guard) allowed(ip net.IP) bool {
	if g.policy.AllowPrivate {
		return true
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return false
	}
	return true
}
