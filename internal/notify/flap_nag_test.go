package notify

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Felix2yu/feixi/internal/engine"
	"github.com/Felix2yu/feixi/internal/store"
)

// receiver 是一个假的 apprise 目标：记录每次 POST 的正文与查询串。
type receiver struct {
	mu       sync.Mutex
	srv      *httptest.Server
	title    string
	message  string
	status   string
	hits     int
	urlValue string
}

func newReceiver(t *testing.T, status int) *receiver {
	t.Helper()
	r := &receiver{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		doc := map[string]any{}
		// mock 解析失败会让后续断言拿不到字段，届时自然暴露
		_ = json.Unmarshal(body, &doc)

		r.mu.Lock()
		r.hits++
		r.title, _ = doc["title"].(string)
		r.message, _ = doc["message"].(string)
		if s, ok := doc["type"].(string); ok {
			r.status = s
		}
		r.mu.Unlock()

		w.WriteHeader(status)
		io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(r.srv.Close)
	r.urlValue = strings.Replace(r.srv.URL, "http://", "json://", 1)
	return r
}

func (r *receiver) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits
}

func (r *receiver) text() (string, string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.title, r.message
}

func (r *receiver) channel(t *testing.T, db *store.DB, projectID int64, conf store.ChannelConf) *store.Channel {
	t.Helper()
	ch, err := db.CreateChannel(context.Background(), &store.NewChannel{
		ProjectID: projectID, Name: "hook", URLs: []string{r.urlValue}, Conf: conf,
	})
	if err != nil {
		t.Fatal(err)
	}
	return ch
}

func mustCheck(t *testing.T, db *store.DB, projectID int64, channelIDs []int64, mutate func(*store.NewCheck)) *engine.Check {
	t.Helper()
	in := &store.NewCheck{
		ProjectID: projectID, Name: "夜间备份", Kind: engine.KindSimple,
		Timeout: time.Hour, Grace: time.Hour, TZ: "UTC", ChannelIDs: channelIDs,
	}
	if mutate != nil {
		mutate(in)
	}
	c, err := db.CreateCheck(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMessageTemplates(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	simple := &engine.Check{Name: "夜间备份", Kind: engine.KindSimple, Timeout: 90 * time.Minute,
		Status: engine.StatusDown, LastPing: &now, Code: "abc"}
	cron := &engine.Check{Name: "", Code: "uuid-ish", Kind: engine.KindCron,
		Schedule: "15 5 * * *", TZ: "Asia/Shanghai", LastPing: &now}
	bare := &engine.Check{Kind: engine.KindCron, Code: "only-code"}

	nag := nagBody(simple, engine.ReasonTimeout, 3*time.Hour, "http://x/checks/abc")
	for _, want := range []string{"仍未恢复：已宕机 3 小时。", "原因：成功信号未按时到达，宽限期已过。",
		"超时：1 小时 30 分钟", "最后 ping：2026-10-10 12:00:00 UTC", "http://x/checks/abc"} {
		if !strings.Contains(nag, want) {
			t.Errorf("nag body missing %q:\n%s", want, nag)
		}
	}

	// 没有原因（比如手工暂停后恢复）时不写原因行。
	if got := nagBody(simple, engine.ReasonNone, 0, ""); strings.Contains(got, "原因：") {
		t.Errorf("no reason should mean no reason line:\n%s", got)
	}
	if got := nagBody(simple, engine.ReasonFail, 0, ""); !strings.Contains(got, "收到了失败信号") {
		t.Errorf("fail reason:\n%s", got)
	}
	// 宕机时长为 0 时读起来是「0 秒」，但仍然要有这一行。
	if got := nagBody(simple, engine.ReasonNone, 0, ""); !strings.Contains(got, "已宕机 0 秒") {
		t.Errorf("zero duration:\n%s", got)
	}
	// cron 检查项报计划而不是超时。
	if got := nagBody(cron, engine.ReasonNone, time.Minute, ""); !strings.Contains(got, "计划：15 5 * * *（Asia/Shanghai）") {
		t.Errorf("cron context:\n%s", got)
	}
	// 没有名字时标题退到 code。
	if got := title(cron, engine.StatusDown); got != "uuid-ish DOWN" {
		t.Errorf("title = %q", got)
	}
	if got := flapTitle(bare); got != "only-code 状态抖动" {
		t.Errorf("flapTitle = %q", got)
	}
	if got := stableTitle(bare); got != "only-code 已恢复稳定" {
		t.Errorf("stableTitle = %q", got)
	}

	flap := flapEnterBody(simple, time.Hour, 3, "http://x/checks/abc")
	for _, want := range []string{"状态在 1 小时 内已至少翻转 3 次", "超时：1 小时 30 分钟", "http://x/checks/abc"} {
		if !strings.Contains(flap, want) {
			t.Errorf("flap body missing %q:\n%s", want, flap)
		}
	}

	stable := stableBody(cron, time.Hour, "")
	if !strings.Contains(stable, "抖动已结束：状态已连续 1 小时 保持正常。") {
		t.Errorf("stable body:\n%s", stable)
	}
	if !strings.Contains(stable, "计划：") {
		t.Errorf("stable body should keep the schedule line:\n%s", stable)
	}
	// 既没有 schedule 也不是 simple（Kind 为空）时两行都不写。
	if got := stableBody(&engine.Check{}, time.Hour, ""); strings.Contains(got, "超时：") || strings.Contains(got, "计划：") {
		t.Errorf("unexpected context line:\n%s", got)
	}
	// 没有 ping 过就不写最后 ping 行。
	if got := stableBody(&engine.Check{Kind: engine.KindSimple}, time.Hour, ""); strings.Contains(got, "最后 ping：") {
		t.Errorf("unexpected last ping line:\n%s", got)
	}

	// reasonLong 只认识两种原因。
	if got := reasonLong(engine.Reason("nonsense")); got != "" {
		t.Errorf("reasonLong = %q", got)
	}
}

func TestNagSendsAReminder(t *testing.T) {
	w, db, project := testWorker(t)
	rec := newReceiver(t, http.StatusOK)
	ch := rec.channel(t, db, project.ID, store.ChannelConf{Down: true})
	c := mustCheck(t, db, project.ID, []int64{ch.ID}, nil)

	downSince := time.Now().UTC().Add(-4 * time.Hour)
	if err := w.Nag(context.Background(), store.NagCandidate{
		Check: c, Channel: ch, DownSince: downSince, Reason: engine.ReasonFail,
	}); err != nil {
		t.Fatal(err)
	}

	if rec.count() != 1 {
		t.Fatalf("delivered %d times", rec.count())
	}
	title, message := rec.text()
	if title != "夜间备份 DOWN" {
		t.Errorf("title = %q", title)
	}
	if !strings.Contains(message, "仍未恢复：已宕机 4 小时") {
		t.Errorf("message = %q", message)
	}

	// 提醒要走正常的通知记录，历史页和去重都看得见。
	rows, err := db.NotificationsForCheck(context.Background(), c.ID, time.Now().UTC().Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CheckStatus != engine.StatusDown {
		t.Errorf("notification rows = %+v", rows)
	}
	if rows[0].Error != "" {
		t.Errorf("a delivered nag should be cleared, got %q", rows[0].Error)
	}
}

func TestNagSkipsQuietChannels(t *testing.T) {
	w, db, project := testWorker(t)
	rec := newReceiver(t, http.StatusOK)

	disabled := rec.channel(t, db, project.ID, store.ChannelConf{Down: true})
	disabled.Disabled = true
	c := mustCheck(t, db, project.ID, []int64{disabled.ID}, nil)

	if err := w.Nag(context.Background(), store.NagCandidate{Check: c, Channel: disabled}); err != nil {
		t.Errorf("disabled channel = %v", err)
	}
	// 只收恢复的渠道不该被提醒打扰。
	upOnly := rec.channel(t, db, project.ID, store.ChannelConf{Up: true})
	if err := w.Nag(context.Background(), store.NagCandidate{Check: c, Channel: upOnly}); err != nil {
		t.Errorf("up-only channel = %v", err)
	}
	if rec.count() != 0 {
		t.Errorf("quiet channels still received %d messages", rec.count())
	}

	// DownSince 为零值时不写具体时长，但提醒本身照发。
	if err := w.Nag(context.Background(), store.NagCandidate{
		Check: c, Channel: rec.channel(t, db, project.ID, store.ChannelConf{Down: true}),
	}); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 1 {
		t.Fatalf("delivered %d times", rec.count())
	}
	if _, message := rec.text(); !strings.Contains(message, "已宕机 0 秒") {
		t.Errorf("message = %q", message)
	}
}

func TestProcessFlapEnterMergesAlerts(t *testing.T) {
	w, db, project := testWorker(t)
	w.Cfg.FlapThreshold = 2
	w.Cfg.FlapWindow = time.Hour

	rec := newReceiver(t, http.StatusOK)
	ch := rec.channel(t, db, project.ID, store.ChannelConf{Up: true, Down: true})
	c := mustCheck(t, db, project.ID, []int64{ch.ID}, nil)
	ctx := context.Background()

	// 两次告警性翻转把窗口填满，第三次应当只发抖动通告。
	var flips []*store.PendingFlip
	for _, action := range []engine.Action{engine.ActionFail, engine.ActionSuccess, engine.ActionFail} {
		updated, flip, _, _, err := db.RecordPing(ctx, c.ID, action, store.PingInput{Method: "GET"})
		if err != nil {
			t.Fatal(err)
		}
		if flip == nil {
			t.Fatalf("action %q produced no flip", action)
		}
		flips = append(flips, &store.PendingFlip{Check: updated, Flip: *flip})
	}

	for _, f := range flips {
		if err := w.Process(ctx, f); err != nil {
			t.Fatal(err)
		}
	}

	title, message := rec.text()
	if !strings.Contains(title, "状态抖动") {
		t.Errorf("last title = %q", title)
	}
	if !strings.Contains(message, "判定为抖动") {
		t.Errorf("last message = %q", message)
	}
	// 第一条告警照常发出，第二条触达阈值时改发抖动感通告，第三条被抑制。
	if rec.count() != 2 {
		t.Errorf("delivered %d times, want the first alert plus the flap notice", rec.count())
	}

	stored, err := db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !stored.Flapping {
		t.Error("the check should be marked flapping")
	}
}

func TestProcessSuppressesLaterFlips(t *testing.T) {
	w, db, project := testWorker(t)
	w.Cfg.FlapThreshold = 1
	w.Cfg.FlapWindow = time.Hour

	rec := newReceiver(t, http.StatusOK)
	ch := rec.channel(t, db, project.ID, store.ChannelConf{Up: true, Down: true})
	c := mustCheck(t, db, project.ID, []int64{ch.ID}, nil)
	ctx := context.Background()

	first, err := db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 先标记成抖动，之后的逐条告警都应被抑制。
	if err := db.UpdateCheck(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx, `UPDATE checks SET flapping = 1 WHERE id = ?`, c.ID); err != nil {
		t.Fatal(err)
	}

	updated, flip, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionFail, store.PingInput{})
	if err != nil {
		t.Fatal(err)
	}
	if flip == nil {
		t.Fatal("expected a flip")
	}
	if err := w.Process(ctx, &store.PendingFlip{Check: updated, Flip: *flip}); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 0 {
		t.Errorf("suppressed alert still delivered %d times", rec.count())
	}
}

func TestSettleFlaps(t *testing.T) {
	w, db, project := testWorker(t)
	w.Cfg.FlapWindow = time.Hour
	ctx := context.Background()

	stableRec := newReceiver(t, http.StatusOK)
	upCh := stableRec.channel(t, db, project.ID, store.ChannelConf{Up: true, Down: true})
	var err error
	upCheck := mustCheck(t, db, project.ID, []int64{upCh.ID}, nil)
	if _, err := db.Write.ExecContext(ctx, `UPDATE checks SET status = 'up' WHERE id = ?`, upCheck.ID); err != nil {
		t.Fatal(err)
	}
	upCheck, err = db.CheckByID(ctx, upCheck.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := w.SettleFlaps(ctx, []store.FlapSettle{{Check: upCheck}}); err != nil {
		t.Fatal(err)
	}
	title, message := stableRec.text()
	if !strings.Contains(title, "已恢复稳定") {
		t.Errorf("title = %q", title)
	}
	if !strings.Contains(message, "抖动已结束") {
		t.Errorf("message = %q", message)
	}
	// 稳定通告记成 stable，普通历史里也看得见。
	rows, err := db.NotificationsForCheck(ctx, upCheck.ID, time.Now().UTC().Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CheckStatus != engine.StatusStable {
		t.Errorf("rows = %+v", rows)
	}

	// 抖动平息但仍是宕机：补发一条正常的宕机告警。
	downRec := newReceiver(t, http.StatusOK)
	downCh := downRec.channel(t, db, project.ID, store.ChannelConf{Up: true, Down: true})
	downCheck := mustCheck(t, db, project.ID, []int64{downCh.ID}, nil)
	if _, err := db.Write.ExecContext(ctx, `UPDATE checks SET status = 'down' WHERE id = ?`, downCheck.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO flips(owner_id, created, old_status, new_status, reason) VALUES(?,?, 'up', 'down', 'fail')`,
		downCheck.ID, time.Now().UTC().UnixMicro()); err != nil {
		t.Fatal(err)
	}
	reloaded, err := db.CheckByID(ctx, downCheck.ID)
	if err != nil {
		t.Fatal(err)
	}

	if err := w.SettleFlaps(ctx, []store.FlapSettle{{Check: reloaded}}); err != nil {
		t.Fatal(err)
	}
	title, message = downRec.text()
	if title != "夜间备份 DOWN" {
		t.Errorf("title = %q", title)
	}
	if !strings.Contains(message, "收到了失败信号") {
		t.Errorf("the settled alert should carry the last down reason:\n%s", message)
	}

	// 暂停/新建的检查项安静收场，不发任何东西。
	quietRec := newReceiver(t, http.StatusOK)
	quietCh := quietRec.channel(t, db, project.ID, store.ChannelConf{Up: true, Down: true})
	quiet := mustCheck(t, db, project.ID, []int64{quietCh.ID}, nil)
	if err := w.SettleFlaps(ctx, []store.FlapSettle{{Check: quiet}}); err != nil {
		t.Fatal(err)
	}
	if quietRec.count() != 0 {
		t.Errorf("a new check should settle silently, got %d deliveries", quietRec.count())
	}

	// 检查项已被删除时 ChannelsForCheck 不会报错，只发不到；
	// 这里用不存在的渠道连接来触发错误分支。
	orphan := &engine.Check{ID: 9999, Code: "gone", Name: "已删除", Status: engine.StatusUp}
	if err := w.SettleFlaps(ctx, []store.FlapSettle{{Check: orphan}}); err != nil {
		t.Errorf("settling an unknown check = %v", err)
	}
}

func TestRetryNotification(t *testing.T) {
	w, db, project := testWorker(t)
	ctx := context.Background()

	rec := newReceiver(t, http.StatusOK)
	ch := rec.channel(t, db, project.ID, store.ChannelConf{Up: true, Down: true})
	c := mustCheck(t, db, project.ID, []int64{ch.ID}, nil)

	// 缺检查项或缺渠道的历史记录没法重试。
	if err := w.RetryNotification(ctx, &store.Notification{}); err == nil {
		t.Error("a notice without check or channel should refuse")
	}
	id := c.ID
	if err := w.RetryNotification(ctx, &store.Notification{CheckID: &id}); err == nil {
		t.Error("a notice without a channel should refuse")
	}

	missing := int64(9999)
	badChannel := missing
	if err := w.RetryNotification(ctx, &store.Notification{CheckID: &id, ChannelID: &badChannel}); err == nil ||
		!strings.Contains(err.Error(), "渠道已删除") {
		t.Errorf("deleted channel = %v", err)
	}
	badCheck := missing
	channelID := ch.ID
	if err := w.RetryNotification(ctx, &store.Notification{CheckID: &badCheck, ChannelID: &channelID}); err == nil ||
		!strings.Contains(err.Error(), "检查项已删除") {
		t.Errorf("deleted check = %v", err)
	}
	notice := &store.Notification{CheckID: &id, ChannelID: &channelID, CheckStatus: engine.StatusDown,
		Created: time.Now().UTC()}
	if err := w.RetryNotification(ctx, notice); err != nil {
		t.Fatal(err)
	}
	if rec.count() != 1 {
		t.Fatalf("delivered %d times", rec.count())
	}
	if title, _ := rec.text(); title != "夜间备份 DOWN" {
		t.Errorf("title = %q", title)
	}

	// 渠道停用时要先让人去页面上启用。
	ch.Disabled = true
	if err := db.UpdateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if err := w.RetryNotification(ctx, notice); err == nil || !strings.Contains(err.Error(), "渠道已停用") {
		t.Errorf("disabled channel = %v", err)
	}
}

func TestRetryNotificationRespectsChannelFilters(t *testing.T) {
	w, db, project := testWorker(t)
	ctx := context.Background()

	rec := newReceiver(t, http.StatusOK)
	upOnly := rec.channel(t, db, project.ID, store.ChannelConf{Up: true})
	downOnly := rec.channel(t, db, project.ID, store.ChannelConf{Down: true})
	c := mustCheck(t, db, project.ID, []int64{upOnly.ID, downOnly.ID}, nil)
	id, upID, downID := c.ID, upOnly.ID, downOnly.ID
	now := time.Now().UTC()

	// 只收恢复的渠道不该被重发出宕机告警。
	if err := w.RetryNotification(ctx, &store.Notification{
		CheckID: &id, ChannelID: &upID, CheckStatus: engine.StatusDown, Created: now,
	}); err == nil || !strings.Contains(err.Error(), "不接收这类通知") {
		t.Errorf("down retry on an up-only channel = %v", err)
	}
	// 抖动通告挂在宕机开关下。
	if err := w.RetryNotification(ctx, &store.Notification{
		CheckID: &id, ChannelID: &upID, CheckStatus: engine.StatusFlap, Created: now,
	}); err == nil || !strings.Contains(err.Error(), "不接收宕机类通知") {
		t.Errorf("flap retry on an up-only channel = %v", err)
	}
	// 稳定通告挂在恢复开关下。
	if err := w.RetryNotification(ctx, &store.Notification{
		CheckID: &id, ChannelID: &downID, CheckStatus: engine.StatusStable, Created: now,
	}); err == nil || !strings.Contains(err.Error(), "不接收恢复类通知") {
		t.Errorf("stable retry on a down-only channel = %v", err)
	}
	if rec.count() != 0 {
		t.Fatalf("filtered retries still delivered %d times", rec.count())
	}

	// 两类都收时，抖动与稳定都能重发。
	both := rec.channel(t, db, project.ID, store.ChannelConf{Up: true, Down: true})
	bothID := both.ID
	for _, status := range []engine.Status{engine.StatusFlap, engine.StatusStable, engine.StatusUp} {
		if err := w.RetryNotification(ctx, &store.Notification{
			CheckID: &id, ChannelID: &bothID, CheckStatus: status, Created: now,
		}); err != nil {
			t.Fatalf("retry %q: %v", status, err)
		}
	}
	if rec.count() != 3 {
		t.Errorf("delivered %d times, want 3", rec.count())
	}
	if title, _ := rec.text(); !strings.Contains(title, "UP") {
		t.Errorf("last title = %q", title)
	}
}

func TestRetryNotificationUsesTheLastDownReason(t *testing.T) {
	w, db, project := testWorker(t)
	ctx := context.Background()

	rec := newReceiver(t, http.StatusOK)
	ch := rec.channel(t, db, project.ID, store.ChannelConf{Down: true})
	c := mustCheck(t, db, project.ID, []int64{ch.ID}, nil)
	id, chID := c.ID, ch.ID

	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO flips(owner_id, created, old_status, new_status, reason) VALUES(?,?, 'up', 'down', 'timeout')`,
		id, time.Now().UTC().UnixMicro()); err != nil {
		t.Fatal(err)
	}

	if err := w.RetryNotification(ctx, &store.Notification{
		CheckID: &id, ChannelID: &chID, CheckStatus: engine.StatusDown, Created: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if _, message := rec.text(); !strings.Contains(message, "成功信号未按时到达") {
		t.Errorf("message should replay the last down reason:\n%s", message)
	}
}

func TestTestMessage(t *testing.T) {
	w, db, project := testWorker(t)
	rec := newReceiver(t, http.StatusOK)
	ch := rec.channel(t, db, project.ID, store.ChannelConf{Up: true, Down: true})
	c := mustCheck(t, db, project.ID, []int64{ch.ID}, nil)

	if err := w.TestMessage(ch, c, engine.StatusUp); err != nil {
		t.Fatal(err)
	}
	title, message := rec.text()
	if title != "夜间备份 UP" {
		t.Errorf("title = %q", title)
	}
	if message != "这是一条测试消息。" {
		t.Errorf("message = %q", message)
	}

	// 测试消息不落通知记录，免得污染历史。
	rows, err := db.NotificationsForCheck(context.Background(), c.ID, time.Now().UTC().Add(-time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("test message wrote %d notification rows", len(rows))
	}

	if err := w.TestMessage(ch, c, engine.StatusDown); err != nil {
		t.Fatal(err)
	}
	if title, _ := rec.text(); title != "夜间备份 DOWN" {
		t.Errorf("title = %q", title)
	}
}

func TestRecordedSendRetriesThenGivesUpWithTheContext(t *testing.T) {
	w, db, project := testWorker(t)
	w.Cfg.NotifyRetries = 1

	// 先起一个接收端再关掉，第一次发送必然快速失败。
	rec := newReceiver(t, http.StatusOK)
	ch := rec.channel(t, db, project.ID, store.ChannelConf{Down: true})
	c := mustCheck(t, db, project.ID, []int64{ch.ID}, nil)
	rec.srv.Close()

	// 退避要等 5 秒，而 ctx 两三百毫秒就到期：走的是「等不到重试窗口」这条路。
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	err := w.Process(ctx, &store.PendingFlip{Check: c, Flip: engine.Flip{
		Created: time.Now().UTC(), NewStatus: engine.StatusDown, Reason: engine.ReasonFail,
	}})
	if err == nil {
		t.Fatal("expected the send to fail")
	}
	if !errorsIsContext(err) {
		t.Errorf("err = %v, want a context deadline", err)
	}
}

func errorsIsContext(err error) bool {
	return strings.Contains(err.Error(), "context deadline exceeded") ||
		strings.Contains(err.Error(), "context canceled")
}
