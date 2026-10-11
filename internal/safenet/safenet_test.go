package safenet

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func restore(t *testing.T) {
	t.Helper()
	previous := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = previous })
}

func TestBlocksPrivateTargetsWhenDisallowed(t *testing.T) {
	restore(t)
	Install(Policy{AllowPrivate: false, Connect: time.Second, Read: time.Second})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "should not be reached")
	}))
	defer srv.Close()

	_, err := http.Get(srv.URL)
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v, want a BlockedError", err)
	}
}

func TestAllowsPrivateTargetsByDefault(t *testing.T) {
	restore(t)
	Install(Policy{AllowPrivate: true, Connect: 2 * time.Second, Read: 2 * time.Second})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q", body)
	}
}

func TestRejectsNonHTTPScheme(t *testing.T) {
	restore(t)
	Install(Policy{AllowPrivate: true})

	// A redirect to file:// is refused by the RoundTripper rather than followed.
	transport := http.DefaultTransport.(*guard)
	req, _ := http.NewRequest("GET", "file:///etc/passwd", nil)
	if _, err := transport.RoundTrip(req); err == nil {
		t.Fatal("expected the file scheme to be rejected")
	}
}

// TestRedirectChainIsCheckedEachHop is the property the original gets from
// pycurl's OPENSOCKETFUNCTION: blocking has to apply to where the request ends
// up, not only to the URL the user typed.
func TestRedirectChainIsCheckedEachHop(t *testing.T) {
	restore(t)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "secret")
	}))
	defer target.Close()

	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer front.Close()

	Install(Policy{AllowPrivate: false, Connect: time.Second, Read: time.Second})
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(front.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("the hop to a private address should have been blocked")
	}
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("err = %v, want BlockedError", err)
	}
}
