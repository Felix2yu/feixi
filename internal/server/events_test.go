package server

import (
	"io"
	"net/http"
	"testing"
)

func TestStatusTextAndEventsEndpoints(t *testing.T) {
	ts := newTestServer(t)
	_, code := createCheck(t, ts, map[string]any{"name": "events", "timeout": 3600, "grace": 60})

	// A healthy check answers Up in plain text.
	ts.do(t, "GET", "/ping/"+code, "").Body.Close()
	resp := ts.do(t, "GET", "/front/checks/"+code+"/status", "")
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(raw) != "Up" {
		t.Errorf("status text = %q, want Up", raw)
	}

	// Fail with exit code 3: text status goes Down, and both the pings API and
	// the merged event stream carry the exit code and the down reason.
	ts.do(t, "GET", "/ping/"+code+"/3", "").Body.Close()

	resp = ts.do(t, "GET", "/front/checks/"+code+"/status", "")
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(raw) != "Down" {
		t.Errorf("status text = %q, want Down", raw)
	}

	pingsDoc := decode(t, ts.do(t, "GET", "/api/v3/checks/"+code+"/pings/", ""))
	pings := pingsDoc["pings"].([]any)
	var sawExit bool
	for _, item := range pings {
		p := item.(map[string]any)
		if p["type"] == "fail" {
			if p["exit_status"].(float64) != 3 {
				t.Errorf("exit_status = %v, want 3", p["exit_status"])
			}
			sawExit = true
		}
	}
	if !sawExit {
		t.Error("no fail ping with exit_status found")
	}

	doc := decode(t, ts.do(t, "GET", "/front/checks/"+code+"/events", ""))
	events := doc["events"].([]any)
	if len(events) < 4 {
		t.Fatalf("events = %d, want at least 2 pings + 2 flips", len(events))
	}

	var sawDownFlip, sawFailPing bool
	for _, item := range events {
		e := item.(map[string]any)
		switch e["type"] {
		case "flip":
			if e["new_status"] == "down" {
				if e["reason"] != "fail" {
					t.Errorf("down flip reason = %v, want fail", e["reason"])
				}
				sawDownFlip = true
			}
		case "ping":
			if e["kind"] == "fail" && e["exit_status"].(float64) == 3 {
				sawFailPing = true
			}
		}
	}
	if !sawDownFlip {
		t.Error("event stream missing the down flip")
	}
	if !sawFailPing {
		t.Error("event stream missing the fail ping with exit code")
	}

	// Unknown check gives 404 rather than leaking a generic error page.
	if r := ts.do(t, "GET", "/front/checks/does-not-exist/status", ""); r.StatusCode != http.StatusNotFound {
		t.Errorf("missing check status = %d, want 404", r.StatusCode)
	}
}
