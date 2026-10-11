package store

import (
	"context"
	"testing"
	"time"

	"github.com/yufei/feixi/internal/engine"
)

func mkNagChannel(t *testing.T, db *DB, projectID, checkID int64, nag bool) *Channel {
	t.Helper()
	ch, err := db.CreateChannel(context.Background(), &NewChannel{
		ProjectID: projectID,
		Name:      "催促渠道",
		URLs:      []string{"json://example.test/hook"},
		Conf:      ChannelConf{Up: true, Down: true, Nag: nag},
	})
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := db.Write.Exec(
		`INSERT INTO channel_checks(channel_id, check_id) VALUES(?, ?)`, ch.ID, checkID); err != nil {
		t.Fatalf("link channel: %v", err)
	}
	return ch
}

func TestNagDue(t *testing.T) {
	db := testDB(t)
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, time.Minute)

	// up -> down via an explicit fail signal.
	ping(t, db, c.ID, engine.ActionSuccess)
	updated, _ := ping(t, db, c.ID, engine.ActionFail)
	if updated.Status != engine.StatusDown {
		t.Fatalf("status = %s, want down", updated.Status)
	}

	// No channel yet: nothing due.
	got, err := db.NagDue(context.Background(), time.Nanosecond, time.Now().UTC())
	if err != nil || len(got) != 0 {
		t.Fatalf("nag without channel = %v, %v", got, err)
	}

	quiet := mkNagChannel(t, db, project.ID, c.ID, false)
	loud := mkNagChannel(t, db, project.ID, c.ID, true)

	// Nothing delivered yet: the original alert must land before reminders.
	got, err = db.NagDue(context.Background(), time.Nanosecond, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("nag before first delivery = %d candidates", len(got))
	}

	// A failed first delivery must not start the reminder clock.
	n, err := db.StartNotification(context.Background(), &c.ID, loud.ID, engine.StatusDown)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishNotification(context.Background(), n.ID, "connection refused"); err != nil {
		t.Fatal(err)
	}
	got, _ = db.NagDue(context.Background(), time.Nanosecond, time.Now().UTC())
	if len(got) != 0 {
		t.Fatalf("failed delivery must not count: %d candidates", len(got))
	}

	// Successful delivery for the opted-in channel: due immediately with a
	// sub-microsecond period, and only for that channel.
	n2, err := db.StartNotification(context.Background(), &c.ID, loud.ID, engine.StatusDown)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.FinishNotification(context.Background(), n2.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, err = db.NagDue(context.Background(), time.Nanosecond, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Channel.ID != loud.ID {
		t.Fatalf("nag candidates = %v, want only opted-in channel %d", got, loud.ID)
	}
	if got[0].Reason != engine.ReasonFail {
		t.Errorf("reason = %q, want fail", got[0].Reason)
	}
	if got[0].DownSince.IsZero() {
		t.Error("down-since should be populated")
	}

	// A long period suppresses the reminder; the quiet channel never qualifies.
	got, _ = db.NagDue(context.Background(), 24*time.Hour, time.Now().UTC())
	if len(got) != 0 {
		t.Fatalf("nag within period = %d candidates", len(got))
	}
	_ = quiet
}

func TestLastDownFlip(t *testing.T) {
	db := testDB(t)
	project := testProject(t, db)
	c := mkCheck(t, db, project.ID, time.Hour, time.Minute)

	ping(t, db, c.ID, engine.ActionSuccess)
	ping(t, db, c.ID, engine.ActionFail)

	at, reason, ok, err := db.LastDownFlip(context.Background(), c.ID)
	if err != nil || !ok {
		t.Fatalf("last down flip = ok %v, err %v", ok, err)
	}
	if reason != engine.ReasonFail {
		t.Errorf("reason = %q, want fail", reason)
	}
	if time.Since(at) > time.Minute {
		t.Errorf("flip time %v looks wrong", at)
	}
}
