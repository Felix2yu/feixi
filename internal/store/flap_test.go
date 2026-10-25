package store

import (
	"context"
	"testing"
	"time"

	"github.com/Felix2yu/feixi/internal/engine"
)

// alert returns ResolveFlipAlert's decision for one just-recorded flip.
func alert(t *testing.T, db *DB, checkID int64, window time.Duration, threshold int) FlapDecision {
	t.Helper()
	d, err := db.ResolveFlipAlert(context.Background(), checkID, time.Now().UTC(), window, threshold)
	if err != nil {
		t.Fatalf("resolve flap alert: %v", err)
	}
	return d
}

func TestResolveFlipAlert(t *testing.T) {
	db := testDB(t)
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, time.Minute)
	window := time.Hour
	threshold := 3

	// new -> up is silent and must not count.
	ping(t, db, c.ID, engine.ActionSuccess)
	if d := alert(t, db, c.ID, window, threshold); d != FlapNormal {
		t.Fatalf("silent new->up decision = %d, want normal", d)
	}

	ping(t, db, c.ID, engine.ActionFail) // alertable flip 1
	if d := alert(t, db, c.ID, window, threshold); d != FlapNormal {
		t.Fatalf("flip 1 decision = %d, want normal", d)
	}
	ping(t, db, c.ID, engine.ActionSuccess) // alertable flip 2
	if d := alert(t, db, c.ID, window, threshold); d != FlapNormal {
		t.Fatalf("flip 2 decision = %d, want normal", d)
	}
	ping(t, db, c.ID, engine.ActionFail) // alertable flip 3
	if d := alert(t, db, c.ID, window, threshold); d != FlapEnter {
		t.Fatalf("flip 3 decision = %d, want enter", d)
	}
	reloaded, err := db.CheckByID(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.Flapping || reloaded.FlapSince == nil {
		t.Fatalf("flapping = %v flap_since = %v", reloaded.Flapping, reloaded.FlapSince)
	}

	ping(t, db, c.ID, engine.ActionSuccess) // flip 4, still inside window
	if d := alert(t, db, c.ID, window, threshold); d != FlapSuppress {
		t.Fatalf("flip 4 decision = %d, want suppress", d)
	}

	// Once the burst has aged out, the next decision clears the flap state.
	if d := alert(t, db, c.ID, time.Nanosecond, threshold); d != FlapNormal {
		t.Fatalf("aged-out decision = %d, want normal", d)
	}
	reloaded, _ = db.CheckByID(context.Background(), c.ID)
	if reloaded.Flapping {
		t.Fatal("flapping flag should be cleared after quiet window")
	}

	// Threshold 0 disables the feature entirely.
	if d := alert(t, db, c.ID, window, 0); d != FlapNormal {
		t.Fatalf("disabled decision = %d, want normal", d)
	}
}

func TestFlappingDue(t *testing.T) {
	db := testDB(t)
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, time.Minute)

	ping(t, db, c.ID, engine.ActionSuccess)
	ping(t, db, c.ID, engine.ActionFail)
	ping(t, db, c.ID, engine.ActionSuccess)
	ping(t, db, c.ID, engine.ActionFail) // enters flap, check currently down
	if d := alert(t, db, c.ID, time.Hour, 3); d != FlapEnter {
		t.Fatalf("want enter, got %d", d)
	}

	// Nothing due while the last flip is fresh.
	due, err := db.FlappingDue(context.Background(), time.Now().UTC(), time.Hour)
	if err != nil || len(due) != 0 {
		t.Fatalf("fresh flap due = %d, err = %v", len(due), err)
	}

	// Recover while flapping: the flip is suppressed but status moves to up.
	ping(t, db, c.ID, engine.ActionSuccess)
	if d := alert(t, db, c.ID, time.Hour, 3); d != FlapSuppress {
		t.Fatalf("recovery inside flap should suppress")
	}

	// Age every transition beyond the window.
	if _, err := db.Write.Exec(
		`UPDATE flips SET created = created - ? WHERE owner_id = ?`,
		(2 * time.Hour).Microseconds(), c.ID); err != nil {
		t.Fatal(err)
	}

	due, err = db.FlappingDue(context.Background(), time.Now().UTC(), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 {
		t.Fatalf("due = %d, want 1", len(due))
	}
	if due[0].Check.Status != engine.StatusUp {
		t.Errorf("settled status = %q, want up", due[0].Check.Status)
	}
	if due[0].FlapSince.IsZero() {
		t.Error("flap_since should be populated")
	}

	// Claiming is one-shot: the flag is cleared.
	due, _ = db.FlappingDue(context.Background(), time.Now().UTC(), time.Hour)
	if len(due) != 0 {
		t.Fatalf("second sweep due = %d, want 0", len(due))
	}
}

func TestFailTolerancePersisted(t *testing.T) {
	db := testDB(t)
	project := testProject(t, db)
	c, err := db.CreateCheck(context.Background(), &NewCheck{
		ProjectID: project.ID, Name: "容忍", Kind: engine.KindSimple,
		Timeout: time.Hour, Grace: time.Minute, TZ: "UTC", FailTolerance: 2,
	})
	if err != nil {
		t.Fatal(err)
	}

	// The first ever signal is a failure: absorbed, the check leaves "new"
	// for "up" silently instead of alerting on a brand-new check.
	updated, flip := ping(t, db, c.ID, engine.ActionFail)
	if updated.Status != engine.StatusUp || updated.FailCount != 1 {
		t.Fatalf("first fail: status=%q count=%d", updated.Status, updated.FailCount)
	}
	if flip != nil && engine.ShouldNotify(flip) {
		t.Fatalf("tolerated new->up flip must be silent, got %+v", flip)
	}

	ping(t, db, c.ID, engine.ActionFail)
	updated, flip = ping(t, db, c.ID, engine.ActionFail)
	if updated.Status != engine.StatusDown || updated.FailCount != 3 {
		t.Fatalf("third fail: status=%q count=%d", updated.Status, updated.FailCount)
	}
	if flip == nil || flip.Reason != engine.ReasonFail {
		t.Fatalf("flip = %+v, want fail flip", flip)
	}

	// Success resets the counter even though nothing else changed.
	updated, _ = ping(t, db, c.ID, engine.ActionSuccess)
	if updated.FailCount != 0 {
		t.Fatalf("after success count = %d, want 0", updated.FailCount)
	}

	// The configured value round-trips through a reload.
	reloaded, err := db.CheckByID(context.Background(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.FailTolerance != 2 {
		t.Errorf("tolerance = %d, want 2", reloaded.FailTolerance)
	}
}
