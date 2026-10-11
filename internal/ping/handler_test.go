package ping

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yufei/feixi/internal/cronex"
	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/store"
)

type fixture struct {
	db      *store.DB
	project *store.Project
	handler http.Handler
	pruned  []int64
}

func newFixture(t *testing.T, bodyLimit int) *fixture {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "ping.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Sched = cronex.Scheduler{}
	db.MaxChecks = 100
	db.PingLogLimit = 100

	ctx := context.Background()
	project, err := db.Seed(ctx, "test-secret")
	if err != nil {
		t.Fatal(err)
	}

	f := &fixture{db: db, project: project}
	h := &Handler{DB: db, BodyLimit: bodyLimit, OnPrune: func(id int64) { f.pruned = append(f.pruned, id) }}

	r := chi.NewRouter()
	for _, method := range []string{"GET", "HEAD", "POST"} {
		r.Method(method, "/ping/{first}", http.HandlerFunc(h.Root))
		r.Method(method, "/ping/{first}/{second}", http.HandlerFunc(h.Root))
		r.Method(method, "/ping/{first}/{second}/{third}", http.HandlerFunc(h.Root))
	}
	f.handler = r
	return f
}

func (f *fixture) create(t *testing.T, mutate func(*store.NewCheck)) *engine.Check {
	t.Helper()
	in := &store.NewCheck{
		ProjectID: f.project.ID,
		Name:      "任务",
		Kind:      engine.KindSimple,
		Timeout:   engine.DefaultTimeout,
		Grace:     engine.DefaultGrace,
		TZ:        "UTC",
	}
	if mutate != nil {
		mutate(in)
	}
	c, err := f.db.CreateCheck(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (f *fixture) do(t *testing.T, method, path string, body string, headers map[string]string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.RemoteAddr = "203.0.113.7:54321"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec.Result()
}

// status reloads the check so assertions run against what actually landed.
func (f *fixture) status(t *testing.T, id int64) engine.Status {
	t.Helper()
	c, err := f.db.CheckByID(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return c.Status
}

func TestByCodeRecordsSuccess(t *testing.T) {
	f := newFixture(t, 100)
	c := f.create(t, nil)

	res := f.do(t, "GET", "/ping/"+c.Code, "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Ping-Body-Limit"); got != "100" {
		t.Errorf("Ping-Body-Limit = %q", got)
	}
	if got := res.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("cors header = %q", got)
	}
	if got := res.Header.Get("Cache-Control"); got != "no-cache" {
		t.Errorf("cache-control = %q", got)
	}

	// 首枚 ping 把 New 推到 Up，翻转记录同时落下。
	if got := f.status(t, c.ID); got != engine.StatusUp {
		t.Errorf("status = %q, want up", got)
	}
}

func TestByCodeSuffixes(t *testing.T) {
	tests := []struct {
		suffix   string
		wantBody string
		wantCode int
		wantDown bool
	}{
		{suffix: "", wantCode: http.StatusOK, wantBody: "OK"},
		{suffix: "success", wantCode: http.StatusOK, wantBody: "OK"},
		{suffix: "start", wantCode: http.StatusOK, wantBody: "OK"},
		{suffix: "fail", wantCode: http.StatusOK, wantBody: "OK", wantDown: true},
		{suffix: "log", wantCode: http.StatusOK, wantBody: "OK"},
		{suffix: "ign", wantCode: http.StatusOK, wantBody: "OK"},
		{suffix: "2", wantCode: http.StatusOK, wantBody: "OK", wantDown: true},
		{suffix: "0", wantCode: http.StatusOK, wantBody: "OK"},
		{suffix: "256", wantCode: http.StatusBadRequest, wantBody: "无效的链接格式"},
		{suffix: "-1", wantCode: http.StatusNotFound, wantBody: "未找到"},
		{suffix: "nope", wantCode: http.StatusNotFound, wantBody: "未找到"},
	}

	for _, tt := range tests {
		t.Run("suffix="+tt.suffix, func(t *testing.T) {
			f := newFixture(t, 100)
			c := f.create(t, nil)

			path := "/ping/" + c.Code
			if tt.suffix != "" {
				path += "/" + tt.suffix
			}
			res := f.do(t, "GET", path, "", nil)
			if res.StatusCode != tt.wantCode {
				t.Fatalf("status = %d, want %d", res.StatusCode, tt.wantCode)
			}
			body, _ := io.ReadAll(res.Body)
			if string(body) != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
			if tt.wantCode != http.StatusOK {
				return
			}
			if down := f.status(t, c.ID) == engine.StatusDown; down != tt.wantDown {
				t.Errorf("down = %v, want %v", down, tt.wantDown)
			}
		})
	}
}

func TestByCodeUnknown(t *testing.T) {
	f := newFixture(t, 100)
	res := f.do(t, "GET", "/ping/"+uuid.NewString(), "", nil)
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", res.StatusCode)
	}
}

func TestByCodeRecordsPingFields(t *testing.T) {
	f := newFixture(t, 10)
	c := f.create(t, nil)

	res := f.do(t, "POST", "/ping/"+c.Code+"/fail", "0123456789ABCDEF", map[string]string{
		"User-Agent":        "cron/1.0",
		"X-Forwarded-For":   "198.51.100.9, 203.0.113.7",
		"X-Forwarded-Proto": "https",
	})
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}

	pings, err := f.db.ListPings(context.Background(), c.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pings) != 1 {
		t.Fatalf("pings = %d, want 1", len(pings))
	}
	p := pings[0]
	// 正文按 Ping-Body-Limit 截断后再入库，body_size 记的是截断后的字节数。
	if len(p.Body) != 10 {
		t.Errorf("body = %q, want the first 10 bytes", p.Body)
	}
	if p.BodySize != 10 {
		t.Errorf("body_size = %d, want 10", p.BodySize)
	}
	if p.RemoteAddr != "198.51.100.9" {
		t.Errorf("remote_addr = %q, want the first X-Forwarded-For hop", p.RemoteAddr)
	}
	if p.Scheme != "https" {
		t.Errorf("scheme = %q, want the forwarded proto", p.Scheme)
	}
	if p.Method != "POST" || p.UA != "cron/1.0" {
		t.Errorf("method/ua = %q/%q", p.Method, p.UA)
	}
	if p.Kind != string(engine.ActionFail) {
		t.Errorf("kind = %q, want fail", p.Kind)
	}
}

func TestRid(t *testing.T) {
	f := newFixture(t, 100)
	c := f.create(t, nil)
	rid := uuid.NewString()

	res := f.do(t, "GET", "/ping/"+c.Code+"/start?rid="+rid, "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	after, err := f.db.CheckByID(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastStartRid == nil || *after.LastStartRid != rid {
		t.Errorf("last_start_rid = %v, want %s", after.LastStartRid, rid)
	}

	if res := f.do(t, "GET", "/ping/"+c.Code+"?rid=not-a-uuid", "", nil); res.StatusCode != http.StatusBadRequest {
		t.Errorf("bad rid status = %d, want 400", res.StatusCode)
	}
}

func TestEmptyBodyStillRecords(t *testing.T) {
	f := newFixture(t, 100)
	c := f.create(t, nil)

	// httptest.NewRequest 总要给个 Body；这里直接构造无 Body 的请求走 handler。
	h := &Handler{DB: f.db, BodyLimit: 100}
	req := httptest.NewRequest("GET", "/ping/"+c.Code, nil)
	req.Body = nil
	rec := httptest.NewRecorder()
	h.Root(rec, withParams(req, "first", c.Code))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestBySlug(t *testing.T) {
	f := newFixture(t, 100)
	c := f.create(t, func(in *store.NewCheck) { in.Slug = "nightly" })

	res := f.do(t, "GET", "/ping/"+f.project.PingKey+"/nightly", "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := f.status(t, c.ID); got != engine.StatusUp {
		t.Errorf("status = %q, want up", got)
	}
}

func TestBySlugCreate(t *testing.T) {
	f := newFixture(t, 100)

	res := f.do(t, "GET", "/ping/"+f.project.PingKey+"/brand-new?create=1", "", nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", res.StatusCode)
	}
	body, _ := io.ReadAll(res.Body)
	if string(body) != "Created" {
		t.Errorf("body = %q, want Created", body)
	}

	list, err := f.db.ListChecks(context.Background(), f.project.ID, store.CheckFilter{Slugs: []string{"brand-new"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("checks = %d, want the auto-provisioned one", len(list))
	}
	if list[0].Name != "brand-new" || list[0].Kind != engine.KindSimple {
		t.Errorf("created check = %+v", list[0])
	}
	if list[0].Timeout != engine.DefaultTimeout || list[0].TZ != "UTC" {
		t.Errorf("created check should carry the defaults: %+v", list[0])
	}

	// 第二次不再走创建分支，回到普通的 200。
	if res := f.do(t, "GET", "/ping/"+f.project.PingKey+"/brand-new", "", nil); res.StatusCode != http.StatusOK {
		t.Errorf("second ping = %d, want 200", res.StatusCode)
	}
}

func TestBySlugCreateAssignsAllChannels(t *testing.T) {
	f := newFixture(t, 100)
	ctx := context.Background()

	for _, name := range []string{"甲", "乙"} {
		if _, err := f.db.CreateChannel(ctx, &store.NewChannel{
			ProjectID: f.project.ID, Name: name, URLs: []string{"json://localhost"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := f.db.AllChannelIDs(ctx, f.project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Fatalf("project channels = %d, want 2", ids)
	}

	res := f.do(t, "GET", "/ping/"+f.project.PingKey+"/fresh?create=1", "", nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", res.StatusCode)
	}
	list, err := f.db.ListChecks(ctx, f.project.ID, store.CheckFilter{Slugs: []string{"fresh"}})
	if err != nil {
		t.Fatal(err)
	}
	channels, err := f.db.CheckChannels(ctx, list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(channels) != 2 {
		t.Errorf("new check got %v channels, want the project's 2", channels)
	}
}

func TestBySlugRejected(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		path     func(f *fixture) string
		wantCode int
		wantBody string
	}{
		{
			name:   "unknown project key",
			method: "GET",
			path: func(f *fixture) string {
				return "/ping/" + uuid.NewString() + "/nightly"
			},
			wantCode: http.StatusNotFound, wantBody: "未找到",
		},
		{
			name:     "missing slug",
			method:   "GET",
			path:     func(f *fixture) string { return "/ping/" + f.project.PingKey },
			wantCode: http.StatusBadRequest, wantBody: "无效的链接格式",
		},
		{
			name:     "uppercase slug",
			method:   "GET",
			path:     func(f *fixture) string { return "/ping/" + f.project.PingKey + "/Nightly" },
			wantCode: http.StatusBadRequest, wantBody: "无效的链接格式",
		},
		{
			name:   "unknown slug without create",
			method: "GET",
			path: func(f *fixture) string {
				return "/ping/" + f.project.PingKey + "/ghost"
			},
			wantCode: http.StatusNotFound, wantBody: "未找到",
		},
		{
			name:   "uuid first segment with a third part falls through to slug",
			method: "GET",
			path: func(f *fixture) string {
				return "/ping/" + uuid.NewString() + "/nightly/fail"
			},
			wantCode: http.StatusNotFound, wantBody: "未找到",
		},
	}

	// slug 上有 partial 唯一索引（WHERE slug != ''），所以「标识存在歧义」
	// 只能来自索引建立之前的历史数据，这里构造不出来，不覆盖 409 分支。
	f := newFixture(t, 100)
	if _, err := f.db.CreateCheck(context.Background(), &store.NewCheck{
		ProjectID: f.project.ID, Name: "nightly", Slug: "nightly", Kind: engine.KindSimple,
		Timeout: engine.DefaultTimeout, Grace: engine.DefaultGrace, TZ: "UTC",
	}); err != nil {
		t.Fatal(err)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := f.do(t, tt.method, tt.path(f), "", nil)
			if res.StatusCode != tt.wantCode {
				t.Fatalf("status = %d, want %d", res.StatusCode, tt.wantCode)
			}
			body, _ := io.ReadAll(res.Body)
			if string(body) != tt.wantBody {
				t.Errorf("body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}

func TestHeadRecordsASuccessSignal(t *testing.T) {
	f := newFixture(t, 100)
	c := f.create(t, nil)

	res := f.do(t, "HEAD", "/ping/"+c.Code, "", nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := f.status(t, c.ID); got != engine.StatusUp {
		t.Errorf("status = %q, want up", got)
	}

	pings, err := f.db.ListPings(context.Background(), c.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pings) != 1 {
		t.Fatalf("pings = %d, want 1", len(pings))
	}
	if pings[0].Method != "HEAD" {
		t.Errorf("method = %q, want HEAD", pings[0].Method)
	}
	if pings[0].Kind != "" {
		t.Errorf("kind = %q, want the bare success signal", pings[0].Kind)
	}
	// Go 只把 HEAD 的响应体丢掉，头还是要照发。
	if res.Header.Get("Ping-Body-Limit") != "100" {
		t.Errorf("Ping-Body-Limit = %q", res.Header.Get("Ping-Body-Limit"))
	}
}

func TestBySlugCreateOverLimitHasHeadroom(t *testing.T) {
	f := newFixture(t, 100)
	f.db.MaxChecks = 1
	if _, err := f.db.CreateCheck(context.Background(), &store.NewCheck{
		ProjectID: f.project.ID, Name: "占位", Slug: "占位", Kind: engine.KindSimple,
		Timeout: engine.DefaultTimeout, Grace: engine.DefaultGrace, TZ: "UTC",
	}); err != nil {
		t.Fatal(err)
	}

	// 自动建档给脚本两倍余量：上限 1、已占 1，还能再建一个。
	res := f.do(t, "GET", "/ping/"+f.project.PingKey+"/overflow?create=1", "", nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("first auto-create = %d, want 201 inside the headroom", res.StatusCode)
	}
	if res := f.do(t, "GET", "/ping/"+f.project.PingKey+"/second?create=1", "", nil); res.StatusCode != http.StatusNotFound {
		t.Errorf("past the headroom = %d, want 404", res.StatusCode)
	}

	// 管理 API 建档没有这层余量，一倍就到顶。
	if _, err := f.db.CreateCheck(context.Background(), &store.NewCheck{
		ProjectID: f.project.ID, Name: "手动", Kind: engine.KindSimple,
		Timeout: engine.DefaultTimeout, Grace: engine.DefaultGrace, TZ: "UTC",
	}); err != store.ErrTooManyChecks {
		t.Errorf("manual create err = %v, want ErrTooManyChecks", err)
	}
}

func TestPruneCallback(t *testing.T) {
	f := newFixture(t, 100)
	c := f.create(t, nil)

	for i := 0; i < engine.PruneEveryNPing; i++ {
		if res := f.do(t, "GET", "/ping/"+c.Code+"/log", "", nil); res.StatusCode != http.StatusOK {
			t.Fatalf("ping %d: status = %d", i, res.StatusCode)
		}
	}
	if len(f.pruned) != 1 || f.pruned[0] != c.ID {
		t.Errorf("prune callbacks = %v, want exactly one for check %d", f.pruned, c.ID)
	}
}

func TestResolveAction(t *testing.T) {
	tests := []struct {
		name   string
		check  engine.Check
		action engine.Action
		method string
		body   string
		want   engine.Action
	}{
		{
			name:   "POST-only check downgrades a GET to ign",
			check:  engine.Check{Methods: "POST"},
			action: engine.ActionFail, method: "GET", body: "boom",
			want: engine.ActionIgn,
		},
		{
			name:   "POST-only check keeps a POST",
			check:  engine.Check{Methods: "POST"},
			action: engine.ActionFail, method: "POST",
			want: engine.ActionFail,
		},
		{
			name:   "ign is never re-filtered",
			check:  engine.Check{FilterHTTPBody: true, FailureKW: "boom"},
			action: engine.ActionIgn, method: "GET", body: "boom",
			want: engine.ActionIgn,
		},
		{
			name:   "failure keyword wins over the success keyword",
			check:  engine.Check{FilterHTTPBody: true, SuccessKW: "ok", FailureKW: "boom"},
			action: engine.ActionSuccess, method: "GET", body: "ok but boom",
			want: engine.ActionFail,
		},
		{
			name:   "success keyword overrides a fail ping",
			check:  engine.Check{FilterHTTPBody: true, SuccessKW: "ok"},
			action: engine.ActionFail, method: "GET", body: "all ok",
			want: engine.ActionSuccess,
		},
		{
			name:   "start keyword",
			check:  engine.Check{FilterHTTPBody: true, StartKW: "start"},
			action: engine.ActionSuccess, method: "GET", body: "job start",
			want: engine.ActionStart,
		},
		{
			name:   "nothing matches and the default is fail",
			check:  engine.Check{FilterHTTPBody: true, FilterDefaultFail: true, SuccessKW: "ok"},
			action: engine.ActionSuccess, method: "GET", body: "nothing here",
			want: engine.ActionFail,
		},
		{
			name:   "nothing matches and the default is ign",
			check:  engine.Check{FilterHTTPBody: true, SuccessKW: "ok"},
			action: engine.ActionSuccess, method: "GET", body: "nothing here",
			want: engine.ActionIgn,
		},
		{
			name:   "filter off keeps the requested action",
			check:  engine.Check{FailureKW: "boom"},
			action: engine.ActionSuccess, method: "GET", body: "boom",
			want: engine.ActionSuccess,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.check
			if got := resolveAction(&c, tt.action, tt.method, []byte(tt.body)); got != tt.want {
				t.Errorf("resolveAction = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPostOnlyCheckIgnoresGetPing(t *testing.T) {
	f := newFixture(t, 100)
	c := f.create(t, func(in *store.NewCheck) { in.Methods = "POST" })

	// GET 被降级成 ign，状态仍停在 New。
	if res := f.do(t, "GET", "/ping/"+c.Code+"/success", "", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if got := f.status(t, c.ID); got != engine.StatusNew {
		t.Errorf("status after a GET = %q, want new", got)
	}
	// HEAD 同样不算 POST。
	if res := f.do(t, "HEAD", "/ping/"+c.Code+"/success", "", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if got := f.status(t, c.ID); got != engine.StatusNew {
		t.Errorf("status after a HEAD = %q, want new", got)
	}
	if res := f.do(t, "POST", "/ping/"+c.Code+"/success", "", nil); res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", res.StatusCode)
	}
	if got := f.status(t, c.ID); got != engine.StatusUp {
		t.Errorf("status after a POST = %q, want up", got)
	}
}

func TestMatchKeywords(t *testing.T) {
	if !matchKeywords("job finished", " started ,finished") {
		t.Error("comma separated list should match any trimmed keyword")
	}
	if matchKeywords("job", "finished,") {
		t.Error("an empty entry from a trailing comma must not match everything")
	}
	if matchKeywords("Job Finished", "job") {
		t.Error("matching is case sensitive")
	}
	if matchKeywords("", "x") {
		t.Error("empty haystack cannot contain a keyword")
	}
}

func TestTruncateCutsOnBytes(t *testing.T) {
	if got := truncate("abcdef", 3); got != "abc" {
		t.Errorf("truncate = %q", got)
	}
	if got := truncate("ab", 5); got != "ab" {
		t.Errorf("short input should pass through, got %q", got)
	}
}

func TestRemoteAddrFallbacks(t *testing.T) {
	tests := []struct {
		name   string
		header string
		remote string
		want   string
	}{
		{"bare address without port", "", "203.0.113.7", "203.0.113.7"},
		{"forwarded list keeps the first hop", "198.51.100.9, 7.7.7.7", "203.0.113.7:1", "198.51.100.9"},
		{"single forwarded value is trimmed", "  198.51.100.9  ", "203.0.113.7:1", "198.51.100.9"},
		{"forwarded value still carrying a port loses it", "192.0.2.1:8080", "203.0.113.7:1", "192.0.2.1"},
		{"unparseable value is passed through", "not-an-ip", "127.0.0.1:1", "not-an-ip"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.remote
			if tt.header != "" {
				req.Header.Set("X-Forwarded-For", tt.header)
			}
			if got := remoteAddr(req); got != tt.want {
				t.Errorf("remoteAddr = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScheme(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	if got := scheme(req); got != "http" {
		t.Errorf("plain request = %q", got)
	}
	req.Header.Set("X-Forwarded-Proto", "https")
	if got := scheme(req); got != "https" {
		t.Errorf("forwarded proto = %q", got)
	}

	f := newFixture(t, 100)
	c := f.create(t, nil)
	// TLS 终结在反代后面、又没有转发头时，只能靠连接本身判断。
	req = httptest.NewRequest("GET", "/ping/"+c.Code, nil)
	req.TLS = &tls.ConnectionState{}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	pings, err := f.db.ListPings(context.Background(), c.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(pings) != 1 || pings[0].Scheme != "https" {
		t.Errorf("scheme = %v, want https", pings)
	}
}

func withParams(r *http.Request, key, value string) *http.Request {
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, ctx))
}
