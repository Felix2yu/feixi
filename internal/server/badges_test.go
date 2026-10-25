package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Felix2yu/feixi/internal/engine"
)

func badgeURLs(t *testing.T, ts *testServer, tag string) (two, three string) {
	t.Helper()
	doc := decode(t, ts.do(t, "GET", "/api/v3/badges/", ""))
	entry := doc["badges"].(map[string]any)[tag].(map[string]any)
	return entry["svg"].(string), entry["svg3"].(string)
}

func TestBadgeEndpoints(t *testing.T) {
	ts := newTestServer(t)
	doc, _ := createCheck(t, ts, map[string]any{"name": "备份", "tags": "prod", "timeout": 3600, "grace": 3600})

	two, three := badgeURLs(t, ts, "prod")
	if two == three {
		t.Fatal("two-state and three-state signatures must differ")
	}

	// A fresh check is "new", which reads as up in every aggregation.
	resp := get(t, ts, two)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("badge = %d", resp.StatusCode)
	}
	body := read(t, resp)
	if !strings.Contains(body, "#4c1") || !strings.Contains(body, "prod") {
		t.Errorf("svg = %s", body)
	}

	jsonDoc := decode(t, get(t, ts, pathFor(two, "json")))
	if jsonDoc["status"] != "up" || jsonDoc["total"].(float64) != 1 {
		t.Errorf("json badge = %v", jsonDoc)
	}

	shields := decode(t, get(t, ts, pathFor(two, "shields")))
	if shields["color"] != "success" || shields["label"] != "prod" || shields["schemaVersion"].(float64) != 1 {
		t.Errorf("shields = %v", shields)
	}

	// A tampered signature must 404 rather than leak the project state.
	bad := two[:strings.LastIndex(two, "/")+1] + "zzzzzzzz-2.svg"
	if resp := get(t, ts, bad); resp.StatusCode != http.StatusNotFound {
		t.Errorf("bad signature = %d, want 404", resp.StatusCode)
	}

	// The master badge covers everything and uses the configured label.
	master, _ := badgeURLs(t, ts, "*")
	if body := read(t, get(t, ts, master)); !strings.Contains(body, ts.cfg.MasterBadgeLabel) {
		t.Errorf("master badge = %s", body)
	}

	// The per-check badge is addressed by its own key.
	badgeKey := doc["badge_url"].(string)
	badgeKey = badgeKey[strings.LastIndex(badgeKey, "/")+1 : strings.LastIndex(badgeKey, ".svg")]
	if body := read(t, get(t, ts, "/b/2/"+badgeKey+".svg")); !strings.Contains(body, "备份") {
		t.Errorf("check badge = %s", body)
	}
	if resp := get(t, ts, "/b/4/"+badgeKey+".svg"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("states=4 = %d, want 404", resp.StatusCode)
	}
}

func TestBadgeLateState(t *testing.T) {
	ts := newTestServer(t)
	doc, code := createCheck(t, ts, map[string]any{"name": "x", "tags": "prod", "timeout": 3600, "grace": 3600})

	// Backdate the last ping so the check sits inside its grace window:
	// due 30 minutes ago, grace still running.
	past := time.Now().UTC().Add(-90 * time.Minute)
	if _, err := ts.db.Write.ExecContext(context.Background(),
		`UPDATE checks SET last_ping = ?, status = 'up' WHERE code = ?`,
		past.UnixMicro(), code); err != nil {
		t.Fatal(err)
	}
	c, err := ts.db.CheckByCode(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	if s, _ := engine.StatusOf(c, time.Now().UTC(), ts.db.Sched, false); s != engine.DisplayGrace {
		t.Fatalf("setup: status = %s, want grace", s)
	}

	two, three := badgeURLs(t, ts, "prod")
	if body := read(t, get(t, ts, two)); strings.Contains(body, "#fe7d37") {
		t.Error("a two-state badge must count grace as up")
	}
	if body := read(t, get(t, ts, three)); !strings.Contains(body, "#fe7d37") {
		t.Error("a three-state badge must show late")
	}

	jsonDoc := decode(t, get(t, ts, pathFor(three, "json")))
	if jsonDoc["status"] != "late" || jsonDoc["grace"].(float64) != 1 {
		t.Errorf("three-state json = %v", jsonDoc)
	}

	// The per-check badges follow the same rule.
	key := doc["badge_url"].(string)
	key = key[strings.LastIndex(key, "/")+1 : strings.LastIndex(key, ".svg")]
	if body := read(t, get(t, ts, "/b/2/"+key+".svg")); strings.Contains(body, "late") {
		t.Error("/b/2 must not render late")
	}
	if body := read(t, get(t, ts, "/b/3/"+key+".svg")); !strings.Contains(body, "late") {
		t.Error("/b/3 must render late")
	}
}

func pathFor(url, ext string) string {
	i := strings.LastIndex(url, ".")
	return url[:i] + "." + ext
}

func get(t *testing.T, ts *testServer, path string) *http.Response {
	t.Helper()
	if strings.HasPrefix(path, "http") {
		path = strings.TrimPrefix(path, ts.cfg.SiteRoot)
	}
	return ts.do(t, "GET", path, "")
}

func read(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
