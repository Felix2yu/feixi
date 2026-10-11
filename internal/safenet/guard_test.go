package safenet

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func newGuard(p Policy) *guard {
	return &guard{policy: p, inner: &http.Transport{}, resolver: net.DefaultResolver}
}

func TestBlockedErrorMessages(t *testing.T) {
	if got := (&BlockedError{Host: "internal", IP: "10.0.0.1"}).Error(); got != "不允许连接到私有地址 10.0.0.1" {
		t.Errorf("with ip = %q", got)
	}
	if got := (&BlockedError{Host: "example.com"}).Error(); got != `不允许连接到 "example.com"（目标解析不到公网地址）` {
		t.Errorf("without ip = %q", got)
	}
}

func TestAllowedMatrix(t *testing.T) {
	open := newGuard(Policy{AllowPrivate: true})
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.1.1", "224.0.0.1", "::", "fe80::1", "8.8.8.8"} {
		if !open.allowed(net.ParseIP(raw)) {
			t.Errorf("AllowPrivate should accept %s", raw)
		}
	}

	strict := newGuard(Policy{AllowPrivate: false})
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.1", "172.16.0.1", "169.254.1.1",
		"224.0.0.1", "ff02::1", "::", "fe80::1", "fc00::1"} {
		if strict.allowed(net.ParseIP(raw)) {
			t.Errorf("%s should be refused", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "2001:4860:4860::8888", "93.184.216.34"} {
		if ip := net.ParseIP(raw); ip == nil || !strict.allowed(ip) {
			t.Errorf("%s is a public address and should be allowed", raw)
		}
	}
}

func TestDialRejectsAnAddressWithoutAPort(t *testing.T) {
	g := newGuard(Policy{AllowPrivate: true})
	_, err := g.dial(context.Background(), "tcp", "127.0.0.1")
	if err == nil {
		t.Fatal("a missing port should fail before anything is resolved")
	}
	if !strings.Contains(err.Error(), "port") {
		t.Errorf("err = %v, want a missing-port error", err)
	}
}

func TestDialRefusesALoopbackLiteral(t *testing.T) {
	g := newGuard(Policy{AllowPrivate: false})
	_, err := g.dial(context.Background(), "tcp", "127.0.0.1:80")
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v, want BlockedError", err)
	}
	if blocked.IP != "127.0.0.1" {
		t.Errorf("blocked ip = %q", blocked.IP)
	}
}

func TestDialNeverClaimsABlockedPrivateForAPublicLiteral(t *testing.T) {
	// 本机网络会接管出网连接，这里只锁定性质：公网 IP 字面量不会走到
	// 「解析不到公网地址」那条拒绝分支，要么连上、要么是普通连接错误。
	g := newGuard(Policy{AllowPrivate: false, Connect: 150 * time.Millisecond})
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	conn, err := g.dial(ctx, "tcp", "203.0.113.9:80")
	if err == nil {
		conn.Close()
		return
	}
	var blocked *BlockedError
	if errors.As(err, &blocked) {
		t.Fatalf("a public literal was reported as blocked: %v", err)
	}
}

func TestDialAllowsPrivateTargetsWhenPermitted(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			conn.Close()
		}
	}()

	g := newGuard(Policy{AllowPrivate: true, Connect: time.Second})
	conn, err := g.dial(context.Background(), "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if conn == nil {
		t.Fatal("nil connection")
	}
	conn.Close()
}

func TestDialBlocksANameResolvingOnlyToLocalhost(t *testing.T) {
	// localhost 由 /etc/hosts 决定，不需要外网就能得到「解析出来但全是内网」的答案。
	g := newGuard(Policy{AllowPrivate: false, Connect: 150 * time.Millisecond})
	_, err := g.dial(context.Background(), "tcp", "localhost:80")
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v, want BlockedError", err)
	}
	if blocked.Host != "localhost" {
		t.Errorf("blocked host = %q", blocked.Host)
	}
	if !strings.Contains(blocked.Error(), "127.0.0.1") && !strings.Contains(blocked.Error(), "::1") {
		t.Errorf("message should name the refused address: %q", blocked.Error())
	}
}

func TestDialConnectsThroughTheResolvedAddress(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			conn.Close()
		}
	}()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	g := newGuard(Policy{AllowPrivate: true, Connect: time.Second})
	conn, err := g.dial(context.Background(), "tcp", net.JoinHostPort("localhost", port))
	if err != nil {
		t.Fatalf("dial through localhost: %v", err)
	}
	conn.Close()
}

func TestDialReportsTheConnectionErrorAfterAcceptingTheAddress(t *testing.T) {
	// 52454 没人监听：地址本身合法，失败发生在连接阶段。
	g := newGuard(Policy{AllowPrivate: true, Connect: 150 * time.Millisecond})
	_, err := g.dial(context.Background(), "tcp", "localhost:52454")
	if err == nil {
		t.Fatal("expected a connection failure")
	}
	var blocked *BlockedError
	if errors.As(err, &blocked) {
		t.Fatalf("a refused connection should not be reported as blocked: %v", err)
	}
}

func TestInstallFillsInDefaultTimeouts(t *testing.T) {
	restore(t)
	Install(Policy{AllowPrivate: true})

	g, ok := http.DefaultTransport.(*guard)
	if !ok {
		t.Fatalf("DefaultTransport = %T, want *guard", http.DefaultTransport)
	}
	if g.policy.Connect != 10*time.Second || g.policy.Read != 10*time.Second {
		t.Errorf("zero timeouts should fall back to 10s, got %+v", g.policy)
	}
	if g.inner.ResponseHeaderTimeout != 10*time.Second || g.inner.TLSHandshakeTimeout != 10*time.Second {
		t.Errorf("transport timeouts = %+v", g.inner)
	}

	Install(Policy{Connect: time.Second, Read: 2 * time.Second})
	g = http.DefaultTransport.(*guard)
	if g.policy.Connect != time.Second || g.inner.ResponseHeaderTimeout != 2*time.Second {
		t.Errorf("explicit timeouts ignored: %+v/%+v", g.policy, g.inner)
	}
}

func TestRoundTripRejectsOtherSchemes(t *testing.T) {
	restore(t)
	Install(Policy{AllowPrivate: false, Connect: time.Second, Read: time.Second})

	g := http.DefaultTransport.(*guard)
	for _, rawURL := range []string{"ftp://example.com", "file:///etc/passwd"} {
		req, err := http.NewRequest("GET", rawURL, nil)
		if err != nil {
			t.Fatalf("request %q: %v", rawURL, err)
		}
		if _, err := g.RoundTrip(req); err == nil {
			t.Errorf("%q should be refused", rawURL)
		} else if !strings.Contains(err.Error(), "只允许 http/https") {
			t.Errorf("err = %v", err)
		}
	}
}
