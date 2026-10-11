package server

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/uuid"

	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/store"
	"github.com/yufei/feixi/internal/web"
)

func TestAPIChannelSelection(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	one, err := ts.db.CreateChannel(ctx, &store.NewChannel{
		ProjectID: ts.projectID, Name: "甲", URLs: []string{"json://one"},
	})
	if err != nil {
		t.Fatal(err)
	}
	two, err := ts.db.CreateChannel(ctx, &store.NewChannel{
		ProjectID: ts.projectID, Name: "乙", URLs: []string{"json://two"},
	})
	if err != nil {
		t.Fatal(err)
	}

	// 不写 channels 时默认订阅项目下全部渠道；字典里是逗号拼接的 code。
	doc, code := createCheck(t, ts, map[string]any{"name": "默认"})
	if got := sortedCodes(doc["channels"]); got != sortedCodes(one.Code+","+two.Code) {
		t.Errorf("fresh check channels = %v, want both codes", got)
	}

	list := decode(t, ts.do(t, "GET", "/api/v3/channels/", nil))
	rows, ok := list["channels"].([]any)
	if !ok || len(rows) != 2 {
		t.Fatalf("channels = %v", list)
	}
	first := rows[0].(map[string]any)
	if first["id"] != one.Code || first["name"] != "甲" || first["kind"] != "apprise" {
		t.Errorf("channel row = %v", first)
	}

	// "*" 订阅项目下所有渠道。
	doc, _ = createCheck(t, ts, map[string]any{"name": "全部", "channels": "*"})
	if got := sortedCodes(doc["channels"]); got != sortedCodes(one.Code+","+two.Code) {
		t.Errorf("channels=* = %v, want both codes", doc["channels"])
	}

	// 空串是显式的「退订」。
	doc, _ = createCheck(t, ts, map[string]any{"name": "退订", "channels": ""})
	if doc["channels"] != "" {
		t.Errorf("channels=\"\" = %v, want none", doc["channels"])
	}

	// 逗号或空格分隔的 code 列表。
	doc, _ = createCheck(t, ts, map[string]any{"name": "两个", "channels": one.Code + "," + two.Code})
	if got := sortedCodes(doc["channels"]); got != sortedCodes(one.Code+","+two.Code) {
		t.Errorf("explicit list = %v", doc["channels"])
	}

	doc, _ = createCheck(t, ts, map[string]any{"name": "空格分隔", "channels": one.Code + " " + two.Code})
	if got := sortedCodes(doc["channels"]); got != sortedCodes(one.Code+","+two.Code) {
		t.Errorf("space separated list = %v", doc["channels"])
	}

	if res := ts.do(t, "POST", "/api/v3/checks/", map[string]any{
		"name": "坏渠道", "channels": "not-a-code",
	}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown channel = %d", res.StatusCode)
	} else if body := read(t, res); !strings.Contains(body, "not-a-code") {
		t.Errorf("body = %q, want it to name the offending code", body)
	}

	// 更新订阅：换成单个渠道。
	res := ts.do(t, "POST", "/api/v3/checks/"+code+"/", map[string]any{"channels": one.Code})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update channels = %d, body %s", res.StatusCode, read(t, res))
	}
	if got := decode(t, res)["channels"]; got != one.Code {
		t.Errorf("after update = %v, want only %s", got, one.Code)
	}

	// 渠道非法时整次更新作废，连名字都不该改。
	res = ts.do(t, "POST", "/api/v3/checks/"+code+"/", map[string]any{"name": "改名", "channels": "ghost"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad channel on update = %d", res.StatusCode)
	}
	after := decode(t, ts.do(t, "GET", "/api/v3/checks/"+code+"/", nil))
	if after["name"] == "改名" {
		t.Error("a rejected channel list must roll the whole spec back")
	}

	// 别的项目的渠道不能订阅。
	other := insertOtherProject(t, ts)
	foreign, err := ts.db.CreateChannel(ctx, &store.NewChannel{
		ProjectID: other, Name: "外项目", URLs: []string{"json://x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	res = ts.do(t, "POST", "/api/v3/checks/", map[string]any{"name": "串项目", "channels": foreign.Code})
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("foreign channel = %d, want 400", res.StatusCode)
	}
	if body := read(t, res); !strings.Contains(body, "不属于这个项目") {
		t.Errorf("body = %q", body)
	}
}

func sortedCodes(v any) string {
	s, _ := v.(string)
	parts := strings.Split(s, ",")
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// insertOtherProject adds a second project row: the single-tenant Seed only
// maintains the default one, but cross-tenant access is what these guards are for.
func insertOtherProject(t *testing.T, ts *testServer) int64 {
	t.Helper()
	res, err := ts.db.Write.ExecContext(context.Background(),
		`INSERT INTO projects(code, name, badge_key, ping_key, show_slugs, created) VALUES(?,?,?,?,0,?)`,
		uuid.NewString(), "别的项目", uuid.NewString(), uuid.NewString(), time.Now().UTC().UnixMicro())
	if err != nil {
		t.Fatal(err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestFindCheckRejectsOtherProjects(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()
	other := insertOtherProject(t, ts)

	foreign, err := ts.db.CreateCheck(ctx, &store.NewCheck{
		ProjectID: other, Name: "外项目检查项", Kind: engine.KindSimple,
		Timeout: engine.DefaultTimeout, Grace: engine.DefaultGrace, TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}

	for _, req := range []struct{ method, path string }{
		{"GET", "/api/v3/checks/" + foreign.Code + "/"},
		{"POST", "/api/v3/checks/" + foreign.Code + "/"},
		{"DELETE", "/api/v3/checks/" + foreign.Code + "/"},
		{"POST", "/api/v3/checks/" + foreign.Code + "/pause"},
		{"POST", "/api/v3/checks/" + foreign.Code + "/resume"},
		{"GET", "/api/v3/checks/" + foreign.Code + "/pings/"},
		{"GET", "/api/v3/checks/" + foreign.Code + "/flips/"},
		{"GET", "/api/v3/checks/" + foreign.Code + "/pings/1/body"},
	} {
		res := ts.do(t, req.method, req.path, nil)
		if res.StatusCode != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", req.method, req.path, res.StatusCode)
		}
	}

	if res := ts.do(t, "GET", "/api/v3/checks/"+uuid.NewString()+"/", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown code = %d, want 404", res.StatusCode)
	}
	for _, path := range []string{"/pause", "/resume", "/pings/", "/flips/"} {
		res := ts.do(t, "POST", "/api/v3/checks/no-such-code"+path, nil)
		if res.StatusCode != http.StatusNotFound && res.StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 404", path, res.StatusCode)
		}
	}
}

func TestAPIUpsertByUnique(t *testing.T) {
	tests := []struct {
		name string
		// seed 是已存在检查项的字段，spec 是带着 unique 的 POST。
		seed map[string]any
		spec map[string]any
	}{
		{
			name: "name",
			seed: map[string]any{"name": "任务"},
			spec: map[string]any{"unique": []string{"name"}, "name": "任务", "tags": "命中"},
		},
		{
			name: "slug",
			seed: map[string]any{"name": "任务", "slug": "nightly"},
			spec: map[string]any{"unique": []string{"slug"}, "name": "改名", "slug": "nightly"},
		},
		{
			// tags 先归一化再比对，多余空格不该让匹配失效。
			name: "tags",
			seed: map[string]any{"name": "任务", "tags": "prod"},
			spec: map[string]any{"unique": []string{"tags"}, "tags": "  prod  ", "name": "改名"},
		},
		{
			name: "timeout",
			seed: map[string]any{"name": "任务", "timeout": 3600},
			spec: map[string]any{"unique": []string{"timeout"}, "timeout": 3600, "name": "改名"},
		},
		{
			name: "grace",
			seed: map[string]any{"name": "任务", "grace": 120},
			spec: map[string]any{"unique": []string{"grace"}, "grace": 120, "name": "改名"},
		},
		{
			name: "combined",
			seed: map[string]any{"name": "任务", "slug": "nightly"},
			spec: map[string]any{"unique": []string{"name", "slug"}, "name": "任务", "slug": "nightly"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			_, code := createCheck(t, ts, tt.seed)

			res := ts.do(t, "POST", "/api/v3/checks/", tt.spec)
			if res.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, body %s", res.StatusCode, read(t, res))
			}
			doc := decode(t, res)
			if doc["uuid"] != code {
				t.Errorf("upsert returned %v, want the existing %s", doc["uuid"], code)
			}
			// 命中之后的字段更新同样生效。
			if doc["tags"] == nil {
				t.Errorf("upsert did not apply the spec: %v", doc)
			}

			list := decode(t, ts.do(t, "GET", "/api/v3/checks/", nil))["checks"].([]any)
			if len(list) != 1 {
				t.Errorf("upsert created a second check: %d rows", len(list))
			}
		})
	}

	ts := newTestServer(t)
	createCheck(t, ts, map[string]any{"name": "任务", "slug": "nightly"})

	// 不带 unique 时同样的正文只会新建。
	if res := ts.do(t, "POST", "/api/v3/checks/", map[string]any{"name": "任务"}); res.StatusCode != http.StatusCreated {
		t.Errorf("without unique = %d, want 201", res.StatusCode)
	}
	// unique 字段名不在白名单里直接 400。
	if res := ts.do(t, "POST", "/api/v3/checks/", map[string]any{"unique": []string{"desc"}, "name": "任务"}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("unsupported unique = %d", res.StatusCode)
	}
	// 匹配不上时照常创建。
	if res := ts.do(t, "POST", "/api/v3/checks/", map[string]any{"unique": []string{"slug"}, "slug": "brand-new", "name": "新建"}); res.StatusCode != http.StatusCreated {
		t.Errorf("non-matching unique = %d, want 201", res.StatusCode)
	}
	// 任一字段对不上就不算命中，走新建。
	if res := ts.do(t, "POST", "/api/v3/checks/", map[string]any{
		"unique": []string{"name", "slug"}, "name": "别的名字", "slug": "other",
	}); res.StatusCode != http.StatusCreated {
		t.Errorf("partially matching unique = %d, want 201: %s", res.StatusCode, read(t, res))
	}
}

func TestAPIMaxChecks(t *testing.T) {
	ts := newTestServer(t)
	ts.db.MaxChecks = 1

	createCheck(t, ts, map[string]any{"name": "占位"})

	res := ts.do(t, "POST", "/api/v3/checks/", map[string]any{"name": "超限"})
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("over the cap = %d, want 403", res.StatusCode)
	}
	if body := read(t, res); !strings.Contains(body, "上限") {
		t.Errorf("body = %q", body)
	}
}

func TestAPIUpdateRejectsBrokenScheduleThroughDeadline(t *testing.T) {
	ts := newTestServer(t)
	ctx := context.Background()

	_, code := createCheck(t, ts, map[string]any{"name": "定时", "schedule": "0 3 * * *", "tz": "UTC"})
	pingCode(t, ts, code, "success")

	// 直接把库里的时区改坏，模拟历史脏数据：到期时间算不出来时必须报错而不是静默保存。
	if _, err := ts.db.Write.ExecContext(ctx, `UPDATE checks SET tz = 'Mars/Olympus' WHERE code = ?`, code); err != nil {
		t.Fatal(err)
	}

	res := ts.do(t, "POST", "/api/v3/checks/"+code+"/", map[string]any{"name": "改名"})
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("broken zone = %d, body %s", res.StatusCode, read(t, res))
	}
	body := read(t, res)
	if !strings.Contains(body, "JSON 验证错误") {
		t.Errorf("body = %q", body)
	}

	after := decode(t, ts.do(t, "GET", "/api/v3/checks/"+code+"/", nil))
	if after["name"] == "改名" {
		t.Error("a failed deadline recomputation must not save the new name")
	}
}

func TestAPIPingBodyEndpoint(t *testing.T) {
	ts := newTestServer(t)
	_, code := createCheck(t, ts, map[string]any{"name": "任务"})

	// 没有正文的 ping 取不到 body。
	pingCode(t, ts, code, "success")
	res := ts.do(t, "GET", "/api/v3/checks/"+code+"/pings/1/body", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("empty body = %d, want 404", res.StatusCode)
	}

	res = ts.do(t, "POST", "/ping/"+code, "payload")
	if res.StatusCode != http.StatusOK {
		t.Fatalf("ping with body = %d", res.StatusCode)
	}
	res = ts.do(t, "GET", "/api/v3/checks/"+code+"/pings/2/body", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("body = %d, want 200", res.StatusCode)
	}
	if got := read(t, res); got != "payload" {
		t.Errorf("body = %q", got)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type = %q", ct)
	}

	// n 不是数字、或已经被剪掉，一律 404。
	for _, bad := range []string{"abc", "0", "-1", "9999"} {
		if res := ts.do(t, "GET", "/api/v3/checks/"+code+"/pings/"+bad+"/body", nil); res.StatusCode != http.StatusNotFound {
			t.Errorf("n=%s = %d, want 404", bad, res.StatusCode)
		}
	}

	// 超出保留窗口（n <= npings - ping_log_limit）的历史 ping 不再提供。
	ts.cfg.PingLogLimit = 1
	if res := ts.do(t, "GET", "/api/v3/checks/"+code+"/pings/1/body", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("pruned n = %d, want 404", res.StatusCode)
	}
	ts.cfg.PingLogLimit = 100
}

func TestAPIPauseResumeEdges(t *testing.T) {
	ts := newTestServer(t)
	_, code := createCheck(t, ts, map[string]any{"name": "任务"})
	base := "/api/v3/checks/" + code

	// 未暂停时 resume 是 409。
	if res := ts.do(t, "POST", base+"/resume", nil); res.StatusCode != http.StatusConflict {
		t.Errorf("resume while up = %d, want 409", res.StatusCode)
	}

	paused := decode(t, ts.do(t, "POST", base+"/pause", nil))
	if paused["status"] != "paused" {
		t.Errorf("pause = %v", paused["status"])
	}
	// 重复 pause 直接回现状，不再写库。
	again := decode(t, ts.do(t, "POST", base+"/pause", nil))
	if again["status"] != "paused" {
		t.Errorf("second pause = %v", again["status"])
	}
	resumed := decode(t, ts.do(t, "POST", base+"/resume", nil))
	if resumed["status"] == "paused" {
		t.Errorf("resume = %v", resumed["status"])
	}

	// 删除走的是改名再删，删完 404。
	if res := ts.do(t, "DELETE", base+"/", nil); res.StatusCode != http.StatusNoContent {
		t.Errorf("delete = %d", res.StatusCode)
	}
	if res := ts.do(t, "GET", base+"/", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("get after delete = %d", res.StatusCode)
	}
}

func TestSPAStatic(t *testing.T) {
	ts := newTestServer(t)

	files, err := web.FS()
	if err != nil {
		t.Skipf("前端未构建：%v", err)
	}
	// 只有占位 .gitkeep 的 dist 也能 embed 成功，但没有任何可服务的产物。
	if _, err := fs.ReadFile(files, "index.html"); err != nil {
		t.Skip("前端未构建：dist 里只有占位文件")
	}
	asset := firstAsset(t, files)

	res := ts.do(t, "GET", "/", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("index = %d, body %s", res.StatusCode, read(t, res))
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("index content-type = %q", ct)
	}
	if res.Header.Get("Cache-Control") != "no-cache" {
		t.Errorf("index should stay revalidatable, got %q", res.Header.Get("Cache-Control"))
	}

	res = ts.do(t, "GET", "/assets/"+asset, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("asset = %d", res.StatusCode)
	}
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("hashed assets should be immutable, got %q", cc)
	}

	// 前端路由要落回 index.html，而不是 404。
	res = ts.do(t, "GET", "/checks/abc123", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("spa route = %d", res.StatusCode)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("spa route content-type = %q", ct)
	}

	// 带扩展名的缺失文件不再吐壳页：manifest 被 HTML 顶替时 PWA 会静默失效。
	res = ts.do(t, "GET", "/assets/definitely-missing.js", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("missing asset = %d, want 404", res.StatusCode)
	}

	for _, tt := range []struct{ path, wantType string }{
		{"/manifest.webmanifest", "application/manifest+json"},
		{"/icon-192.png", "image/png"},
		{"/favicon.ico", "image/vnd.microsoft.icon"},
		{"/favicon.svg", "image/svg+xml"},
	} {
		res = ts.do(t, "GET", tt.path, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s = %d, want 200（public/ 下的资产没进 dist？）", tt.path, res.StatusCode)
			continue
		}
		if ct := res.Header.Get("Content-Type"); ct != tt.wantType {
			t.Errorf("%s content-type = %q, want %q", tt.path, ct, tt.wantType)
		}
	}

	// 真实 check 的 ID 要能落回壳页：一旦 ID 格式里冒出点号，上面那条
	// 「带扩展名不回落」的规则会先把这里打红，而不是线上一片 404。
	_, checkID := createCheck(t, ts, map[string]any{"name": "路由"})
	res = ts.do(t, "GET", "/checks/"+checkID, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("check route = %d, want 200", res.StatusCode)
	}
}

func TestContentType(t *testing.T) {
	tests := []struct{ name, want string }{
		{"index.html", "text/html; charset=utf-8"},
		{"app.js", "text/javascript; charset=utf-8"},
		{"site.css", "text/css; charset=utf-8"},
		{"logo.svg", "image/svg+xml"},
		{"manifest.json", "application/json"},
		{"manifest.webmanifest", "application/manifest+json"},
		{"icon-512.png", "image/png"},
		{"apple-touch-icon.png", "image/png"},
		{"favicon.ico", "image/vnd.microsoft.icon"},
		{"data.bin", "application/octet-stream"},
		{"no-extension", "application/octet-stream"},
	}
	for _, tt := range tests {
		if got := contentType(tt.name); got != tt.want {
			t.Errorf("contentType(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestServeStaticRejectsTraversal(t *testing.T) {
	ts := newTestServer(t)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/../../etc/passwd", nil)
	ts.handler.ServeHTTP(rec, req)
	// path.Clean 之后要么回落到 index.html，要么 503，绝不能把仓库外的文件发出去。
	if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "root:") {
		t.Error("leaked a passwd-shaped body")
	}
}

// TestServeStaticAssetRules 用内存 FS 验静态规则，不依赖有没有跑过 make web。
func TestServeStaticAssetRules(t *testing.T) {
	files := fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html>壳")},
		"manifest.webmanifest": {Data: []byte(`{"name":"飞檄"}`)},
		"assets/index-ABCD.js": {Data: []byte("console.log(1)")},
	}

	tests := []struct {
		path   string
		status int
		ctype  string
		cache  string
		body   string
	}{
		{"/", http.StatusOK, "text/html; charset=utf-8", "no-cache", "壳"},
		{"/checks/9f2c1a", http.StatusOK, "text/html; charset=utf-8", "no-cache", "壳"},
		{"/gone/page.html", http.StatusOK, "text/html; charset=utf-8", "no-cache", "壳"},
		{"/manifest.webmanifest", http.StatusOK, "application/manifest+json", "no-cache", "飞檄"},
		{"/assets/index-ABCD.js", http.StatusOK, "text/javascript; charset=utf-8", "immutable", "console"},
		{"/manifest-typo.webmanifest", http.StatusNotFound, "text/plain; charset=utf-8", "", ""},
		{"/icon-192.png", http.StatusNotFound, "text/plain; charset=utf-8", "", ""},
		{"/wp-login.php", http.StatusNotFound, "text/plain; charset=utf-8", "", ""},
		{"/assets/missing-ABCD.js", http.StatusNotFound, "text/plain; charset=utf-8", "", ""},
	}
	for _, tt := range tests {
		rec := httptest.NewRecorder()
		serveStatic(rec, httptest.NewRequest("GET", tt.path, nil), files)

		if rec.Code != tt.status {
			t.Errorf("%s status = %d, want %d", tt.path, rec.Code, tt.status)
		}
		if ct := rec.Header().Get("Content-Type"); ct != tt.ctype {
			t.Errorf("%s content-type = %q, want %q", tt.path, ct, tt.ctype)
		}
		if tt.cache != "" && !strings.Contains(rec.Header().Get("Cache-Control"), tt.cache) {
			t.Errorf("%s cache-control = %q, want %q", tt.path, rec.Header().Get("Cache-Control"), tt.cache)
		}
		if tt.body != "" && !strings.Contains(rec.Body.String(), tt.body) {
			t.Errorf("%s body = %q, want it to contain %q", tt.path, rec.Body.String(), tt.body)
		}
	}
}

// firstAsset picks any file under assets/ so the cache header branch is real.
func firstAsset(t *testing.T, files fs.FS) string {
	t.Helper()
	var name string
	err := fs.WalkDir(files, "assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			name = strings.TrimPrefix(path, "assets/")
			return fs.SkipAll
		}
		return nil
	})
	if err != nil || name == "" {
		t.Skip("dist/assets 里没有产物")
	}
	return name
}
