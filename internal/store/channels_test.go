package store

import (
	"context"
	"testing"
	"time"

	"github.com/yufei/feixi/internal/engine"
)

func mkChannel(t *testing.T, db *DB, projectID int64, name string, urls []string, conf ChannelConf) *Channel {
	t.Helper()
	ch, err := db.CreateChannel(context.Background(), &NewChannel{
		ProjectID: projectID, Name: name, URLs: urls, Conf: conf,
	})
	if err != nil {
		t.Fatalf("create channel %q: %v", name, err)
	}
	return ch
}

func TestChannelIsNoop(t *testing.T) {
	tests := []struct {
		name string
		conf ChannelConf
		want map[engine.Status]bool
	}{
		{"both on", ChannelConf{Up: true, Down: true}, map[engine.Status]bool{
			engine.StatusUp: false, engine.StatusDown: false, engine.StatusNew: true, engine.StatusPaused: true,
		}},
		{"up only", ChannelConf{Up: true}, map[engine.Status]bool{
			engine.StatusUp: false, engine.StatusDown: true,
		}},
		{"down only", ChannelConf{Down: true}, map[engine.Status]bool{
			engine.StatusUp: true, engine.StatusDown: false,
		}},
		{"muted", ChannelConf{}, map[engine.Status]bool{
			engine.StatusUp: true, engine.StatusDown: true,
		}},
	}
	for _, tt := range tests {
		ch := &Channel{Conf: tt.conf}
		for status, want := range tt.want {
			if got := ch.IsNoop(status); got != want {
				t.Errorf("%s: IsNoop(%q) = %v, want %v", tt.name, status, got, want)
			}
		}
	}
}

func TestChannelValueJoinsURLs(t *testing.T) {
	ch := &Channel{URLs: []string{"json://a", "mailto://b"}}
	if got := ch.Value(); got != "json://a\nmailto://b" {
		t.Errorf("Value = %q", got)
	}
	if got := (&Channel{}).Value(); got != "" {
		t.Errorf("empty channel = %q", got)
	}
}

func TestChannelCRUD(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	ch := mkChannel(t, db, p.ID, "值班群", []string{"json://one", "json://two"}, ChannelConf{Up: true, Nag: true})
	if ch.Name != "值班群" || len(ch.URLs) != 2 || !ch.Conf.Up || !ch.Conf.Nag {
		t.Fatalf("created channel = %+v", ch)
	}
	if ch.Conf.Down {
		t.Error("down should default to false when not set")
	}
	if ch.Created.IsZero() {
		t.Error("created should round-trip")
	}

	byID, err := db.ChannelByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if byID.Code != ch.Code {
		t.Errorf("ChannelByID code = %q, want %q", byID.Code, ch.Code)
	}
	if _, err := db.ChannelByID(ctx, 9999); err != ErrNotFound {
		t.Errorf("unknown id = %v, want ErrNotFound", err)
	}
	if _, err := db.ChannelByCode(ctx, "nope"); err != ErrNotFound {
		t.Errorf("unknown code = %v, want ErrNotFound", err)
	}

	list, err := db.Channels(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("Channels = %d, want 1", len(list))
	}
	if other, err := db.Channels(ctx, p.ID+5); err != nil || len(other) != 0 {
		t.Errorf("other project channels = %v, %v", other, err)
	}

	ch.Name = "改名"
	ch.URLs = []string{"json://only"}
	ch.Conf = ChannelConf{Down: true}
	ch.Disabled = true
	if err := db.UpdateChannel(ctx, ch); err != nil {
		t.Fatal(err)
	}
	after, err := db.ChannelByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Name != "改名" || len(after.URLs) != 1 || after.URLs[0] != "json://only" {
		t.Errorf("update did not land: %+v", after)
	}
	if after.Conf.Up || !after.Conf.Down || !after.Disabled {
		t.Errorf("conf/disabled round-trip: %+v", after)
	}

	if err := db.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ChannelByID(ctx, ch.ID); err != ErrNotFound {
		t.Errorf("after delete = %v", err)
	}
	if err := db.DeleteChannel(ctx, ch.ID); err != ErrNotFound {
		t.Errorf("deleting twice = %v, want ErrNotFound", err)
	}
}

func TestChannelDeleteUnassignsChecks(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	ch := mkChannel(t, db, p.ID, "群", []string{"json://a"}, ChannelConf{})
	c := newCheck(t, db, p.ID, "任务", "", "")
	if err := db.SetCheckChannels(ctx, c.ID, []int64{ch.ID}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteChannel(ctx, ch.ID); err != nil {
		t.Fatal(err)
	}
	assigned, err := db.CheckChannels(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assigned) != 0 {
		t.Errorf("channel_checks survived the delete: %v", assigned)
	}
}

func TestScanChannelFallsBackOnBrokenConf(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	ch := mkChannel(t, db, p.ID, "坏配置", []string{"json://a", "", "  "}, ChannelConf{Up: true})
	// 历史上的脏数据不能让读取整个失败。
	if _, err := db.Write.ExecContext(ctx, `UPDATE channels SET conf = 'not json' WHERE id = ?`, ch.ID); err != nil {
		t.Fatal(err)
	}

	loaded, err := db.ChannelByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Conf.Up || !loaded.Conf.Down {
		t.Errorf("broken conf should fall back to both enabled, got %+v", loaded.Conf)
	}
	// splitLines 会丢掉空行和空白。
	if len(loaded.URLs) != 1 || loaded.URLs[0] != "json://a" {
		t.Errorf("urls = %v, want the single non-blank entry", loaded.URLs)
	}
}

func TestRecordAttempt(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	ch := mkChannel(t, db, p.ID, "群", []string{"json://a"}, ChannelConf{})
	at := time.Now().UTC().Truncate(time.Microsecond)

	if err := db.RecordAttempt(ctx, ch.ID, at, 250*time.Millisecond, "", false); err != nil {
		t.Fatal(err)
	}
	after, err := db.ChannelByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastNotify == nil || !after.LastNotify.Equal(at) {
		t.Errorf("last_notify = %v, want %v", after.LastNotify, at)
	}
	if after.LastNotifyDuration == nil || *after.LastNotifyDuration != 250*time.Millisecond {
		t.Errorf("duration = %v", after.LastNotifyDuration)
	}
	if after.Disabled {
		t.Error("a clean attempt must not disable the channel")
	}

	if err := db.RecordAttempt(ctx, ch.ID, at, time.Second, "403 Forbidden", true); err != nil {
		t.Fatal(err)
	}
	after, err = db.ChannelByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !after.Disabled || after.LastError != "403 Forbidden" {
		t.Errorf("failed attempt should disable and record the error: %+v", after)
	}

	// 未知渠道只会零行更新，不报错。
	if err := db.RecordAttempt(ctx, 9999, at, time.Second, "", false); err != nil {
		t.Errorf("unknown channel = %v", err)
	}
}

func TestNotifications(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	ch := mkChannel(t, db, p.ID, "群", []string{"json://a"}, ChannelConf{})
	c := newCheck(t, db, p.ID, "任务", "", "")

	checkID := c.ID
	noted, err := db.StartNotification(ctx, &checkID, ch.ID, engine.StatusDown)
	if err != nil {
		t.Fatal(err)
	}
	if noted.ID == 0 || noted.Code == "" || noted.CheckStatus != engine.StatusDown {
		t.Fatalf("StartNotification = %+v", noted)
	}
	projectWide, err := db.StartNotification(ctx, nil, ch.ID, engine.StatusUp)
	if err != nil {
		t.Fatal(err)
	}
	if projectWide.CheckID != nil {
		t.Errorf("project-level notice should have no check: %+v", projectWide.CheckID)
	}

	list, err := db.Notifications(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 新在前，且 JOIN 出了检查项与渠道名。
	if len(list) != 2 || list[0].Code != projectWide.Code {
		t.Fatalf("Notifications = %+v", list)
	}
	if list[1].CheckName != "任务" || list[1].ChannelName != "群" {
		t.Errorf("joined names = %+v", list[1])
	}
	if list[1].Error != "Sending" {
		t.Errorf("fresh notice error = %q, want Sending", list[1].Error)
	}
	if list[1].Created.IsZero() {
		t.Error("created should round-trip")
	}

	if limited, err := db.Notifications(ctx, 1); err != nil || len(limited) != 1 {
		t.Errorf("limit = %d, %v", len(limited), err)
	}

	byCode, err := db.NotificationByCode(ctx, noted.Code)
	if err != nil {
		t.Fatal(err)
	}
	if byCode.ID != noted.ID || byCode.ChannelID == nil || *byCode.ChannelID != ch.ID {
		t.Errorf("NotificationByCode = %+v", byCode)
	}
	if byCode.CheckID == nil || *byCode.CheckID != c.ID {
		t.Errorf("check id = %v", byCode.CheckID)
	}
	if _, err := db.NotificationByCode(ctx, "ghost"); err != ErrNotFound {
		t.Errorf("unknown code = %v, want ErrNotFound", err)
	}

	if err := db.FinishNotification(ctx, noted.ID, ""); err != nil {
		t.Fatal(err)
	}
	finished, err := db.NotificationByCode(ctx, noted.Code)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Error != "" {
		t.Errorf("after finish error = %q", finished.Error)
	}

	since := time.Now().UTC().Add(-time.Hour)
	forCheck, err := db.NotificationsForCheck(ctx, c.ID, since, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(forCheck) != 1 || forCheck[0].Code != noted.Code {
		t.Errorf("NotificationsForCheck = %+v", forCheck)
	}
	if future, err := db.NotificationsForCheck(ctx, c.ID, time.Now().UTC().Add(time.Hour), 10); err != nil || len(future) != 0 {
		t.Errorf("since in the future should be empty: %v, %v", future, err)
	}
}

func TestSetNotificationError(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	ch := mkChannel(t, db, p.ID, "群", []string{"json://a"}, ChannelConf{})
	c := newCheck(t, db, p.ID, "任务", "", "")
	checkID := c.ID

	note, err := db.StartNotification(ctx, &checkID, ch.ID, engine.StatusDown)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.StartNotification(ctx, nil, ch.ID, engine.StatusUp); err != nil {
		t.Fatal(err)
	}

	updated, matched, err := db.SetNotificationError(ctx, note.Code, "3rd party said no", true)
	if err != nil {
		t.Fatal(err)
	}
	if !matched {
		t.Fatal("the notice should have matched")
	}
	if updated.CheckName != "任务" || updated.CheckStatus != engine.StatusDown {
		t.Errorf("returned notice = %+v", updated)
	}
	if updated.ID != note.ID || updated.Code != note.Code {
		t.Errorf("returned identity = %+v", updated)
	}
	if updated.Created.IsZero() {
		t.Error("created should round-trip")
	}

	after, err := db.NotificationByCode(ctx, note.Code)
	if err != nil {
		t.Fatal(err)
	}
	if after.Error != "3rd party said no" {
		t.Errorf("error = %q", after.Error)
	}
	channel, err := db.ChannelByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !channel.Disabled || channel.LastError != "3rd party said no" {
		t.Errorf("markDisabled should disable the channel: %+v", channel)
	}

	// 未知 code 不报错，只报告没匹配上。
	if _, matched, err := db.SetNotificationError(ctx, "ghost", "x", true); err != nil || matched {
		t.Errorf("unknown code = %v, %v", matched, err)
	}

	// 项目级通知没有 owner_id，检查项名必须当成空串而不是 NULL 扫出来。
	wide, err := db.StartNotification(ctx, nil, ch.ID, engine.StatusUp)
	if err != nil {
		t.Fatal(err)
	}
	returned, matched, err := db.SetNotificationError(ctx, wide.Code, "delivered", false)
	if err != nil {
		t.Fatalf("project-wide notice: %v", err)
	}
	if !matched || returned.CheckName != "" || returned.CheckID != nil {
		t.Errorf("project-wide notice = %+v, matched=%v", returned, matched)
	}
	if wide.ChannelID == nil || *wide.ChannelID != ch.ID {
		t.Errorf("StartNotification should echo the channel: %+v", wide.ChannelID)
	}
}

func TestSetNotificationErrorIgnoresStaleReports(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	ch := mkChannel(t, db, p.ID, "群", []string{"json://a"}, ChannelConf{})
	note, err := db.StartNotification(ctx, nil, ch.ID, engine.StatusUp)
	if err != nil {
		t.Fatal(err)
	}
	// 一小时前的送达回执属于重复上报，不该再改任何状态。
	if _, err := db.Write.ExecContext(ctx, `UPDATE notifications SET created = ? WHERE id = ?`,
		toDB(time.Now().UTC().Add(-2*time.Hour)), note.ID); err != nil {
		t.Fatal(err)
	}

	_, matched, err := db.SetNotificationError(ctx, note.Code, "late report", true)
	if err != nil {
		t.Fatal(err)
	}
	if matched {
		t.Error("a stale report must not match")
	}
	after, err := db.NotificationByCode(ctx, note.Code)
	if err != nil {
		t.Fatal(err)
	}
	if after.Error != "Sending" {
		t.Errorf("stale report rewrote the notice: %q", after.Error)
	}
	channel, err := db.ChannelByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if channel.Disabled {
		t.Error("stale report disabled the channel")
	}
}

func TestLastDeliveredNotice(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	ch := mkChannel(t, db, p.ID, "群", []string{"json://a"}, ChannelConf{})
	c := newCheck(t, db, p.ID, "任务", "", "")
	checkID := c.ID

	none, err := db.LastDeliveredNotice(ctx, c.ID, ch.ID, engine.StatusDown)
	if err != nil {
		t.Fatal(err)
	}
	if none != nil {
		t.Errorf("no notice yet = %v, want nil", none)
	}

	note, err := db.StartNotification(ctx, &checkID, ch.ID, engine.StatusDown)
	if err != nil {
		t.Fatal(err)
	}
	// 还在 Sending 的不算送达。
	if at, err := db.LastDeliveredNotice(ctx, c.ID, ch.ID, engine.StatusDown); err != nil || at != nil {
		t.Errorf("sending notice = %v, %v", at, err)
	}

	if err := db.FinishNotification(ctx, note.ID, ""); err != nil {
		t.Fatal(err)
	}
	at, err := db.LastDeliveredNotice(ctx, c.ID, ch.ID, engine.StatusDown)
	if err != nil {
		t.Fatal(err)
	}
	if at == nil {
		t.Fatal("delivered notice should be found")
	}
	if time.Since(*at) > time.Minute {
		t.Errorf("delivered at = %v", *at)
	}

	// 失败的投递记录不算。
	failed, err := db.StartNotification(ctx, &checkID, ch.ID, engine.StatusUp)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishNotification(ctx, failed.ID, "boom"); err != nil {
		t.Fatal(err)
	}
	if at, err := db.LastDeliveredNotice(ctx, c.ID, ch.ID, engine.StatusUp); err != nil || at != nil {
		t.Errorf("errored notice should not count as delivered: %v, %v", at, err)
	}
	// 别的渠道查不到这条检查项的记录。
	other := mkChannel(t, db, p.ID, "另一个群", []string{"json://b"}, ChannelConf{})
	if at, err := db.LastDeliveredNotice(ctx, c.ID, other.ID, engine.StatusDown); err != nil || at != nil {
		t.Errorf("other channel = %v, %v", at, err)
	}
}

func TestChannelsForCheckSkipsDisabled(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	fast := mkChannel(t, db, p.ID, "快", []string{"json://a"}, ChannelConf{})
	slow := mkChannel(t, db, p.ID, "慢", []string{"json://b"}, ChannelConf{})
	gone := mkChannel(t, db, p.ID, "停用", []string{"json://c"}, ChannelConf{})

	c := newCheck(t, db, p.ID, "任务", "", "")
	if err := db.SetCheckChannels(ctx, c.ID, []int64{slow.ID, fast.ID, gone.ID}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := db.RecordAttempt(ctx, fast.ID, at, 10*time.Millisecond, "", false); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordAttempt(ctx, slow.ID, at, 5*time.Second, "", false); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordAttempt(ctx, gone.ID, at, time.Millisecond, "", true); err != nil {
		t.Fatal(err)
	}

	list, err := db.ChannelsForCheck(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("channels = %d, want the two enabled ones", len(list))
	}
	// 快的排前面，慢渠道不能拖住整条告警。
	if list[0].ID != fast.ID || list[1].ID != slow.ID {
		t.Errorf("order = %v, want fast before slow", []int64{list[0].ID, list[1].ID})
	}
}
