package daemon

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/yufei/feixi/internal/config"
	"github.com/yufei/feixi/internal/cronex"
	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/store"
)

type fakeNotifier struct {
	mu        sync.Mutex
	processed []*store.PendingFlip
	nagged    []store.NagCandidate
	settled   [][]store.FlapSettle

	processErr error
	nagErr     error
	settleErr  error
}

func (f *fakeNotifier) Process(ctx context.Context, flip *store.PendingFlip) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.processed = append(f.processed, flip)
	return f.processErr
}

func (f *fakeNotifier) Nag(ctx context.Context, cand store.NagCandidate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nagged = append(f.nagged, cand)
	return f.nagErr
}

func (f *fakeNotifier) SettleFlaps(ctx context.Context, settles []store.FlapSettle) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settled = append(f.settled, settles)
	return f.settleErr
}

func (f *fakeNotifier) counts() (int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.processed), len(f.nagged), len(f.settled)
}

func testDaemon(t *testing.T) (*Daemon, *store.DB, *fakeNotifier) {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "daemon.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.Sched = cronex.Scheduler{}
	db.MaxChecks = 100
	db.PingLogLimit = 100

	cfg := config.Default()
	cfg.AlertScanInterval = 10 * time.Millisecond
	cfg.FlipIdleSleep = 5 * time.Millisecond
	cfg.NagScanInterval = 10 * time.Millisecond
	cfg.FlapScanInterval = 10 * time.Millisecond

	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if _, err := db.Seed(context.Background(), "test-secret"); err != nil {
		t.Fatal(err)
	}

	d := New(db, cfg, slog.Default())
	notifier := &fakeNotifier{}
	d.Notifier = notifier
	return d, db, notifier
}

func project(t *testing.T, db *store.DB) *store.Project {
	t.Helper()
	p, err := db.Project(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNewUsesANoopNotifier(t *testing.T) {
	d, db, _ := testDaemon(t)
	if _, ok := d.Notifier.(*fakeNotifier); !ok {
		t.Fatalf("Notifier = %T", d.Notifier)
	}
	fresh := New(db, d.Cfg, slog.Default())
	noop, ok := fresh.Notifier.(noopNotifier)
	if !ok {
		t.Fatalf("default Notifier = %T, want noopNotifier", fresh.Notifier)
	}
	// 没配通知器时三条路径都得安静地返回 nil。
	if err := noop.Process(context.Background(), &store.PendingFlip{}); err != nil {
		t.Errorf("Process = %v", err)
	}
	if err := noop.Nag(context.Background(), store.NagCandidate{}); err != nil {
		t.Errorf("Nag = %v", err)
	}
	if err := noop.SettleFlaps(context.Background(), nil); err != nil {
		t.Errorf("SettleFlaps = %v", err)
	}
	// 没接通知器时也得能跑完整条链路而不 panic。
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	fresh.Run(ctx)
	time.Sleep(60 * time.Millisecond)
}

func TestQueuePruneDropsWhenFull(t *testing.T) {
	d, _, _ := testDaemon(t)

	for i := int64(0); i < 128; i++ {
		d.QueuePrune(i)
	}
	done := make(chan struct{})
	go func() {
		// 队列满了也必须立刻返回，不能让 ping 请求等清理。
		d.QueuePrune(999)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("QueuePrune blocked on a full channel")
	}
}

func TestSweepTurnsExpiredChecksDown(t *testing.T) {
	d, db, _ := testDaemon(t)
	ctx := context.Background()
	p := project(t, db)

	c, err := db.CreateCheck(ctx, &store.NewCheck{
		ProjectID: p.ID, Name: "任务", Kind: engine.KindSimple,
		Timeout: time.Hour, Grace: time.Hour, TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	// 先转 up，再把截止时间挪到过去，模拟超时未 ping。
	if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionSuccess, store.PingInput{}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().UTC().Add(-time.Minute)
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE checks SET status = 'up', last_ping = ?, alert_after = ? WHERE id = ?`,
		deadline.Add(-2*time.Hour).UnixMicro(), deadline.UnixMicro(), c.ID); err != nil {
		t.Fatal(err)
	}

	d.sweep(ctx)

	stored, err := db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != engine.StatusDown {
		t.Errorf("status = %q, want down", stored.Status)
	}
	if stored.AlertAfter != nil {
		t.Errorf("alert_after = %v, want cleared", stored.AlertAfter)
	}

	// 再扫一次没有可认领的行，也不该报错。
	d.sweep(ctx)

	// ctx 已取消时立刻收工。
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	d.sweep(cancelled)
}

func TestSweepLogsAndStopsOnScanError(t *testing.T) {
	d, db, _ := testDaemon(t)
	ctx := context.Background()
	p := project(t, db)

	c, err := db.CreateCheck(ctx, &store.NewCheck{
		ProjectID: p.ID, Name: "坏日程", Kind: engine.KindCron,
		Timeout: time.Hour, Grace: time.Hour, Schedule: "0 3 * * *", TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionSuccess, store.PingInput{}); err != nil {
		t.Fatal(err)
	}
	// 时区改坏：ClaimGoingDown 会返回错误，扫描只能记日志并退出这一轮。
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE checks SET tz = 'Mars/Olympus', alert_after = ? WHERE id = ?`,
		time.Now().UTC().Add(-time.Minute).UnixMicro(), c.ID); err != nil {
		t.Fatal(err)
	}

	d.sweep(ctx)

	stored, err := db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status == engine.StatusDown {
		t.Error("an unreadable schedule must not be judged down")
	}
}

func TestDrainFlipsDeliversAndIdles(t *testing.T) {
	d, db, notifier := testDaemon(t)
	ctx := context.Background()
	p := project(t, db)

	c, err := db.CreateCheck(ctx, &store.NewCheck{
		ProjectID: p.ID, Name: "任务", Kind: engine.KindSimple,
		Timeout: time.Hour, Grace: time.Hour, TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionSuccess, store.PingInput{}); err != nil {
		t.Fatal(err)
	}

	run, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		d.drainFlips(run)
		close(done)
	}()

	waitFor(t, func() bool {
		n, _, _ := notifier.counts()
		return n == 1
	}, "the flip should be delivered")

	// 队列空了以后是 sleep-轮询，不是忙等；处理标记仍然只有一条。
	time.Sleep(50 * time.Millisecond)
	if n, _, _ := notifier.counts(); n != 1 {
		t.Errorf("processed = %d, want exactly 1", n)
	}

	flips, err := db.Flips(ctx, c.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(flips) != 1 {
		t.Fatalf("flips = %d", len(flips))
	}
	// 认领成功后 processed 一定被写上了时间戳。
	var processed int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT processed FROM flips WHERE owner_id = ?`, c.ID).Scan(&processed); err != nil {
		t.Fatal(err)
	}
	if processed == 0 {
		t.Error("the claimed flip should be marked processed")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("drainFlips did not stop")
	}
}

func TestDrainFlipsKeepsGoingAfterAFailure(t *testing.T) {
	d, db, notifier := testDaemon(t)
	ctx := context.Background()
	p := project(t, db)

	notifier.processErr = errFake
	for _, name := range []string{"甲", "乙"} {
		c, err := db.CreateCheck(ctx, &store.NewCheck{
			ProjectID: p.ID, Name: name, Kind: engine.KindSimple,
			Timeout: time.Hour, Grace: time.Hour, TZ: "UTC",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionFail, store.PingInput{}); err != nil {
			t.Fatal(err)
		}
	}

	run, cancel := context.WithCancel(ctx)
	defer cancel()
	go d.drainFlips(run)

	waitFor(t, func() bool {
		n, _, _ := notifier.counts()
		return n == 2
	}, "both flips should be attempted even when delivery fails")
}

func TestNagLoopIsOffWithoutAPeriod(t *testing.T) {
	d, _, notifier := testDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.nagLoop(ctx)
		close(done)
	}()
	// NagPeriod<=0 时根本不进循环。
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("nagLoop should return once the period is disabled")
	}
	if _, n, _ := notifier.counts(); n != 0 {
		t.Errorf("nags = %d", n)
	}
}

func TestSweepNagsAndFlaps(t *testing.T) {
	d, db, notifier := testDaemon(t)
	ctx := context.Background()
	p := project(t, db)

	// 一个宕机满一个周期、且渠道开了催促的检查项。
	ch, err := db.CreateChannel(ctx, &store.NewChannel{
		ProjectID: p.ID, Name: "群", URLs: []string{"json://one"},
		Conf: store.ChannelConf{Down: true, Nag: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	c, err := db.CreateCheck(ctx, &store.NewCheck{
		ProjectID: p.ID, Name: "任务", Kind: engine.KindSimple,
		Timeout: time.Hour, Grace: time.Hour, TZ: "UTC", ChannelIDs: []int64{ch.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionFail, store.PingInput{}); err != nil {
		t.Fatal(err)
	}
	id := c.ID
	notice, err := db.StartNotification(ctx, &id, ch.ID, engine.StatusDown)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishNotification(ctx, notice.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE notifications SET created = ? WHERE id = ?`,
		time.Now().UTC().Add(-2*time.Hour).UnixMicro(), notice.ID); err != nil {
		t.Fatal(err)
	}

	d.Cfg.NagPeriod = time.Hour
	d.sweepNags(ctx)
	if _, n, _ := notifier.counts(); n != 1 {
		t.Fatalf("nags = %d, want the due candidate", n)
	}

	// Nag 报错只记日志，不影响下一轮。
	notifier.nagErr = errFake
	d.sweepNags(ctx)
	if _, n, _ := notifier.counts(); n != 2 {
		t.Errorf("nags after a failure = %d", n)
	}

	// 没有待结算的抖动时不该调用 SettleFlaps。
	d.Cfg.FlapWindow = time.Hour
	d.sweepFlaps(ctx)
	if _, _, s := notifier.counts(); s != 0 {
		t.Errorf("settled = %d, want nothing to settle", s)
	}

	// 结算看的是最后一次告警性翻转，所以把翻转挪到一个窗口之前。
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE checks SET flapping = 1, flap_since = ? WHERE id = ?`,
		time.Now().UTC().Add(-2*time.Hour).UnixMicro(), c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE flips SET created = ? WHERE owner_id = ?`,
		time.Now().UTC().Add(-2*time.Hour).UnixMicro(), c.ID); err != nil {
		t.Fatal(err)
	}
	d.sweepFlaps(ctx)
	if _, _, s := notifier.counts(); s != 1 {
		t.Fatalf("settled = %d, want one batch", s)
	}

	// 结算失败也只是日志，标记已经在查询时被清掉了。
	notifier.settleErr = errFake
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE checks SET flapping = 1, flap_since = ? WHERE id = ?`,
		time.Now().UTC().Add(-2*time.Hour).UnixMicro(), c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE flips SET created = ? WHERE owner_id = ?`,
		time.Now().UTC().Add(-2*time.Hour).UnixMicro(), c.ID); err != nil {
		t.Fatal(err)
	}
	d.sweepFlaps(ctx)
	if _, _, s := notifier.counts(); s != 2 {
		t.Errorf("settled = %d", s)
	}
}

func TestFlapLoopIsOffWithoutAThreshold(t *testing.T) {
	d, _, _ := testDaemon(t)
	d.Cfg.FlapThreshold = 0

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.flapLoop(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("flapLoop should return once the threshold is disabled")
	}
}

func TestLoopsRunAndStop(t *testing.T) {
	d, db, notifier := testDaemon(t)
	d.Cfg.NagPeriod = time.Hour
	ctx, cancel := context.WithCancel(context.Background())
	d.Run(ctx)

	// 跑一个 ticker 周期，让四个循环各自至少触发一次。
	time.Sleep(60 * time.Millisecond)
	p := project(t, db)
	c, err := db.CreateCheck(ctx, &store.NewCheck{
		ProjectID: p.ID, Name: "任务", Kind: engine.KindSimple,
		Timeout: time.Hour, Grace: time.Hour, TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionSuccess, store.PingInput{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		n, _, _ := notifier.counts()
		return n >= 1
	}, "the queue loop should pick up the flip")

	cancel()
	time.Sleep(30 * time.Millisecond)
}

func TestPruneLoopTrimsWhenAsked(t *testing.T) {
	d, db, _ := testDaemon(t)
	ctx := context.Background()
	p := project(t, db)

	d.Cfg.PingLogLimit = 2
	c, err := db.CreateCheck(ctx, &store.NewCheck{
		ProjectID: p.ID, Name: "任务", Kind: engine.KindSimple,
		Timeout: time.Hour, Grace: time.Hour, TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionLog, store.PingInput{}); err != nil {
			t.Fatal(err)
		}
	}

	run, cancel := context.WithCancel(ctx)
	defer cancel()
	go d.pruneLoop(run)

	d.QueuePrune(c.ID)
	waitFor(t, func() bool {
		rows, err := db.ListPings(ctx, c.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(rows) == 2
	}, "the queued prune should trim the log")

	// 未知检查项的清理不报错，后面的排队请求也照样处理。
	second, err := db.CreateCheck(ctx, &store.NewCheck{
		ProjectID: p.ID, Name: "另一个任务", Kind: engine.KindSimple,
		Timeout: time.Hour, Grace: time.Hour, TZ: "UTC",
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, _, _, _, err := db.RecordPing(ctx, second.ID, engine.ActionLog, store.PingInput{}); err != nil {
			t.Fatal(err)
		}
	}
	d.QueuePrune(9999)
	d.QueuePrune(second.ID)
	waitFor(t, func() bool {
		rows, err := db.ListPings(ctx, second.ID, 100)
		if err != nil {
			t.Fatal(err)
		}
		return len(rows) == 2
	}, "the loop should survive an unknown check id and keep pruning")
}

func TestSleepHonoursContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	sleep(ctx, time.Hour)
	if time.Since(start) > time.Second {
		t.Errorf("sleep waited %v despite a cancelled context", time.Since(start))
	}

	done := make(chan struct{})
	go func() {
		sleep(context.Background(), 10*time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("sleep did not return")
	}
}

var errFake = &fakeErr{}

type fakeErr struct{}

func (*fakeErr) Error() string { return "模拟发送失败" }

func waitFor(t *testing.T, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out: %s", msg)
}
