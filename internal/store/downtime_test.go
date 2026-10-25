package store

import (
	"context"
	"testing"
	"time"

	"github.com/Felix2yu/feixi/internal/engine"
)

func TestDownDurationOnRecovery(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, time.Hour)

	// up → down (fail) → up (success): the recovery flip must carry the outage length.
	if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionSuccess, PingInput{}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionFail, PingInput{}); err != nil {
		t.Fatal(err)
	}

	// Rewind the whole history an hour so the recovery has a measurable outage
	// behind it. Only the immediately preceding flip counts, like the original.
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE flips SET created = created - ?`, int64(time.Hour/time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := db.RecordPing(ctx, c.ID, engine.ActionSuccess, PingInput{}); err != nil {
		t.Fatal(err)
	}

	// Drain the silent new->up flip first.
	first, ok, err := db.ClaimNextFlip(ctx, time.Now().UTC())
	if err != nil || !ok {
		t.Fatalf("claim: ok=%v err=%v", ok, err)
	}
	if engine.ShouldNotify(&first.Flip) {
		t.Fatal("new->up must not be delivered")
	}

	down, ok, err := db.ClaimNextFlip(ctx, time.Now().UTC())
	if err != nil || !ok {
		t.Fatal("expected the down flip")
	}
	if down.DownDur != nil {
		t.Errorf("down flip should have no outage length, got %v", *down.DownDur)
	}

	up, ok, err := db.ClaimNextFlip(ctx, time.Now().UTC())
	if err != nil || !ok {
		t.Fatal("expected the recovery flip")
	}
	if up.DownDur == nil {
		t.Fatal("recovery flip is missing the outage length")
	}
	if *up.DownDur < 59*time.Minute || *up.DownDur > 61*time.Minute {
		t.Errorf("outage length = %v, want about an hour", *up.DownDur)
	}
}

func TestDowntimesThroughStore(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, time.Hour)

	_, _, _, _, _ = db.RecordPing(ctx, c.ID, engine.ActionSuccess, PingInput{})
	_, _, _, _, _ = db.RecordPing(ctx, c.ID, engine.ActionFail, PingInput{})
	_, _, _, _, _ = db.RecordPing(ctx, c.ID, engine.ActionSuccess, PingInput{})

	after, err := db.CheckByID(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	records, err := db.Downtimes(ctx, after, 1, "UTC", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 {
		t.Fatalf("records = %d", len(records))
	}
	if records[0].Count != 1 {
		t.Errorf("downtime count = %d, want 1", records[0].Count)
	}
	// Two sub-second pings in a row: the outage is measured in milliseconds,
	// and the record must not claim a negative or absurd length.
	if records[0].Duration < 0 || records[0].Duration > time.Minute {
		t.Errorf("duration = %v", records[0].Duration)
	}
	if records[0].Uptime(time.UTC) <= 0.99 {
		t.Errorf("uptime = %v, want ~1", records[0].Uptime(time.UTC))
	}
}
