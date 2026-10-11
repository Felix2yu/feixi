package server

import (
	"context"
	"errors"
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
	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/store"
)

// fakeNotifier 实现 Tester/Resender，把外呼挡在测试之外。
type fakeNotifier struct {
	testErr  error
	retryErr error
	tested   []*store.Channel
	retried  []*store.Notification
}

func (f *fakeNotifier) TestMessage(ch *store.Channel, c *engine.Check, status engine.Status) error {
	f.tested = append(f.tested, ch)
	return f.testErr
}

func (f *fakeNotifier) RetryNotification(ctx context.Context, n *store.Notification) error {
	f.retried = append(f.retried, n)
	return f.retryErr
}

func newFrontServer(t *testing.T, notifier *fakeNotifier) (*testServer, *fakeNotifier) {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "front.db"))
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

	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	project, err := db.Seed(context.Background(), "test-secret")
	if err != nil {
		t.Fatal(err)
	}

	d := daemon.New(db, cfg, slog.Default())
	var tester Tester
	var resender Resender
	if notifier != nil {
		tester, resender = notifier, notifier
	}
	srv := New(cfg, db, d, []string{"json", "mailto"}, tester, resender)
	return &testServer{handler: srv.Router, db: db, cfg: cfg, projectID: project.ID}, notifier
}

func pingCode(t *testing.T, ts *testServer, code, suffix string) {
	t.Helper()
	path := "/ping/" + code
	if suffix != "" {
		path += "/" + suffix
	}
	if res := ts.do(t, "GET", path, nil); res.StatusCode >= 400 {
		t.Fatalf("ping %s: %d %s", path, res.StatusCode, read(t, res))
	}
}

// setLastPing 直接把 last_ping 挪到过去，用来造出 grace/late 这类派生态。
func setLastPing(t *testing.T, ts *testServer, code string, at time.Time) {
	t.Helper()
	if _, err := ts.db.Write.ExecContext(context.Background(),
		`UPDATE checks SET last_ping = ? WHERE code = ?`, at.UnixMicro(), code); err != nil {
		t.Fatal(err)
	}
}

func TestBoundedInt(t *testing.T) {
	tests := []struct {
		raw       string
		want      int
		wantValid bool
	}{
		{"", 6, true},
		{"3", 3, true},
		{"12", 12, true},
		{"0", 6, false},
		{"13", 6, false},
		{"abc", 6, false},
		{"-1", 6, false},
		{"1.5", 6, false},
		{" 3", 6, false},
		{"999999999999", 6, false},
	}
	for _, tt := range tests {
		got, valid := boundedInt(tt.raw, 6, 1, 12)
		if got != tt.want || valid != tt.wantValid {
			t.Errorf("boundedInt(%q) = %d/%v, want %d/%v", tt.raw, got, valid, tt.want, tt.wantValid)
		}
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Nightly Backup", "nightly-backup"},
		{"  spaced   out  ", "--spaced---out--"},
		{"UPPER Case", "upper-case"},
		{"with_underscore-1", "with_underscore-1"},
		{"中文名字", ""},
		{"emoji 🎉 123", "emoji--123"},
		{"dots.don't survive!", "dotsdont-survive"},
	}
	for _, tt := range tests {
		if got := slugify(tt.in); got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	long := slugify(strings.Repeat("a", 120))
	if len(long) != 100 {
		t.Errorf("len = %d, want truncation at 100", len(long))
	}
}

func TestErrTextForUI(t *testing.T) {
	if got := errTextForUI(nil); got != "" {
		t.Errorf("nil error = %q", got)
	}
	if got := errTextForUI(errors.New("第一行\n第二行")); got != "第一行; 第二行" {
		t.Errorf("newlines should collapse to '; ', got %q", got)
	}
	long := errTextForUI(errors.New(strings.Repeat("问", 200)))
	// 300 是按字节截的，中文会被切在半路，这里只保证不再更长。
	if len(long) > 300 {
		t.Errorf("len = %d bytes, want at most 300", len(long))
	}
	if short := errTextForUI(errors.New("短")); short != "短" {
		t.Errorf("short messages should pass through, got %q", short)
	}
}

func TestCleanURLs(t *testing.T) {
	// 一行一个、逗号不分隔、空白丢弃。
	urls, err := cleanURLs([]string{"json://one\n\n  ", " json://two "})
	if err != nil {
		t.Fatal(err)
	}
	if len(urls) != 2 || urls[0] != "json://one" || urls[1] != "json://two" {
		t.Errorf("urls = %v", urls)
	}

	if _, err := cleanURLs(nil); err == nil || err.Error() != "至少要填一个 apprise URL" {
		t.Errorf("empty = %v", err)
	}
	if _, err := cleanURLs([]string{"   ", "\n"}); err == nil {
		t.Error("blank-only input should be rejected")
	}
	if _, err := cleanURLs([]string{"not-a-url"}); err == nil {
		t.Error("an unparseable apprise URL should be rejected")
	} else if !strings.Contains(err.Error(), "apprise URL 无法识别") {
		t.Errorf("err = %v", err)
	}
}

func TestChannelDictShape(t *testing.T) {
	now := time.Now().UTC()
	d := 500 * time.Millisecond
	ch := &store.Channel{
		Code: "abc", Name: "群", URLs: []string{"json://a"},
		Conf: store.ChannelConf{Up: true, Nag: true}, LastNotify: &now, LastNotifyDuration: &d,
		LastError: "boom",
	}
	out := toChannelDict(ch)
	if out.Kind != "apprise" || out.Code != "abc" || out.Name != "群" {
		t.Errorf("dict = %+v", out)
	}
	if !out.Up || out.Down || !out.Nag {
		t.Errorf("conf flags = %+v", out)
	}
	if out.LastNotify == nil || !strings.HasPrefix(*out.LastNotify, now.Format("2006-01-02T15:04:05")) {
		t.Errorf("last_notify = %v", out.LastNotify)
	}
	if out.LastNotifyDuration == nil || *out.LastNotifyDuration != 500 {
		t.Errorf("duration = %v, want milliseconds", out.LastNotifyDuration)
	}
	if out.LastError != "boom" {
		t.Errorf("last_error = %q", out.LastError)
	}

	blank := toChannelDict(&store.Channel{Conf: store.ChannelConf{}})
	if blank.LastNotify != nil || blank.LastNotifyDuration != nil {
		t.Errorf("empty channel = %+v", blank)
	}
}

func TestChannelEndpoints(t *testing.T) {
	ts, _ := newFrontServer(t, &fakeNotifier{})

	// 一开始是空的，而且必须是空数组而不是 null。
	res := ts.do(t, "GET", "/front/channels", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("list = %d", res.StatusCode)
	}
	doc := decode(t, res)
	list, ok := doc["channels"].([]any)
	if !ok || len(list) != 0 {
		t.Fatalf("channels = %v", doc["channels"])
	}

	// 缺 URL 直接 400。
	if res := ts.do(t, "POST", "/front/channels", map[string]any{"name": "空"}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("create without urls = %d", res.StatusCode)
	}
	// 认不出的 apprise URL 也要在表单阶段就报错。
	if res := ts.do(t, "POST", "/front/channels", map[string]any{"name": "坏", "urls": []string{"zzz://x"}}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("create with bad url = %d", res.StatusCode)
	}
	if res := ts.do(t, "POST", "/front/channels", "{"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("create with broken json = %d", res.StatusCode)
	}

	res = ts.do(t, "POST", "/front/channels", map[string]any{
		"name": "值班群", "urls": []string{"json://one\njson://two"}, "up": true, "down": false, "nag": true,
	})
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("create = %d, body %s", res.StatusCode, read(t, res))
	}
	created := decode(t, res)
	code, _ := created["code"].(string)
	if code == "" {
		t.Fatal("created channel has no code")
	}
	if created["up"] != true || created["down"] != false || created["nag"] != true {
		t.Errorf("conf flags not echoed: %v", created)
	}
	if urls, ok := created["urls"].([]any); !ok || len(urls) != 2 {
		t.Errorf("urls = %v", created["urls"])
	}

	list1 := decode(t, ts.do(t, "GET", "/front/channels", nil))["channels"]
	if arr, ok := list1.([]any); !ok || len(arr) != 1 {
		t.Errorf("list after create = %v", list1)
	}

	// 更新只改给出的字段。
	res = ts.do(t, "POST", "/front/channels/"+code, map[string]any{"name": "改名", "nag": false})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("update = %d, body %s", res.StatusCode, read(t, res))
	}
	updated := decode(t, res)
	if updated["name"] != "改名" {
		t.Errorf("name = %v", updated["name"])
	}
	if updated["nag"] != false {
		t.Errorf("nag = %v", updated["nag"])
	}
	if updated["up"] != true {
		t.Errorf("untouched flags should survive: %v", updated)
	}

	// 停用再启用会顺带清掉错误信息。
	after := decode(t, ts.do(t, "POST", "/front/channels/"+code, map[string]any{"disabled": true}))
	if after["disabled"] != true {
		t.Errorf("disabled = %v", after["disabled"])
	}
	after = decode(t, ts.do(t, "POST", "/front/channels/"+code, map[string]any{"disabled": false}))
	if after["disabled"] != false || after["last_error"] != "" {
		t.Errorf("re-enabled channel = %v", after)
	}

	// 换一批 URL，其中一条非法则整次更新作废。
	if res := ts.do(t, "POST", "/front/channels/"+code, map[string]any{"urls": []string{"json://fine", "bad"}}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("update with a bad url = %d", res.StatusCode)
	}
	after = decode(t, ts.do(t, "POST", "/front/channels/"+code, map[string]any{"urls": []string{"json://only"}}))
	if urls, ok := after["urls"].([]any); !ok || len(urls) != 1 || urls[0] != "json://only" {
		t.Errorf("replaced urls = %v", after["urls"])
	}

	if res := ts.do(t, "POST", "/front/channels/"+code, "{"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("update with broken json = %d", res.StatusCode)
	}
	if res := ts.do(t, "POST", "/front/channels/no-such-code", map[string]any{"name": "x"}); res.StatusCode != http.StatusNotFound {
		t.Errorf("update unknown = %d", res.StatusCode)
	}

	res = ts.do(t, "DELETE", "/front/channels/"+code, nil)
	if res.StatusCode != http.StatusNoContent {
		t.Errorf("delete = %d", res.StatusCode)
	}
	if res := ts.do(t, "DELETE", "/front/channels/"+code, nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("delete twice = %d", res.StatusCode)
	}
	empty := decode(t, ts.do(t, "GET", "/front/channels", nil))["channels"]
	if arr, ok := empty.([]any); !ok || len(arr) != 0 {
		t.Errorf("list after delete = %v", empty)
	}
}

func TestTestChannelEndpoint(t *testing.T) {
	ts, notifier := newFrontServer(t, &fakeNotifier{})
	created := decode(t, ts.do(t, "POST", "/front/channels", map[string]any{
		"name": "群", "urls": []string{"json://one"},
	}))
	code, _ := created["code"].(string)
	path := "/front/channels/" + code + "/test"

	// 没有真实检查项时用占位的「测试渠道」做主体。
	res := ts.do(t, "POST", path, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("test = %d, body %s", res.StatusCode, read(t, res))
	}
	if len(notifier.tested) != 1 {
		t.Errorf("tested %d channels", len(notifier.tested))
	}

	// 有检查项时借用第一条。
	createCheck(t, ts, map[string]any{"name": "夜间任务"})
	res = ts.do(t, "POST", path, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("test with a check = %d", res.StatusCode)
	}

	if res := ts.do(t, "POST", "/front/channels/nope/test", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("test unknown channel = %d", res.StatusCode)
	}

	notifier.testErr = errors.New("对端拒绝")
	res = ts.do(t, "POST", path, nil)
	if res.StatusCode != http.StatusBadGateway {
		t.Errorf("failing probe = %d", res.StatusCode)
	}
	if body := read(t, res); !strings.Contains(body, "对端拒绝") {
		t.Errorf("body = %q", body)
	}

	// 没接通知器时是 503，而不是假装成功。
	bare, _ := newFrontServer(t, nil)
	created = decode(t, bare.do(t, "POST", "/front/channels", map[string]any{
		"name": "群", "urls": []string{"json://one"},
	}))
	res = bare.do(t, "POST", "/front/channels/"+created["code"].(string)+"/test", nil)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("without a tester = %d", res.StatusCode)
	}
}

func TestProjectEndpoint(t *testing.T) {
	ts, _ := newFrontServer(t, nil)
	res := ts.do(t, "GET", "/front/project", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	doc := decode(t, res)
	if doc["name"] != "默认项目" {
		t.Errorf("name = %v", doc["name"])
	}
	for _, key := range []string{"badge_key", "ping_key"} {
		if v, ok := doc[key].(string); !ok || v == "" {
			t.Errorf("%s missing from %v", key, doc)
		}
	}
	createCheck(t, ts, map[string]any{"name": "一个"})
	createCheck(t, ts, map[string]any{"name": "两个"})
	doc = decode(t, ts.do(t, "GET", "/front/project", nil))
	if doc["check_count"] != float64(2) {
		t.Errorf("check_count = %v", doc["check_count"])
	}
	if doc["max_checks"] != float64(ts.cfg.MaxChecks) || doc["ping_body_limit"] != float64(ts.cfg.PingBodyLimit) {
		t.Errorf("limits = %v", doc)
	}
	if doc["site_root"] != "http://example.test" || doc["master_badge_label"] != "飞檄" {
		t.Errorf("presentation fields = %v", doc)
	}
}

func TestFrontStatusEndpoint(t *testing.T) {
	ts, _ := newFrontServer(t, nil)

	_, upCode := createCheck(t, ts, map[string]any{"name": "在线", "tags": "prod web"})
	_, downCode := createCheck(t, ts, map[string]any{"name": "宕了", "tags": "prod db"})
	_, freshCode := createCheck(t, ts, map[string]any{"name": "新的", "tags": "staging"})

	pingCode(t, ts, upCode, "success")
	pingCode(t, ts, downCode, "fail")
	// start 只标记 started，状态仍是 new；标签取的是组内最差状态。
	pingCode(t, ts, freshCode, "start")

	res := ts.do(t, "GET", "/front/status", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if res.Header.Get("ETag") == "" {
		t.Error("missing ETag")
	}
	if res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("cache-control = %q", res.Header.Get("Cache-Control"))
	}
	doc := decode(t, res)

	details, ok := doc["details"].([]any)
	if !ok || len(details) != 3 {
		t.Fatalf("details = %v", doc["details"])
	}
	byCode := map[string]map[string]any{}
	for _, raw := range details {
		row := raw.(map[string]any)
		byCode[row["code"].(string)] = row
	}
	if byCode[upCode]["status"] != "up" {
		t.Errorf("up row = %v", byCode[upCode])
	}
	if byCode[downCode]["status"] != "down" {
		t.Errorf("down row = %v", byCode[downCode])
	}
	if byCode[upCode]["last_ping"] == nil {
		t.Error("pinged check should report last_ping")
	}
	if byCode[freshCode]["started"] != true {
		t.Errorf("start ping should mark started: %v", byCode[freshCode])
	}
	// start 之后 GraceStart 会退回 last_start，所以 next_ping 有值。
	if byCode[freshCode]["next_ping"] == nil {
		t.Errorf("a started check should report next_ping: %v", byCode[freshCode])
	}
	if byCode[upCode]["next_ping"] == nil {
		t.Errorf("an up simple check has a deadline: %v", byCode[upCode])
	}

	if doc["title"] != "1 宕机" {
		t.Errorf("title = %v, want the down count", doc["title"])
	}
	tags := doc["tags"].(map[string]any)
	// prod 同时挂在在线和宕了的检查项上，标签取最差状态。
	if tags["prod"] != "down" {
		t.Errorf("prod tag = %v", tags["prod"])
	}
	if tags["web"] != "up" || tags["db"] != "down" {
		t.Errorf("tags = %v", tags)
	}

	// 同一份数据带 If-None-Match 回来就是 304，不再序列化正文。
	req := httptest.NewRequest("GET", "/front/status", nil)
	req.Header.Set("If-None-Match", res.Header.Get("ETag"))
	rec := httptest.NewRecorder()
	ts.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Errorf("conditional GET = %d, want 304", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 must have no body, got %q", rec.Body.String())
	}

	// 只有一项处于 grace（延迟）时标题换成延迟数。
	grace, _ := newFrontServer(t, nil)
	_, code := createCheck(t, grace, map[string]any{"name": "迟到了", "timeout": 60, "grace": 60})
	pingCode(t, grace, code, "success")
	setLastPing(t, grace, code, time.Now().UTC().Add(-90*time.Second))
	doc = decode(t, grace.do(t, "GET", "/front/status", nil))
	if doc["title"] != "1 延迟" {
		t.Errorf("title = %v, want the grace count; rows=%v", doc["title"], doc["details"])
	}

	// 全绿时回到实例名。
	quiet, _ := newFrontServer(t, nil)
	_, quietCode := createCheck(t, quiet, map[string]any{"name": "安静"})
	pingCode(t, quiet, quietCode, "success")
	doc = decode(t, quiet.do(t, "GET", "/front/status", nil))
	if doc["title"] != "飞檄" {
		t.Errorf("title = %v, want the instance name", doc["title"])
	}
}

func TestFrontDowntimesEndpoint(t *testing.T) {
	ts, _ := newFrontServer(t, nil)
	_, code := createCheck(t, ts, map[string]any{"name": "任务"})
	pingCode(t, ts, code, "fail")
	pingCode(t, ts, code, "success")

	res := ts.do(t, "GET", "/front/checks/"+code+"/downtimes", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("downtimes = %d, body %s", res.StatusCode, read(t, res))
	}
	doc := decode(t, res)
	if doc["tz"] != "UTC" {
		t.Errorf("tz = %v", doc["tz"])
	}
	records := doc["downtimes"].([]any)
	if len(records) != 6 {
		t.Fatalf("records = %d, want the default six months", len(records))
	}
	first := records[0].(map[string]any)
	for _, key := range []string{"boundary", "seconds", "count", "uptime", "no_data"} {
		if _, ok := first[key]; !ok {
			t.Errorf("record missing %s: %v", key, first)
		}
	}
	if first["no_data"] == true {
		t.Errorf("a check with history should have data: %v", first)
	}

	// 换成本地时区后 boundary 按本地日期分月。
	res = ts.do(t, "GET", "/front/checks/"+code+"/downtimes?tz=Asia/Shanghai&months=1", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("shanghai = %d", res.StatusCode)
	}
	doc = decode(t, res)
	if len(doc["downtimes"].([]any)) != 1 {
		t.Errorf("months=1 returned %d records", len(doc["downtimes"].([]any)))
	}

	for _, bad := range []string{"?months=0", "?months=13", "?months=abc", "?months=-1"} {
		if res := ts.do(t, "GET", "/front/checks/"+code+"/downtimes"+bad, nil); res.StatusCode != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, res.StatusCode)
		}
	}
	if res := ts.do(t, "GET", "/front/checks/"+code+"/downtimes?tz=Mars/Olympus", nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad tz = %d, want 400", res.StatusCode)
	}
	if res := ts.do(t, "GET", "/front/checks/no-such-code/downtimes", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown check = %d", res.StatusCode)
	}
}

func TestFrontNotificationsEndpoint(t *testing.T) {
	ts, _ := newFrontServer(t, &fakeNotifier{})

	// 通知记录由 daemon 生成，这里直接落库，只验证读取与序列化。
	ctx := context.Background()
	_, code := createCheck(t, ts, map[string]any{"name": "任务"})
	c, err := ts.db.CheckByCode(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	ch := decode(t, ts.do(t, "POST", "/front/channels", map[string]any{
		"name": "群", "urls": []string{"json://one"},
	}))
	channel, err := ts.db.ChannelByCode(ctx, ch["code"].(string))
	if err != nil {
		t.Fatal(err)
	}
	for _, status := range []engine.Status{engine.StatusDown, engine.StatusUp} {
		id := c.ID
		if _, err := ts.db.StartNotification(ctx, &id, channel.ID, status); err != nil {
			t.Fatal(err)
		}
	}

	res := ts.do(t, "GET", "/front/notifications", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("notifications = %d", res.StatusCode)
	}
	doc := decode(t, res)
	rows := doc["notifications"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	// 最新在前。
	newest := rows[0].(map[string]any)
	if newest["status"] != "up" {
		t.Errorf("newest = %v", newest)
	}
	if newest["check_name"] != "任务" || newest["channel_name"] != "群" {
		t.Errorf("joined names = %v", newest)
	}
	if newest["error"] != "Sending" {
		t.Errorf("error = %v", newest["error"])
	}
	if newest["created"] == nil {
		t.Error("created should be serialized")
	}
	if _, ok := newest["code"].(string); !ok {
		t.Errorf("code = %v", newest["code"])
	}

	// limit 越界一律回落到默认值，而不是报错。
	for _, q := range []string{"?limit=0", "?limit=abc", "?limit=99999"} {
		doc := decode(t, ts.do(t, "GET", "/front/notifications"+q, nil))
		if len(doc["notifications"].([]any)) != 2 {
			t.Errorf("%s limited the list to %v", q, doc["notifications"])
		}
	}
	doc = decode(t, ts.do(t, "GET", "/front/notifications?limit=1", nil))
	if len(doc["notifications"].([]any)) != 1 {
		t.Errorf("limit=1 returned %d rows", len(doc["notifications"].([]any)))
	}

	empty, _ := newFrontServer(t, &fakeNotifier{})
	doc = decode(t, empty.do(t, "GET", "/front/notifications", nil))
	if arr, ok := doc["notifications"].([]any); !ok || len(arr) != 0 {
		t.Errorf("empty list = %v", doc["notifications"])
	}
}

func TestRetryNotificationEndpoint(t *testing.T) {
	ts, notifier := newFrontServer(t, &fakeNotifier{})
	ctx := context.Background()

	_, code := createCheck(t, ts, map[string]any{"name": "任务"})
	c, err := ts.db.CheckByCode(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	ch := decode(t, ts.do(t, "POST", "/front/channels", map[string]any{
		"name": "群", "urls": []string{"json://one"},
	}))
	channel, err := ts.db.ChannelByCode(ctx, ch["code"].(string))
	if err != nil {
		t.Fatal(err)
	}
	id := c.ID
	note, err := ts.db.StartNotification(ctx, &id, channel.ID, engine.StatusDown)
	if err != nil {
		t.Fatal(err)
	}

	res := ts.do(t, "POST", "/front/notifications/"+note.Code+"/retry", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("retry = %d, body %s", res.StatusCode, read(t, res))
	}
	if len(notifier.retried) != 1 || notifier.retried[0].Code != note.Code {
		t.Errorf("retried = %v", notifier.retried)
	}

	notifier.retryErr = errors.New("渠道已删除")
	res = ts.do(t, "POST", "/front/notifications/"+note.Code+"/retry", nil)
	if res.StatusCode != http.StatusBadGateway {
		t.Errorf("failing retry = %d", res.StatusCode)
	}
	if body := read(t, res); !strings.Contains(body, "渠道已删除") {
		t.Errorf("body = %q", body)
	}

	if res := ts.do(t, "POST", "/front/notifications/no-such-code/retry", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown notice = %d", res.StatusCode)
	}

	bare, _ := newFrontServer(t, nil)
	res = bare.do(t, "POST", "/front/notifications/"+note.Code+"/retry", nil)
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("without a resender = %d", res.StatusCode)
	}
}

func TestSchedulePreviewEndpoint(t *testing.T) {
	ts, _ := newFrontServer(t, nil)

	res := ts.do(t, "POST", "/front/schedule/preview", map[string]any{"schedule": "*/15 * * * *"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("preview = %d, body %s", res.StatusCode, read(t, res))
	}
	doc := decode(t, res)
	next := doc["next"].([]any)
	if len(next) != 3 {
		t.Fatalf("next = %v, want three occurrences", next)
	}
	if doc["tz"] != "UTC" {
		t.Errorf("tz = %v, want the UTC default", doc["tz"])
	}
	for _, raw := range next {
		if _, err := time.Parse("2006-01-02 15:04:05 MST", raw.(string)); err != nil {
			t.Errorf("unparsable occurrence %q: %v", raw, err)
		}
	}

	res = ts.do(t, "POST", "/front/schedule/preview", map[string]any{
		"schedule": "0 9 * * *", "tz": "Asia/Shanghai",
	})
	doc = decode(t, res)
	if doc["tz"] != "Asia/Shanghai" {
		t.Errorf("tz = %v", doc["tz"])
	}
	if !strings.HasSuffix(doc["next"].([]any)[0].(string), " CST") {
		t.Errorf("occurrences should render in the requested zone: %v", doc["next"])
	}

	if res := ts.do(t, "POST", "/front/schedule/preview", "{"); res.StatusCode != http.StatusBadRequest {
		t.Errorf("broken json = %d", res.StatusCode)
	}
	for _, bad := range []string{"", "@every 5m", "not a cron", "* * * * * *"} {
		res := ts.do(t, "POST", "/front/schedule/preview", map[string]any{"schedule": bad})
		if res.StatusCode != http.StatusBadRequest {
			t.Errorf("schedule %q = %d, want 400", bad, res.StatusCode)
		}
	}
	if res := ts.do(t, "POST", "/front/schedule/preview", map[string]any{
		"schedule": "0 9 * * *", "tz": "Mars/Olympus",
	}); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad tz = %d", res.StatusCode)
	}

	// 合法的 cron 但永不发生（2 月 30 日）：不报错，返回空的 next。
	res = ts.do(t, "POST", "/front/schedule/preview", map[string]any{"schedule": "0 0 30 2 *"})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("never-firing schedule = %d, body %s", res.StatusCode, read(t, res))
	}
	doc = decode(t, res)
	if got := len(doc["next"].([]any)); got != 0 {
		t.Errorf("next = %d entries, want none", got)
	}
}

func TestClearEventsEndpoint(t *testing.T) {
	ts, _ := newFrontServer(t, &fakeNotifier{})
	ctx := context.Background()

	_, code := createCheck(t, ts, map[string]any{"name": "任务"})
	pingCode(t, ts, code, "")

	c, err := ts.db.CheckByCode(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	if res := ts.do(t, "POST", "/front/checks/"+code+"/clear-events", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("clear-events = %d, body %s", res.StatusCode, read(t, res))
	}

	after, err := ts.db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != engine.StatusNew || after.LastPing != nil || after.AlertAfter != nil {
		t.Errorf("check after clearing = %+v, want a fresh new check", after)
	}
	if pings, err := ts.db.ListPings(ctx, c.ID, 10); err != nil || len(pings) != 0 {
		t.Errorf("pings = %d/%v, want the history gone", len(pings), err)
	}

	// 清完还得能重新计时：一次 ping 就把它推回 up。
	pingCode(t, ts, code, "")
	if got, err := ts.db.CheckByID(ctx, c.ID); err != nil || got.Status != engine.StatusUp {
		t.Errorf("status after a new ping = %q/%v, want up", got.Status, err)
	}

	if res := ts.do(t, "POST", "/front/checks/no-such-code/clear-events", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("unknown check = %d, want 404", res.StatusCode)
	}
}

func checkDoc(t *testing.T, ts *testServer, uuid string) map[string]any {
	t.Helper()
	doc := decode(t, ts.do(t, "GET", "/api/v3/checks/", nil))
	for _, raw := range doc["checks"].([]any) {
		c := raw.(map[string]any)
		if c["uuid"] == uuid {
			return c
		}
	}
	t.Fatalf("check %s missing from the list", uuid)
	return nil
}

func TestProjectUpdateEndpoint(t *testing.T) {
	ts, _ := newFrontServer(t, &fakeNotifier{})

	doc := decode(t, ts.do(t, "POST", "/front/project", map[string]any{"name": "家里的活", "show_slugs": true}))
	if doc["name"] != "家里的活" || doc["show_slugs"] != true {
		t.Fatalf("project = %+v", doc)
	}

	// 只发一半：另一半保持原值。
	doc = decode(t, ts.do(t, "POST", "/front/project", map[string]any{"name": "换个名字"}))
	if doc["name"] != "换个名字" || doc["show_slugs"] != true {
		t.Errorf("project = %+v, want show_slugs untouched", doc)
	}

	for _, bad := range []map[string]any{{"name": "   "}, {"name": strings.Repeat("长", 201)}} {
		if res := ts.do(t, "POST", "/front/project", bad); res.StatusCode != http.StatusBadRequest {
			t.Errorf("name %q = %d, want 400", bad["name"], res.StatusCode)
		}
	}

	// slug 形态开着时 API 给的是 <ping_key>/<slug>；没 slug 的检查项仍用 UUID。
	pingKey := decode(t, ts.do(t, "GET", "/front/project", nil))["ping_key"].(string)
	_, withSlug := createCheck(t, ts, map[string]any{"name": "带标识", "slug": "nightly"})
	_, noSlug := createCheck(t, ts, map[string]any{"name": "只有名字"})
	if got := checkDoc(t, ts, withSlug)["ping_url"]; got != "http://example.test/ping/"+pingKey+"/nightly" {
		t.Errorf("ping_url = %v, want the slug form", got)
	}
	if got := checkDoc(t, ts, noSlug)["ping_url"]; got != "http://example.test/ping/"+noSlug {
		t.Errorf("ping_url without a slug = %v, want the UUID form", got)
	}

	decode(t, ts.do(t, "POST", "/front/project", map[string]any{"show_slugs": false}))
	if got := checkDoc(t, ts, withSlug)["ping_url"]; got != "http://example.test/ping/"+withSlug {
		t.Errorf("ping_url after switching back = %v, want the UUID form", got)
	}
}
