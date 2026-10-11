package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yufei/feixi/internal/config"
	"github.com/yufei/feixi/internal/cronex"
	"github.com/yufei/feixi/internal/daemon"
	"github.com/yufei/feixi/internal/store"
)

type testServer struct {
	handler   http.Handler
	db        *store.DB
	cfg       *config.Config
	projectID int64
}

func newTestServer(t *testing.T) *testServer {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Sched = cronex.Scheduler{}
	db.MaxChecks = 100
	db.PingLogLimit = 100

	cfg := config.Default()
	cfg.DBPath = db.Path
	cfg.Listen = ":0"
	cfg.SiteRoot = "http://example.test"
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	cfg.SiteRoot = "http://example.test"
	cfg.Scheme = "http"
	cfg.Host = "example.test"

	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	project, err := db.Seed(context.Background(), "test-secret")
	if err != nil {
		t.Fatal(err)
	}

	d := daemon.New(db, cfg, slog.Default())
	srv := New(cfg, db, d, []string{"json", "mailto"}, nil, nil)
	return &testServer{handler: srv.Router, db: db, cfg: cfg, projectID: project.ID}
}

func (ts *testServer) do(t *testing.T, method, path string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		switch v := body.(type) {
		case string:
			reader = strings.NewReader(v)
		default:
			b, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			reader = bytes.NewReader(b)
		}
	}
	req := httptest.NewRequest(method, path, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	return rec.Result()
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var doc map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return doc
}

func createCheck(t *testing.T, ts *testServer, spec map[string]any) (map[string]any, string) {
	t.Helper()
	resp := ts.do(t, "POST", "/api/v3/checks/", spec)
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create = %d %s", resp.StatusCode, body)
	}
	doc := decode(t, resp)
	return doc, doc["uuid"].(string)
}

func TestCheckGoldenShape(t *testing.T) {
	ts := newTestServer(t)
	doc, code := createCheck(t, ts, map[string]any{
		"name": "备份任务", "tags": "prod www", "timeout": 3600, "grace": 60,
	})

	want := []string{
		"name", "slug", "tags", "desc", "grace", "n_pings", "status", "started",
		"last_ping", "next_ping", "manual_resume", "methods", "subject", "subject_fail",
		"start_kw", "success_kw", "failure_kw", "filter_subject", "filter_body",
		"filter_http_body", "filter_default_fail", "badge_url", "uuid", "ping_url",
		"update_url", "pause_url", "resume_url", "channels", "timeout",
	}
	for _, key := range want {
		if _, ok := doc[key]; !ok {
			t.Errorf("CheckDict is missing %q", key)
		}
	}
	if doc["status"] != "new" {
		t.Errorf("status = %v, want new", doc["status"])
	}
	if doc["next_ping"] != nil {
		t.Errorf("next_ping = %v, want null for a new check", doc["next_ping"])
	}
	if doc["ping_url"] != "http://example.test/ping/"+code {
		t.Errorf("ping_url = %v", doc["ping_url"])
	}
	if badge, _ := doc["badge_url"].(string); !strings.HasPrefix(badge, ts.cfg.SiteRoot+"/b/2/") ||
		!strings.HasSuffix(badge, ".svg") {
		t.Errorf("badge_url = %v", doc["badge_url"])
	}
	for _, key := range []string{"last_ping", "next_ping"} {
		if v, ok := doc[key].(string); ok {
			if strings.Contains(v, ".") || !strings.HasSuffix(v, "+00:00") {
				t.Errorf("%s = %q, want second precision with a +00:00 offset", key, v)
			}
		}
	}
	// schedule/tz must not appear for a simple check.
	if _, ok := doc["schedule"]; ok {
		t.Error("simple check must not carry a schedule field")
	}
}

func TestCronCheckShape(t *testing.T) {
	ts := newTestServer(t)
	doc, _ := createCheck(t, ts, map[string]any{
		"name": "cron 任务", "schedule": "15 5 * * *", "tz": "Europe/Berlin", "grace": 1800,
	})
	if doc["schedule"] != "15 5 * * *" || doc["tz"] != "Europe/Berlin" {
		t.Errorf("schedule/tz = %v/%v", doc["schedule"], doc["tz"])
	}
	if _, ok := doc["timeout"]; ok {
		t.Error("cron check must not carry a timeout field")
	}
}

func TestUpsertWithUnique(t *testing.T) {
	ts := newTestServer(t)
	_, code := createCheck(t, ts, map[string]any{"name": "Nightly", "timeout": 3600, "grace": 60})

	resp := ts.do(t, "POST", "/api/v3/checks/", map[string]any{
		"name": "Nightly", "timeout": 7200, "unique": []string{"name"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upsert = %d, want 200", resp.StatusCode)
	}
	doc := decode(t, resp)
	if doc["uuid"] != code {
		t.Error("upsert must update the existing check")
	}
	if doc["timeout"].(float64) != 7200 {
		t.Errorf("timeout = %v, want 7200", doc["timeout"])
	}

	// A different name misses the unique match, so a new check is created.
	resp = ts.do(t, "POST", "/api/v3/checks/", map[string]any{
		"name": "Other", "timeout": 7200, "unique": []string{"name"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("new name = %d, want 201", resp.StatusCode)
	}
}

func TestPauseResumeCodes(t *testing.T) {
	ts := newTestServer(t)
	_, code := createCheck(t, ts, map[string]any{"name": "x", "timeout": 3600})

	if resp := ts.do(t, "POST", "/api/v3/checks/"+code+"/resume", ""); resp.StatusCode != http.StatusConflict {
		t.Errorf("resume of a new check = %d, want 409", resp.StatusCode)
	}

	resp := ts.do(t, "POST", "/api/v3/checks/"+code+"/pause", "")
	doc := decode(t, resp)
	if resp.StatusCode != http.StatusOK || doc["status"] != "paused" {
		t.Fatalf("pause = %d %v", resp.StatusCode, doc["status"])
	}

	// pause is idempotent
	resp = ts.do(t, "POST", "/api/v3/checks/"+code+"/pause", "")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("second pause = %d, want 200", resp.StatusCode)
	}

	resp = ts.do(t, "POST", "/api/v3/checks/"+code+"/resume", "")
	doc = decode(t, resp)
	if resp.StatusCode != http.StatusOK || doc["status"] != "new" {
		t.Fatalf("resume = %d %v", resp.StatusCode, doc["status"])
	}
}

func TestDeleteCheck(t *testing.T) {
	ts := newTestServer(t)
	_, code := createCheck(t, ts, map[string]any{"name": "gone"})

	if resp := ts.do(t, "DELETE", "/api/v3/checks/"+code, ""); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", resp.StatusCode)
	}
	if resp := ts.do(t, "GET", "/api/v3/checks/"+code, ""); resp.StatusCode != http.StatusNotFound {
		t.Error("a deleted check must be gone")
	}
	if resp := ts.do(t, "GET", "/ping/"+code, ""); resp.StatusCode != http.StatusNotFound {
		t.Error("the old ping URL must no longer resolve")
	}
}

func TestSpecValidation(t *testing.T) {
	ts := newTestServer(t)
	cases := []struct {
		name    string
		spec    map[string]any
		wantMsg string
	}{
		{"未知字段", map[string]any{"bogus": 1}, "JSON 验证错误"},
		{"null 值", map[string]any{"name": nil}, "不能为 null"},
		{"超时太小", map[string]any{"timeout": 5}, "60"},
		{"超时太大", map[string]any{"timeout": 40000000}, "31536000"},
		{"非法时区", map[string]any{"schedule": "0 1 * * *", "tz": "Mars/Rover"}, "timezone"},
		{"六字段 cron", map[string]any{"schedule": "0 0 1 * * *"}, "cron"},
		{"oncalendar", map[string]any{"schedule": "*-*-* 03:00:00"}, "OnCalendar"},
		{"methods 取值", map[string]any{"methods": "PUT"}, "methods"},
		{"slug 大写", map[string]any{"slug": "Nope"}, "slug"},
	}
	for _, tc := range cases {
		resp := ts.do(t, "POST", "/api/v3/checks/", tc.spec)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", tc.name, resp.StatusCode)
			resp.Body.Close()
			continue
		}
		doc := decode(t, resp)
		msg, _ := doc["error"].(string)
		if !strings.Contains(msg, tc.wantMsg) {
			t.Errorf("%s: error = %q, want it to mention %q", tc.name, msg, tc.wantMsg)
		}
	}
}

func TestUpdateCheckPartial(t *testing.T) {
	ts := newTestServer(t)
	_, code := createCheck(t, ts, map[string]any{
		"name": "keep", "tags": "a", "timeout": 3600, "grace": 120, "desc": "hello",
	})
	resp := ts.do(t, "POST", "/api/v3/checks/"+code, map[string]any{"name": "renamed"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update = %d", resp.StatusCode)
	}
	doc := decode(t, resp)
	if doc["name"] != "renamed" || doc["tags"] != "a" || doc["grace"].(float64) != 120 ||
		doc["desc"] != "hello" || doc["timeout"].(float64) != 3600 {
		t.Errorf("untouched fields changed: %v", doc)
	}
}

func TestListFiltering(t *testing.T) {
	ts := newTestServer(t)
	createCheck(t, ts, map[string]any{"name": "one", "tags": "prod www"})
	createCheck(t, ts, map[string]any{"name": "two", "tags": "prod"})
	createCheck(t, ts, map[string]any{"name": "three", "tags": "www"})

	doc := decode(t, ts.do(t, "GET", "/api/v3/checks/?tag=prod&tag=www", ""))
	names := checkNames(doc)
	if strings.Join(names, ",") != "one" {
		t.Errorf("multiple tags must AND, got %v", names)
	}

	doc = decode(t, ts.do(t, "GET", "/api/v3/checks/?tag=prod", ""))
	if len(checkNames(doc)) != 2 {
		t.Errorf("tag=prod should match 2 checks: %v", checkNames(doc))
	}

	doc = decode(t, ts.do(t, "GET", "/api/v3/checks/?slug=nope", ""))
	if len(checkNames(doc)) != 0 {
		t.Error("unknown slug should match nothing")
	}
}

func checkNames(doc map[string]any) []string {
	var out []string
	for _, c := range doc["checks"].([]any) {
		out = append(out, c.(map[string]any)["name"].(string))
	}
	return out
}

func TestListOrderby(t *testing.T) {
	ts := newTestServer(t)
	codes := map[string]string{}
	for _, name := range []string{"beta", "alpha", "gamma"} {
		_, code := createCheck(t, ts, map[string]any{"name": name})
		codes[name] = code
	}

	if res := ts.do(t, "GET", "/api/v3/checks/?orderby=最近", ""); res.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown orderby = %d, want 400", res.StatusCode)
	}

	tests := []struct {
		query string
		want  string
	}{
		{"", "gamma,alpha,beta"},                 // 默认按建档时间倒序
		{"?orderby=created", "beta,alpha,gamma"}, // 建档时间正序
		{"?orderby=name", "alpha,beta,gamma"},
		{"?orderby=-name", "gamma,beta,alpha"},
	}
	for _, tt := range tests {
		doc := decode(t, ts.do(t, "GET", "/api/v3/checks/"+tt.query, ""))
		if got := strings.Join(checkNames(doc), ","); got != tt.want {
			t.Errorf("checks%s = %v, want %s", tt.query, got, tt.want)
		}
	}

	// 最后 ping 排序：只有报过到的排得在前面。
	if res := ts.do(t, "GET", "/ping/"+codes["beta"], ""); res.StatusCode != http.StatusOK {
		t.Fatalf("ping = %d", res.StatusCode)
	}
	doc := decode(t, ts.do(t, "GET", "/api/v3/checks/?orderby=-last_ping", ""))
	if got := checkNames(doc)[0]; got != "beta" {
		t.Errorf("-last_ping puts %q first, want beta", got)
	}
}

func TestPingsEndpointAndBody(t *testing.T) {
	ts := newTestServer(t)
	_, code := createCheck(t, ts, map[string]any{"name": "body"})

	req := httptest.NewRequest("POST", "/ping/"+code, strings.NewReader("hello body"))
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ping = %d", rec.Code)
	}
	if got := rec.Header().Get("Ping-Body-Limit"); got == "" {
		t.Error("Ping-Body-Limit header missing")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS header = %q", got)
	}

	doc := decode(t, ts.do(t, "GET", "/api/v3/checks/"+code+"/pings/", ""))
	pings := doc["pings"].([]any)
	if len(pings) != 1 {
		t.Fatalf("pings = %d", len(pings))
	}
	p := pings[0].(map[string]any)
	if p["type"] != "success" {
		t.Errorf("type = %v", p["type"])
	}
	bodyURL, ok := p["body_url"].(string)
	if !ok || !strings.HasSuffix(bodyURL, "/pings/1/body") {
		t.Fatalf("body_url = %v", p["body_url"])
	}
	if !strings.Contains(p["date"].(string), ".") {
		t.Errorf("ping date = %q, want microseconds like Python isoformat", p["date"])
	}
	if p["remote_addr"] != "192.0.2.1" {
		t.Errorf("remote_addr = %v, want the port stripped", p["remote_addr"])
	}

	resp := ts.do(t, "GET", strings.TrimPrefix(bodyURL, ts.cfg.SiteRoot), "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("body = %d", resp.StatusCode)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(raw) != "hello body" {
		t.Errorf("body = %q", raw)
	}
}

func TestFlipsEndpoint(t *testing.T) {
	ts := newTestServer(t)
	_, code := createCheck(t, ts, map[string]any{"name": "flips", "timeout": 3600, "grace": 60})

	ts.do(t, "GET", "/ping/"+code, "").Body.Close()
	ts.do(t, "GET", "/ping/"+code+"/fail", "").Body.Close()

	doc := decode(t, ts.do(t, "GET", "/api/v3/checks/"+code+"/flips/", ""))
	flips := doc["flips"].([]any)
	if len(flips) != 2 {
		t.Fatalf("flips = %d, want 2", len(flips))
	}
	// Newest first, matching order_by("-id").
	newest := flips[0].(map[string]any)
	if newest["up"].(float64) != 0 {
		t.Errorf("newest flip should be the down transition")
	}
	if newest["new_status"] != "down" || newest["old_status"] != "up" {
		t.Errorf("flip statuses = %v -> %v, want up -> down", newest["old_status"], newest["new_status"])
	}
	if newest["reason"] != "fail" {
		t.Errorf("down flip reason = %v, want fail", newest["reason"])
	}

	// A window that starts in the future is empty.
	future := time.Now().UTC().Add(time.Hour).Format("2006-01-02T15:04:05")
	doc = decode(t, ts.do(t, "GET", "/api/v3/checks/"+code+"/flips/?start="+future, ""))
	if len(doc["flips"].([]any)) != 0 {
		t.Errorf("future start should filter everything out: %v", doc["flips"])
	}

	resp := ts.do(t, "GET", "/api/v3/checks/"+code+"/flips/?seconds=nope", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad seconds = %d, want 400", resp.StatusCode)
	}
	resp = ts.do(t, "GET", "/api/v3/checks/"+code+"/flips/?seconds=0", "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("seconds=0 = %d, want 400", resp.StatusCode)
	}
}

// TestV1ExposesStarted pins the only behaviour that differs between versions.
func TestV1ExposesStarted(t *testing.T) {
	ts := newTestServer(t)
	_, code := createCheck(t, ts, map[string]any{"name": "run", "timeout": 3600, "grace": 60})
	ts.do(t, "GET", "/ping/"+code, "").Body.Close()
	ts.do(t, "GET", "/ping/"+code+"/start", "").Body.Close()

	v1 := decode(t, ts.do(t, "GET", "/api/v1/checks/"+code, ""))
	if v1["status"] != "started" {
		t.Errorf("v1 status = %v, want started", v1["status"])
	}
	v3 := decode(t, ts.do(t, "GET", "/api/v3/checks/"+code, ""))
	if v3["status"] == "started" {
		t.Error("v3 must not report started")
	}
	if v3["started"] != true {
		t.Errorf("v3 started flag = %v", v3["started"])
	}
}

func TestStatusAndChannelsEndpoints(t *testing.T) {
	ts := newTestServer(t)
	resp := ts.do(t, "GET", "/api/v3/status/", "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(body) != "OK" {
		t.Errorf("status = %d %q", resp.StatusCode, body)
	}

	created := decode(t, ts.do(t, "POST", "/front/channels", map[string]any{
		"name": "邮件", "urls": []string{"mailto://user:pass@gmail.com"},
	}))
	if created["code"] == "" {
		t.Fatalf("channel not created: %v", created)
	}

	doc := decode(t, ts.do(t, "GET", "/api/v3/channels/", ""))
	channels := doc["channels"].([]any)
	if len(channels) != 1 {
		t.Fatalf("channels = %d", len(channels))
	}
	ch := channels[0].(map[string]any)
	if ch["id"] != created["code"] || ch["kind"] != "apprise" || ch["name"] != "邮件" {
		t.Errorf("channel dict = %v", ch)
	}
}

func TestNotificationReportEndpoint(t *testing.T) {
	ts := newTestServer(t)

	// An unknown code must still be answered with 200, or the caller retries.
	req := httptest.NewRequest("POST", "/api/v3/notifications/00000000-0000-0000-0000-000000000000/status",
		strings.NewReader("error=delivery+failed"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("report = %d, want 200", rec.Code)
	}

	ctx := context.Background()
	project, _ := ts.db.Project(ctx)
	ch, err := ts.db.CreateChannel(ctx, &store.NewChannel{
		ProjectID: project.ID, Name: "x", URLs: []string{"json://127.0.0.1:1/hook"},
		Conf: store.ChannelConf{Up: true, Down: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := ts.db.CreateCheck(ctx, &store.NewCheck{
		ProjectID: project.ID, Name: "y", Kind: "simple",
		Timeout: time.Hour, Grace: time.Hour, TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	notif, err := ts.db.StartNotification(ctx, &c.ID, ch.ID, "down")
	if err != nil {
		t.Fatal(err)
	}

	req = httptest.NewRequest("POST", "/api/v3/notifications/"+notif.Code+"/status",
		strings.NewReader("error=bad+token&mark_disabled=1"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("report = %d", rec.Code)
	}

	updated, err := ts.db.ChannelByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Disabled || updated.LastError != "bad token" {
		t.Errorf("channel after report = %+v", updated)
	}
}

func TestPingRejections(t *testing.T) {
	ts := newTestServer(t)
	_, code := createCheck(t, ts, map[string]any{"name": "codes", "timeout": 3600})

	cases := []struct {
		path string
		want int
	}{
		{"/ping/00000000-0000-0000-0000-000000000000", http.StatusNotFound},
		{"/ping/" + code + "/300", http.StatusBadRequest},
		{"/ping/" + code + "/notanumber", http.StatusNotFound},
		{"/ping/" + code + "?rid=nope", http.StatusBadRequest},
		{"/ping/" + code + "/log", http.StatusOK},
	}
	for _, tc := range cases {
		resp := ts.do(t, "GET", tc.path, "")
		if resp.StatusCode != tc.want {
			t.Errorf("%s = %d, want %d", tc.path, resp.StatusCode, tc.want)
		}
		resp.Body.Close()
	}
}

// TestSuccessSuffixIsAliasOfBareURL pins that /success behaves exactly like the
// bare ping URL, including the recorded ping type.
func TestSuccessSuffixIsAliasOfBareURL(t *testing.T) {
	ts := newTestServer(t)
	_, a := createCheck(t, ts, map[string]any{"name": "bare", "timeout": 3600, "grace": 60})
	_, b := createCheck(t, ts, map[string]any{"name": "explicit", "timeout": 3600, "grace": 60})

	for _, u := range []string{"/ping/" + a, "/ping/" + b + "/success"} {
		if resp := ts.do(t, "GET", u, ""); resp.StatusCode != http.StatusOK {
			t.Fatalf("%s = %d, want 200", u, resp.StatusCode)
		}
	}

	docs := decode(t, ts.do(t, "GET", "/api/v3/checks/"+a, ""))
	docE := decode(t, ts.do(t, "GET", "/api/v3/checks/"+b, ""))
	if docs["status"] != "up" || docE["status"] != "up" {
		t.Errorf("statuses = %v / %v, want both up", docs["status"], docE["status"])
	}

	for _, uuid := range []string{a, b} {
		pings := decode(t, ts.do(t, "GET", "/api/v3/checks/"+uuid+"/pings/", ""))["pings"].([]any)
		if len(pings) != 1 {
			t.Fatalf("%s pings = %d", uuid, len(pings))
		}
		if typ := pings[0].(map[string]any)["type"]; typ != "success" {
			t.Errorf("%s recorded type = %v, want success", uuid, typ)
		}
	}

	// /success on a down check recovers it, same as the bare URL.
	ts.do(t, "GET", "/ping/"+b+"/fail", "").Body.Close()
	ts.do(t, "GET", "/ping/"+b+"/success", "").Body.Close()
	doc := decode(t, ts.do(t, "GET", "/api/v3/checks/"+b, ""))
	if doc["status"] != "up" {
		t.Errorf("after fail then /success: status = %v, want up", doc["status"])
	}
}

func TestFailureTolerance(t *testing.T) {
	ts := newTestServer(t)
	doc, code := createCheck(t, ts, map[string]any{
		"name": "容忍失败", "timeout": 3600, "grace": 60, "failure_tolerance": 2,
	})
	if doc["failure_tolerance"] != float64(2) {
		t.Fatalf("create echoes tolerance = %v", doc["failure_tolerance"])
	}

	// Two consecutive failure signals are absorbed; the check stays up and
	// the running count is exposed while nonzero.
	ts.do(t, "GET", "/ping/"+code+"/fail", "").Body.Close()
	ts.do(t, "GET", "/ping/"+code+"/fail", "").Body.Close()
	doc = decode(t, ts.do(t, "GET", "/api/v3/checks/"+code, ""))
	if doc["status"] != "up" {
		t.Fatalf("status after 2 fails = %v, want up", doc["status"])
	}
	if doc["failure_count"] != float64(2) {
		t.Fatalf("failure_count = %v, want 2", doc["failure_count"])
	}

	// The third failure flips the check down.
	ts.do(t, "GET", "/ping/"+code+"/fail", "").Body.Close()
	doc = decode(t, ts.do(t, "GET", "/api/v3/checks/"+code, ""))
	if doc["status"] != "down" {
		t.Fatalf("status after 3 fails = %v, want down", doc["status"])
	}

	// Updating the tolerance round-trips; out-of-range values are rejected.
	resp := ts.do(t, "POST", "/api/v3/checks/"+code, map[string]any{"failure_tolerance": 5})
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("update = %d %s", resp.StatusCode, body)
	}
	resp.Body.Close()
	if resp := ts.do(t, "POST", "/api/v3/checks/"+code, map[string]any{"failure_tolerance": 999}); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("tolerance 999 = %d, want 400", resp.StatusCode)
		resp.Body.Close()
	}
}

func TestMetaFlapSettings(t *testing.T) {
	ts := newTestServer(t)
	doc := decode(t, ts.do(t, "GET", "/front/meta", ""))
	if doc["flap_threshold"] != float64(ts.cfg.FlapThreshold) {
		t.Errorf("flap_threshold = %v", doc["flap_threshold"])
	}
	if doc["flap_window_seconds"] != float64(int(ts.cfg.FlapWindow.Seconds())) {
		t.Errorf("flap_window_seconds = %v", doc["flap_window_seconds"])
	}
}
