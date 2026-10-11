package server

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestStatusPage(t *testing.T) {
	ts := newTestServer(t)
	project, err := ts.db.Project(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// A wrong key behaves like a missing resource, never as the project page.
	if resp := get(t, ts, "/front/status-page/does-not-exist"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("bad key = %d, want 404", resp.StatusCode)
	}

	// Empty project: everything is operational.
	doc := decode(t, get(t, ts, "/front/status-page/"+project.BadgeKey))
	if doc["summary"] != "ok" || doc["total"].(float64) != 0 {
		t.Fatalf("empty page = %v", doc)
	}
	if doc["project"] != project.Name || doc["updated_at"] == "" {
		t.Fatalf("page header = %v", doc)
	}

	createCheck(t, ts, map[string]any{"name": "网关", "tags": "prod edge", "timeout": 3600, "grace": 60})
	doc = decode(t, get(t, ts, "/front/status-page/"+project.BadgeKey))
	if doc["total"].(float64) != 1 {
		t.Fatalf("total = %v", doc["total"])
	}
	row := doc["checks"].([]any)[0].(map[string]any)
	if row["name"] != "网关" || row["status"] != "new" || row["tags"] != "prod edge" {
		t.Fatalf("check row = %v", row)
	}
	if _, leaked := row["ping_url"]; leaked {
		t.Fatal("public row must not expose ping_url")
	}

	// Backdate well past timeout+grace: the page must flip to "down".
	past := time.Now().UTC().Add(-3 * time.Hour)
	if _, err := ts.db.Write.ExecContext(context.Background(),
		`UPDATE checks SET last_ping = ?, status = 'up'`, past.UnixMicro()); err != nil {
		t.Fatal(err)
	}
	doc = decode(t, get(t, ts, "/front/status-page/"+project.BadgeKey))
	if doc["summary"] != "down" || doc["down"].(float64) != 1 {
		t.Fatalf("down page = %v", doc)
	}
	row = doc["checks"].([]any)[0].(map[string]any)
	if row["status"] != "down" || row["last_ping"] == nil {
		t.Fatalf("down row = %v", row)
	}

	// Flapping is a presentation flag on top of the real status.
	if _, err := ts.db.Write.ExecContext(context.Background(),
		`UPDATE checks SET flapping = 1`); err != nil {
		t.Fatal(err)
	}
	doc = decode(t, get(t, ts, "/front/status-page/"+project.BadgeKey))
	row = doc["checks"].([]any)[0].(map[string]any)
	if row["flapping"] != true {
		t.Fatalf("flapping row = %v", row)
	}
}
