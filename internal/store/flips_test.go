package store

import (
	"context"
	"testing"
	"time"

	"github.com/yufei/feixi/internal/engine"
)

func TestFlipsRangeFilters(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	c := mkCheck(t, db, p.ID, engine.DefaultTimeout, engine.DefaultGrace)
	ctx := context.Background()

	record(t, db, c.ID, engine.ActionSuccess, PingInput{})
	record(t, db, c.ID, engine.ActionFail, PingInput{})
	record(t, db, c.ID, engine.ActionSuccess, PingInput{})

	// 三条翻转都发生在同一毫秒附近，把 created 拉开才测得动区间。
	// 截到微秒：库里存的是 UnixMicro，而 CI 的 Linux 时钟有纳秒精度、尾数几乎必非零，
	// 不截就是 Linux 上必红、macOS 上必绿（本机时钟只到微秒，实测 20000 次采样尾数全为 0）。
	anchor := time.Now().UTC().Add(-3 * time.Hour).Truncate(time.Microsecond)
	flips, err := db.Flips(ctx, c.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(flips) != 3 {
		t.Fatalf("flips = %d, want 3", len(flips))
	}
	for i := range flips {
		if _, err := db.Write.ExecContext(ctx,
			`UPDATE flips SET created = ? WHERE owner_id = ? AND id = (
				SELECT id FROM flips WHERE owner_id = ? ORDER BY id LIMIT 1 OFFSET ?)`,
			toDB(anchor.Add(time.Duration(i)*time.Hour)), c.ID, c.ID, i); err != nil {
			t.Fatal(err)
		}
	}

	all, err := db.Flips(ctx, c.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("all = %d", len(all))
	}
	// created 升序，Up 标志跟着 new_status。
	if !all[0].Up || all[1].Up || !all[2].Up {
		t.Errorf("up flags = %v", []bool{all[0].Up, all[1].Up, all[2].Up})
	}
	if all[1].NewStatus != engine.StatusDown || all[1].Reason != engine.ReasonFail {
		t.Errorf("middle flip = %+v", all[1])
	}
	if all[0].OldStatus != engine.StatusNew {
		t.Errorf("first flip should leave new, got %+v", all[0])
	}
	if !all[0].Timestamp.Equal(anchor) {
		t.Errorf("timestamp = %v, want %v", all[0].Timestamp, anchor)
	}

	// 三条翻转分别落在 anchor、+1h、+2h。
	start := anchor.Add(90 * time.Minute)
	fromMiddle, err := db.Flips(ctx, c.ID, &start, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(fromMiddle) != 1 {
		t.Errorf("start filter = %d, want only the last flip", len(fromMiddle))
	}

	end := anchor.Add(90 * time.Minute)
	untilMiddle, err := db.Flips(ctx, c.ID, nil, &end)
	if err != nil {
		t.Fatal(err)
	}
	if len(untilMiddle) != 2 {
		t.Errorf("end filter = %d, want the first two flips", len(untilMiddle))
	}

	none, err := db.Flips(ctx, c.ID, &start, &end)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Errorf("start after end = %+v, want empty", none)
	}

	other, err := db.Flips(ctx, c.ID+999, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 0 {
		t.Errorf("unknown check returned %d flips", len(other))
	}
}

func TestClaimGoingDownOnBrokenSchedulePushesDeadline(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	// 状态机只在 up 且有过 ping 的 cron 检查项上算下次到期，
	// 所以先用合法日程让它转 up，再把日程改坏。
	c, err := db.CreateCheck(ctx, &NewCheck{
		ProjectID: p.ID, Name: "坏日程", Kind: engine.KindCron,
		Timeout: engine.DefaultTimeout, Grace: engine.DefaultGrace,
		Schedule: "0 3 * * *", TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	record(t, db, c.ID, engine.ActionSuccess, PingInput{})

	c, err = db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Schedule = "not a cron"
	deadline := time.Now().UTC().Add(-time.Minute)
	c.AlertAfter = &deadline
	if err := db.UpdateCheck(ctx, c); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()

	flip, err := db.ClaimGoingDown(ctx, now)
	if err == nil {
		t.Fatal("a broken schedule should surface an error")
	}
	if flip != nil {
		t.Errorf("flip = %+v, want none", flip)
	}

	after, err := db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status == engine.StatusDown {
		t.Error("a check with an unreadable schedule must not be flipped down")
	}
	// 注意：推后 alert_after 的写入和这次事务一起回滚了，所以同一个坏日程
	// 每轮扫描都会再撞上一次。这里只固化「不转 down、如实报错」的行为。

	// 坏日程的检查项仍然会被扫到（见上面的回滚说明）。
	if flip, err := db.ClaimGoingDown(ctx, now); err == nil || flip != nil {
		t.Errorf("second pass = %+v, %v", flip, err)
	}
}
