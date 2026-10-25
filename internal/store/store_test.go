package store

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Felix2yu/feixi/internal/cronex"
	"github.com/Felix2yu/feixi/internal/engine"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	db.Sched = cronex.Scheduler{}
	db.MaxChecks = 100
	db.PingLogLimit = 100

	ctx := context.Background()
	if _, err := db.Seed(ctx, "test-secret"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return db
}

func testProject(t *testing.T, db *DB) *Project {
	t.Helper()
	p, err := db.Project(context.Background())
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	return p
}

func mkCheck(t *testing.T, db *DB, projectID int64, timeout, grace time.Duration) *engine.Check {
	t.Helper()
	c, err := db.CreateCheck(context.Background(), &NewCheck{
		ProjectID: projectID,
		Name:      "测试",
		Kind:      engine.KindSimple,
		Timeout:   timeout,
		Grace:     grace,
		TZ:        "UTC",
	})
	if err != nil {
		t.Fatalf("create check: %v", err)
	}
	return c
}

func ping(t *testing.T, db *DB, checkID int64, action engine.Action) (*engine.Check, *engine.Flip) {
	t.Helper()
	c, flip, _, _, err := db.RecordPing(context.Background(), checkID, action, PingInput{
		Scheme: "http", RemoteAddr: "127.0.0.1", Method: "GET",
	})
	if err != nil {
		t.Fatalf("record ping: %v", err)
	}
	return c, flip
}

func TestOpenAppliesPragmas(t *testing.T) {
	db := testDB(t)
	var mode string
	if err := db.Read.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := db.Read.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatal(err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "again.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path); err != nil {
		t.Fatalf("second open: %v", err)
	}
}

func TestSeedKeepsStableKeys(t *testing.T) {
	db := testDB(t)
	first := testProject(t, db)
	again, err := db.Seed(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if again.BadgeKey != first.BadgeKey || again.PingKey != first.PingKey {
		t.Error("re-seeding must not rotate badge_key or ping_key")
	}
	if secret, _ := db.Setting(context.Background(), "secret_key"); secret == "" {
		t.Error("secret_key was not persisted")
	}
}

func TestRecordPingUpdatesCheckAndQueuesFlip(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, 10*time.Minute)

	if c.Status != engine.StatusNew || c.NPings != 0 {
		t.Fatalf("fresh check = %+v", c)
	}
	if c.AlertAfter != nil {
		t.Errorf("a new check must not have a deadline, got %v", *c.AlertAfter)
	}

	c, flip := ping(t, db, c.ID, engine.ActionSuccess)
	if flip == nil || flip.NewStatus != engine.StatusUp {
		t.Fatalf("flip = %+v, want new->up", flip)
	}
	if c.NPings != 1 || c.LastPing == nil {
		t.Errorf("check = %+v", c)
	}
	if c.AlertAfter == nil {
		t.Fatal("up check needs a deadline")
	}

	// The flip is queued but must not alert: new->up is filtered by the
	// notifier, and processed stays NULL for it to decide.
	pending, claimed, err := db.ClaimNextFlip(ctx, time.Now().UTC())
	if err != nil || !claimed {
		t.Fatalf("claim flip: %v %v", claimed, err)
	}
	if pending.Flip.NewStatus != engine.StatusUp || pending.Flip.OldStatus != engine.StatusNew {
		t.Errorf("queued flip = %+v", pending.Flip)
	}
	if _, ok, _ := db.ClaimNextFlip(ctx, time.Now().UTC()); ok {
		t.Error("second claim must find an empty queue")
	}

	// A test notification is recorded and carries the check status.
	ch, err := db.CreateChannel(ctx, &NewChannel{
		ProjectID: project.ID, Name: "本地", URLs: []string{"json://localhost:9910/hook"},
		Conf: ChannelConf{Up: true, Down: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.StartNotification(ctx, &c.ID, ch.ID, engine.StatusUp); err != nil {
		t.Fatal(err)
	}
}

func TestGoingDownScannerFlipsAtDeadline(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Minute, time.Minute)
	ping(t, db, c.ID, engine.ActionSuccess)

	// The deadline is two minutes out; claiming with a future clock must flip it.
	future := time.Now().UTC().Add(3 * time.Minute)
	flip, err := db.ClaimGoingDown(ctx, future)
	if err != nil {
		t.Fatal(err)
	}
	if flip == nil {
		t.Fatal("expected a going-down flip")
	}
	if flip.Reason != engine.ReasonTimeout || flip.NewStatus != engine.StatusDown {
		t.Errorf("flip = %+v", flip)
	}
	if flip.Created.After(future) {
		t.Errorf("flip created = %v, want the deadline not the scan time", flip.Created)
	}

	after, err := db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != engine.StatusDown || after.AlertAfter != nil {
		t.Errorf("check = status %q alert_after %v", after.Status, after.AlertAfter)
	}

	// The timeout flip must reach the notification queue, or alerts are lost.
	if _, ok, err := db.ClaimNextFlip(ctx, future); err != nil || !ok {
		t.Fatalf("timeout flip was not queued (ok=%v err=%v)", ok, err)
	}

	// A second sweep finds nothing: the deadline is cleared and status is down.
	again, err := db.ClaimGoingDown(ctx, future)
	if err != nil {
		t.Fatal(err)
	}
	if again != nil {
		t.Errorf("expected no further flip, got %+v", again)
	}
}

func TestGoingDownRefreshesWhenStillUp(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, time.Hour)
	ping(t, db, c.ID, engine.ActionSuccess)

	before, err := db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	flip, err := db.ClaimGoingDown(ctx, before.Created.Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if flip != nil {
		t.Errorf("no flip expected while the check is up, got %+v", flip)
	}
	after, err := db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != engine.StatusUp {
		t.Errorf("status = %q, want up", after.Status)
	}
	if after.AlertAfter == nil || !after.AlertAfter.Equal(*before.AlertAfter) {
		t.Errorf("alert_after = %v, want it preserved as %v", after.AlertAfter, *before.AlertAfter)
	}
}

func TestConcurrentPingsKeepSequenceGapless(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, time.Hour)

	const n = 500
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionSuccess, PingInput{Method: "GET"})
			if err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent ping: %v", err)
	}

	after, err := db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.NPings != n {
		t.Fatalf("n_pings = %d, want %d", after.NPings, n)
	}

	// Every sequence number from 1..n appears exactly once.
	seen := map[int64]int{}
	rows, err := db.Read.QueryContext(ctx, `SELECT n FROM pings WHERE owner_id = ?`, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		seen[v]++
	}
	if len(seen) != n {
		t.Fatalf("distinct n values = %d, want %d", len(seen), n)
	}
	for i := 1; i <= n; i++ {
		if seen[int64(i)] != 1 {
			t.Fatalf("sequence %d appears %d times", i, seen[int64(i)])
		}
	}
}

func TestPruneKeepsLastNPings(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, time.Hour)

	for i := 0; i < 5; i++ {
		ping(t, db, c.ID, engine.ActionSuccess)
	}
	if err := db.PruneCheck(ctx, c.ID, 2); err != nil {
		t.Fatal(err)
	}

	var count int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM pings WHERE owner_id = ?`, c.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Errorf("retained pings = %d, want 2", count)
	}
}

func TestPauseResumeThroughStore(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, time.Hour)
	ping(t, db, c.ID, engine.ActionSuccess)
	ping(t, db, c.ID, engine.ActionStart)

	paused, changed, err := db.Pause(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || paused.Status != engine.StatusPaused {
		t.Fatalf("pause = %+v changed=%v", paused, changed)
	}
	if paused.LastPing == nil {
		t.Error("pause must keep last_ping")
	}
	if paused.LastStart != nil {
		t.Error("pause must clear last_start")
	}

	// Drain the new->up flip from the setup pings before looking at pause.
	if _, ok, err := db.ClaimNextFlip(ctx, time.Now().UTC()); err != nil || !ok {
		t.Fatalf("setup flip should be queued (ok=%v err=%v)", ok, err)
	}

	// pause is idempotent
	if _, changed, err := db.Pause(ctx, c.ID); err != nil || changed {
		t.Errorf("second pause changed=%v err=%v, want no change", changed, err)
	}

	resumed, changed, err := db.Resume(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !changed || resumed.Status != engine.StatusNew || resumed.LastPing != nil {
		t.Fatalf("resume = %+v changed=%v", resumed, changed)
	}
	if _, changed, err := db.Resume(ctx, c.ID); err != nil || changed {
		t.Errorf("resuming a new check must report no change (409 at the API)")
	}

	// None of the pause/resume flips may alert, so the queue must be empty.
	if _, ok, err := db.ClaimNextFlip(ctx, time.Now().UTC()); err != nil || ok {
		t.Errorf("pause/resume must not queue notifications (ok=%v err=%v)", ok, err)
	}
}

func TestManualResumeRecordsIgnoredPing(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, time.Hour)
	c.ManualResume = true
	c.Methods = ""
	if err := db.UpdateCheck(ctx, c); err != nil {
		t.Fatal(err)
	}
	ping(t, db, c.ID, engine.ActionSuccess)
	if _, _, err := db.Pause(ctx, c.ID); err != nil {
		t.Fatal(err)
	}

	after, flip, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionSuccess, PingInput{Method: "GET"})
	if err != nil {
		t.Fatal(err)
	}
	if flip != nil {
		t.Errorf("flip = %+v, want none", flip)
	}
	if after.Status != engine.StatusPaused {
		t.Errorf("status = %q, want paused", after.Status)
	}

	pings, err := db.ListPings(ctx, c.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pings) != 2 || pings[0].Kind != "ign" {
		t.Errorf("pings = %+v, want the newest recorded as ign", pings)
	}
}

func TestRenameAndDelete(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, time.Hour)
	ping(t, db, c.ID, engine.ActionSuccess)

	deleted, err := db.RenameAndDelete(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CheckByID(ctx, c.ID); err != ErrNotFound {
		t.Errorf("check still present: %v", err)
	}
	var pings int
	db.Read.QueryRowContext(ctx, `SELECT count(*) FROM pings`).Scan(&pings)
	if pings != 0 {
		t.Errorf("pings left behind: %d", pings)
	}
	// The old ping URL must now 404 rather than hit a live check.
	if _, err := db.CheckByCode(ctx, deleted.Code); err != ErrNotFound {
		t.Errorf("lookup by the original code: %v", err)
	}
}
