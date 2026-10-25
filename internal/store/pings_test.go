package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Felix2yu/feixi/internal/engine"
)

func record(t *testing.T, db *DB, checkID int64, action engine.Action, in PingInput) (*engine.Check, *engine.Flip) {
	t.Helper()
	c, f, _, _, err := db.RecordPing(context.Background(), checkID, action, in)
	if err != nil {
		t.Fatalf("record %q: %v", action, err)
	}
	return c, f
}

func TestListPingsPairedDuration(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	c := mkCheck(t, db, p.ID, engine.DefaultTimeout, engine.DefaultGrace)
	ctx := context.Background()

	rid := uuid.NewString()
	record(t, db, c.ID, engine.ActionStart, PingInput{Rid: &rid, Method: "GET"})
	record(t, db, c.ID, engine.ActionSuccess, PingInput{Rid: &rid, Method: "GET"})

	pings, err := db.ListPings(ctx, c.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pings) != 2 {
		t.Fatalf("pings = %d, want 2", len(pings))
	}
	// 最新在前。
	first, second := pings[0], pings[1]
	if first.N != 2 || second.N != 1 {
		t.Errorf("n = %d/%d, want descending 2/1", first.N, second.N)
	}
	if second.Kind != string(engine.ActionStart) {
		t.Errorf("oldest kind = %q, want start", second.Kind)
	}
	// start 就在同一页里，时长直接配对成功。
	if first.Duration == nil {
		t.Fatal("success ping should carry a duration")
	}
	if *first.Duration < 0 || *first.Duration > time.Minute {
		t.Errorf("duration = %v", *first.Duration)
	}
	if second.Duration != nil {
		t.Errorf("start ping should not carry a duration: %v", second.Duration)
	}
	if first.Rid == nil || *first.Rid != rid {
		t.Errorf("rid = %v, want %s", first.Rid, rid)
	}
	if second.Rid == nil || *second.Rid != rid {
		t.Errorf("start rid = %v", second.Rid)
	}
}

func TestListPingsDurationFallsBackOutsideThePage(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	c := mkCheck(t, db, p.ID, engine.DefaultTimeout, engine.DefaultGrace)
	ctx := context.Background()

	rid := uuid.NewString()
	record(t, db, c.ID, engine.ActionStart, PingInput{Rid: &rid})
	record(t, db, c.ID, engine.ActionSuccess, PingInput{Rid: &rid})

	// 只取最新一条，start 落在页外，走回看查询。
	pings, err := db.ListPings(ctx, c.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(pings) != 1 {
		t.Fatalf("pings = %d", len(pings))
	}
	if pings[0].Duration == nil {
		t.Fatal("fallback should find the start outside the page")
	}
}

func TestListPingsDurationWithoutAnyStart(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	c := mkCheck(t, db, p.ID, engine.DefaultTimeout, engine.DefaultGrace)
	ctx := context.Background()

	record(t, db, c.ID, engine.ActionSuccess, PingInput{})
	record(t, db, c.ID, engine.ActionSuccess, PingInput{})

	pings, err := db.ListPings(ctx, c.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	// 回看时先撞上一条同 rid 的 success，说明这一轮根本没有 start。
	if pings[0].Duration != nil {
		t.Errorf("duration = %v, want nil", pings[0].Duration)
	}
	if pings[0].Kind != "" {
		t.Errorf("success pings store a NULL kind, got %q", pings[0].Kind)
	}
}

func TestListPingsGivesUpAfterTooManyMisses(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	c := mkCheck(t, db, p.ID, engine.DefaultTimeout, engine.DefaultGrace)
	ctx := context.Background()

	// 没有 start 可配对，回看次数超过阈值后整页时长一律置空。
	for i := 0; i < 12; i++ {
		record(t, db, c.ID, engine.ActionFail, PingInput{})
	}
	pings, err := db.ListPings(ctx, c.ID, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(pings) != 12 {
		t.Fatalf("pings = %d, want 12", len(pings))
	}
	for _, ping := range pings {
		if ping.Duration != nil {
			t.Fatalf("duration = %v, want every one cleared", ping.Duration)
		}
	}
}

func TestListPingsEmpty(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	c := mkCheck(t, db, p.ID, engine.DefaultTimeout, engine.DefaultGrace)

	pings, err := db.ListPings(context.Background(), c.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pings) != 0 {
		t.Errorf("pings = %v", pings)
	}
}

func TestListPingsCarriesBodyAndExitStatus(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	c := mkCheck(t, db, p.ID, engine.DefaultTimeout, engine.DefaultGrace)
	ctx := context.Background()

	exit := int64(3)
	record(t, db, c.ID, engine.ActionSuccess, PingInput{Body: []byte("payload"), ExitStatus: &exit})

	pings, err := db.ListPings(ctx, c.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if string(pings[0].Body) != "payload" {
		t.Errorf("body = %q", pings[0].Body)
	}
	if pings[0].BodySize != 7 {
		t.Errorf("body_size = %d", pings[0].BodySize)
	}
	if pings[0].ExitStatus == nil || *pings[0].ExitStatus != 3 {
		t.Errorf("exitstatus = %v, want 3", pings[0].ExitStatus)
	}
	if pings[0].CheckID != c.ID || pings[0].ID == 0 {
		t.Errorf("identity = %+v", pings[0])
	}
	if pings[0].Created.IsZero() {
		t.Error("created should round-trip")
	}

	body, err := db.PingBody(ctx, c.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "payload" {
		t.Errorf("PingBody = %q", body)
	}
	if _, err := db.PingBody(ctx, c.ID, 99); err != ErrNotFound {
		t.Errorf("unknown n = %v, want ErrNotFound", err)
	}
}

func TestLastPingBody(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	c := mkCheck(t, db, p.ID, engine.DefaultTimeout, engine.DefaultGrace)
	ctx := context.Background()

	body, exit, err := db.LastPingBody(ctx, c.ID, time.Now().UTC())
	if err != nil || body != nil || exit != nil {
		t.Fatalf("no pings yet = %q/%v/%v", body, exit, err)
	}

	code := int64(7)
	record(t, db, c.ID, engine.ActionSuccess, PingInput{Body: []byte("first"), ExitStatus: &code})
	// log/start 这类不算结果 ping，只有 NULL kind（success）和 fail 会被取到。
	record(t, db, c.ID, engine.ActionLog, PingInput{Body: []byte("ignored")})

	body, gotExit, err := db.LastPingBody(ctx, c.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "first" {
		t.Errorf("body = %q, want the success ping", body)
	}
	if gotExit == nil || *gotExit != 7 {
		t.Errorf("exitstatus = %v, want 7", gotExit)
	}

	record(t, db, c.ID, engine.ActionFail, PingInput{Body: []byte("failed")})
	body, gotExit, err = db.LastPingBody(ctx, c.ID, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "failed" || gotExit != nil {
		t.Errorf("after a fail ping = %q/%v", body, gotExit)
	}

	// 时间窗在第一条之前，什么都取不到。
	body, _, err = db.LastPingBody(ctx, c.ID, time.Now().UTC().Add(-time.Hour))
	if err != nil || body != nil {
		t.Errorf("before window = %q, %v", body, err)
	}
}

func TestPruneAll(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	one := mkCheck(t, db, p.ID, engine.DefaultTimeout, engine.DefaultGrace)
	two := mkCheck(t, db, p.ID, engine.DefaultTimeout, engine.DefaultGrace)
	for i := 0; i < 5; i++ {
		record(t, db, one.ID, engine.ActionSuccess, PingInput{})
		record(t, db, two.ID, engine.ActionLog, PingInput{})
	}

	n, err := db.PruneAll(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("pruned %d checks, want both", n)
	}
	for _, id := range []int64{one.ID, two.ID} {
		pings, err := db.ListPings(ctx, id, 100)
		if err != nil {
			t.Fatal(err)
		}
		if len(pings) != 2 {
			t.Errorf("check %d kept %d pings, want the last 2", id, len(pings))
		}
	}
}

func TestPruneCheckDefaultsAndUnknownCheck(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	// 未知检查项安静地什么都不做。
	if err := db.PruneCheck(ctx, 9999, 10); err != nil {
		t.Errorf("unknown check = %v", err)
	}

	c := mkCheck(t, db, p.ID, engine.DefaultTimeout, engine.DefaultGrace)
	for i := 0; i < 5; i++ {
		record(t, db, c.ID, engine.ActionSuccess, PingInput{})
	}
	// limit<=0 回落到 PingLogLimit，5 条都该留下。
	if err := db.PruneCheck(ctx, c.ID, 0); err != nil {
		t.Fatal(err)
	}
	pings, err := db.ListPings(ctx, c.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(pings) != 5 {
		t.Errorf("pings = %d, want 5 under the default limit", len(pings))
	}
}

func TestPruneCheckTrimsOlderNotificationsAndFlips(t *testing.T) {
	db := testDB(t)
	p := testProject(t, db)
	ctx := context.Background()

	c := mkCheck(t, db, p.ID, engine.DefaultTimeout, engine.DefaultGrace)
	ch := mkChannel(t, db, p.ID, "群", []string{"json://a"}, ChannelConf{})

	// 一条比最早 ping 还旧的通知，一条新的。
	old, err := db.StartNotification(ctx, &c.ID, ch.ID, engine.StatusDown)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx, `UPDATE notifications SET created = ? WHERE id = ?`,
		toDB(time.Now().UTC().Add(-time.Hour)), old.ID); err != nil {
		t.Fatal(err)
	}

	// 一条超出 93 天保留期的翻转。
	record(t, db, c.ID, engine.ActionSuccess, PingInput{})
	ancient := time.Now().UTC().Add(-engine.FlipRetention - 24*time.Hour)
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE flips SET created = ? WHERE owner_id = ?`, toDB(ancient), c.ID); err != nil {
		t.Fatal(err)
	}

	fresh, err := db.StartNotification(ctx, &c.ID, ch.ID, engine.StatusUp)
	if err != nil {
		t.Fatal(err)
	}

	if err := db.PruneCheck(ctx, c.ID, 100); err != nil {
		t.Fatal(err)
	}

	if _, err := db.NotificationByCode(ctx, old.Code); err != ErrNotFound {
		t.Errorf("notification older than the first ping survived: %v", err)
	}
	if _, err := db.NotificationByCode(ctx, fresh.Code); err != nil {
		t.Errorf("recent notification was pruned: %v", err)
	}

	flips, err := db.Flips(ctx, c.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(flips) != 0 {
		t.Errorf("flips past the retention window survived: %+v", flips)
	}
}
