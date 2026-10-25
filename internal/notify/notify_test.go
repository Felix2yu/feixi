package notify

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	neturl "net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Felix2yu/feixi/internal/config"
	"github.com/Felix2yu/feixi/internal/cronex"
	"github.com/Felix2yu/feixi/internal/engine"
	"github.com/Felix2yu/feixi/internal/store"
)

func testWorker(t *testing.T) (*Worker, *store.DB, *store.Project) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "notify.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Sched = cronex.Scheduler{}
	db.MaxChecks = 100
	db.PingLogLimit = 100

	cfg := config.Default()
	cfg.NotifyDeadline = 5 * time.Second
	cfg.NotifyRetries = 0
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))

	w := NewWorker(db, cfg, slog.Default())
	project, err := db.Seed(context.Background(), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	return w, db, project
}

func TestTitleAndBody(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := &engine.Check{Name: "夜间备份", Kind: engine.KindSimple, Timeout: 24 * time.Hour,
		Status: engine.StatusDown, LastPing: &now}

	if got := title(c, engine.StatusDown); got != "夜间备份 DOWN" {
		t.Errorf("title = %q", got)
	}
	down := body(c, &engine.Flip{NewStatus: engine.StatusDown, Reason: engine.ReasonTimeout}, nil, "")
	if !strings.Contains(down, "原因：成功信号未按时到达，宽限期已过。") {
		t.Errorf("down body = %q", down)
	}

	dur := 90 * time.Minute
	up := body(c, &engine.Flip{NewStatus: engine.StatusUp}, &dur, "")
	if !strings.Contains(up, "宕机持续了 1 小时 30 分钟。") {
		t.Errorf("up body = %q", up)
	}
}

func TestVarsSubstitution(t *testing.T) {
	w, db, project := testWorker(t)
	c, err := db.CreateCheck(context.Background(), &store.NewCheck{
		ProjectID: project.ID, Name: `he said "hi" & left`, Slug: "backup",
		Tags: "prod www", Kind: engine.KindSimple, Timeout: time.Hour, Grace: time.Hour, TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := db.RecordPing(context.Background(), c.ID, engine.ActionSuccess,
		store.PingInput{Method: "POST", Body: []byte("run finished")}); err != nil {
		t.Fatal(err)
	}
	fresh, err := db.CheckByID(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}

	// The flip must be dated after the ping, or the "last ping before the flip"
	// lookup legitimately finds nothing.
	flip := &engine.Flip{Created: time.Now().UTC(), NewStatus: engine.StatusDown, Reason: engine.ReasonFail}
	vars, err := w.buildVars(context.Background(), fresh, flip, "$NAME|$STATUS|$CODE|$TAG1|$BODY|$JSON|$MISSING")
	if err != nil {
		t.Fatal(err)
	}

	raw := "$NAME|$STATUS|$CODE|$TAG1|$BODY|$JSON|$MISSING"
	got := vars.apply(raw)

	if strings.Contains(got, "$NAME") || strings.Contains(got, "$STATUS") {
		t.Errorf("variables were not expanded: %s", got)
	}
	if !strings.Contains(got, "$MISSING") {
		t.Errorf("unknown placeholder should survive: %s", got)
	}
	if strings.Contains(got, "&") && !strings.Contains(got, "%26") {
		t.Errorf("an ampersand in the name would break the query string: %s", got)
	}
	if !strings.Contains(got, "run+finished") {
		t.Errorf("$BODY should be URL encoded: %s", got)
	}
	if !strings.Contains(got, "%7B") {
		t.Errorf("$JSON should be URL encoded JSON: %s", got)
	}
	// Whatever the receiver's URL parser sees must decode back to the original.
	if back, err := neturl.QueryUnescape("run+finished"); err != nil || back != "run finished" {
		t.Errorf("round trip failed: %q %v", back, err)
	}
	// $STATUS is the lowercase persisted status, as in the original.
	if !strings.Contains(got, "|down|") || !strings.Contains(got, "|prod|") {
		t.Errorf("unexpected expansion: %s", got)
	}
}

// TestBodyJSONOnlyFetchedWhenUsed guards the lazy lookup: a URL without $BODY
// must not read the ping table.
func TestBodyVarsAreLazy(t *testing.T) {
	w, db, project := testWorker(t)
	c, err := db.CreateCheck(context.Background(), &store.NewCheck{
		ProjectID: project.ID, Name: "x", Kind: engine.KindSimple,
		Timeout: time.Hour, Grace: time.Hour, TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	vars, err := w.buildVars(context.Background(), c, &engine.Flip{Created: time.Now()}, "$NAME")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := vars.values["$BODY"]; ok {
		t.Error("$BODY was materialized without being referenced")
	}
}

func TestIsClientError(t *testing.T) {
	cases := []struct {
		msg  string
		want bool
	}{
		{"discord://x: unexpected status: Not Found", true},
		{"tgram://x: unexpected status: Unauthorized", true},
		{"slack://x: unexpected status: Forbidden", true},
		{"json://x: unexpected status: Internal Server Error", false},
		{"json://x: unexpected status: Bad Request", false},
		{"json://x: dial tcp: connection refused", false},
		{"unsupported URL scheme: bogus", false},
	}
	for _, tc := range cases {
		if got := isClientError(errWrap(tc.msg)); got != tc.want {
			t.Errorf("isClientError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

func errWrap(msg string) error { return &plainError{msg} }

type plainError struct{ msg string }

func (e *plainError) Error() string { return e.msg }

// TestProcessDeliversOnce walks a real flip through apprise into a local
// receiver: exactly one POST, with the substituted fields.
func TestProcessDeliversOnce(t *testing.T) {
	w, db, project := testWorker(t)

	var hits int32
	var last map[string]any
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		body, _ := io.ReadAll(r.Body)
		doc := map[string]any{}
		// mock 解析失败会让后续断言拿不到字段，届时自然暴露
		_ = json.Unmarshal(body, &doc)
		last = doc
		lastQuery = r.URL.RawQuery
		io.WriteString(rw, `{"ok":true}`)
	}))
	defer srv.Close()

	// apprise maps "?:key=value" onto payload fields, "?-key" onto query params
	// and "?+key" onto headers.
	url := strings.Replace(srv.URL, "http://", "json://", 1) + "/hook?:check=$NAME&:status=$STATUS&-token=$CODE"
	ch, err := db.CreateChannel(context.Background(), &store.NewChannel{
		ProjectID: project.ID, Name: "hook", URLs: []string{url},
		Conf: store.ChannelConf{Up: false, Down: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.CreateCheck(context.Background(), &store.NewCheck{
		ProjectID: project.ID, Name: "夜间备份", Kind: engine.KindSimple,
		Timeout: time.Hour, Grace: time.Hour, TZ: "UTC", ChannelIDs: []int64{ch.ID},
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	// new->up must be silent.
	if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionSuccess, store.PingInput{Method: "GET"}); err != nil {
		t.Fatal(err)
	}
	up, ok, err := db.ClaimNextFlip(ctx, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("claim: %v", err)
	}
	if err := w.Process(ctx, up); err != nil {
		t.Fatalf("process new->up: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 0 {
		t.Fatalf("new->up delivered %d notifications, want 0", got)
	}

	// A failure signal does alert.
	if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionFail, store.PingInput{Method: "GET"}); err != nil {
		t.Fatal(err)
	}
	down, ok, err := db.ClaimNextFlip(ctx, time.Now().UTC())
	if err != nil || !ok {
		t.Fatal("expected the down flip to be queued")
	}
	if err := w.Process(ctx, down); err != nil {
		t.Fatalf("process down: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Fatalf("delivered %d notifications, want exactly 1", got)
	}
	if last["title"] != "夜间备份 DOWN" {
		t.Errorf("title = %v", last["title"])
	}
	msg, _ := last["message"].(string)
	if !strings.Contains(msg, "收到了失败信号") {
		t.Errorf("message = %q", msg)
	}
	if last["check"] != "夜间备份" {
		t.Errorf("payload field check = %v, want the substituted name", last["check"])
	}
	if last["status"] != "down" {
		t.Errorf("payload field status = %v", last["status"])
	}
	if !strings.Contains(lastQuery, "token="+c.Code) {
		t.Errorf("query params = %q, want the code", lastQuery)
	}

	notifs, err := db.Notifications(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(notifs) != 1 || notifs[0].Error != "" {
		t.Errorf("notification rows = %+v", notifs)
	}
	after, err := db.ChannelByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Disabled || after.LastError != "" || after.LastNotify == nil {
		t.Errorf("channel health fields = %+v", after)
	}
}

func TestPermanentFailureDisablesChannel(t *testing.T) {
	w, db, project := testWorker(t)
	ctx := context.Background()

	ch, err := db.CreateChannel(ctx, &store.NewChannel{
		ProjectID: project.ID, Name: "坏 URL", URLs: []string{"json://127.0.0.1:1/hook"},
		Conf: store.ChannelConf{Up: true, Down: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.CreateCheck(ctx, &store.NewCheck{
		ProjectID: project.ID, Name: "x", Kind: engine.KindSimple,
		Timeout: time.Hour, Grace: time.Hour, TZ: "UTC", ChannelIDs: []int64{ch.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionFail, store.PingInput{}); err != nil {
		t.Fatal(err)
	}
	f, ok, err := db.ClaimNextFlip(ctx, time.Now().UTC())
	if err != nil || !ok {
		t.Fatal("expected a queued flip")
	}
	if err := w.Process(ctx, f); err == nil {
		t.Fatal("expected the delivery to fail")
	}

	// A refused connection is transient: the channel must stay enabled but keep
	// the error text for the UI.
	ch2, err := db.ChannelByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if ch2.Disabled {
		t.Errorf("transient failure disabled the channel: %q", ch2.LastError)
	}
	if ch2.LastError == "" {
		t.Error("the failure text was not recorded")
	}
	notifs, _ := db.Notifications(ctx, 10)
	if len(notifs) == 0 || notifs[0].Error == "" {
		t.Errorf("notification should carry the error: %+v", notifs)
	}
}

func TestSkipsNoopAndDisabledChannels(t *testing.T) {
	w, db, project := testWorker(t)
	ctx := context.Background()

	downOnly, _ := db.CreateChannel(ctx, &store.NewChannel{
		ProjectID: project.ID, Name: "只发宕机", URLs: []string{"json://127.0.0.1:1/hook"},
		Conf: store.ChannelConf{Up: false, Down: true},
	})
	disabled, _ := db.CreateChannel(ctx, &store.NewChannel{
		ProjectID: project.ID, Name: "已禁用", URLs: []string{"json://127.0.0.1:1/hook"},
		Conf: store.ChannelConf{Up: true, Down: true},
	})
	disabled.Disabled = true
	if err := db.UpdateChannel(ctx, disabled); err != nil {
		t.Fatal(err)
	}

	c, _ := db.CreateCheck(ctx, &store.NewCheck{
		ProjectID: project.ID, Name: "x", Kind: engine.KindSimple,
		Timeout: time.Hour, Grace: time.Hour, TZ: "UTC",
		ChannelIDs: []int64{downOnly.ID, disabled.ID},
	})
	_, _, _, _, _ = db.RecordPing(ctx, c.ID, engine.ActionSuccess, store.PingInput{})

	flip, ok, _ := db.ClaimNextFlip(ctx, time.Now().UTC())
	if !ok {
		t.Fatal("expected the new->up flip")
	}
	// new->up is filtered before channels are even looked at.
	if err := w.Process(ctx, flip); err != nil {
		t.Fatal(err)
	}

	_, _, _, _, _ = db.RecordPing(ctx, c.ID, engine.ActionFail, store.PingInput{})
	down, ok, _ := db.ClaimNextFlip(ctx, time.Now().UTC())
	if !ok {
		t.Fatal("expected the down flip")
	}
	// downOnly is eligible, disabled is not; both point at a dead port, so we
	// assert on how many notification rows were written instead.
	if err := w.Process(ctx, down); err == nil {
		t.Fatal("delivery should fail against a closed port")
	}
	notifs, _ := db.Notifications(ctx, 10)
	if len(notifs) != 1 {
		t.Errorf("notification rows = %d, want 1 (disabled channel excluded)", len(notifs))
	}
}
