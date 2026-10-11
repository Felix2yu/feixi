package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/tzutil"
)

// ClaimGoingDown takes exactly one check whose deadline has passed and decides,
// inside a single transaction, whether it really went down. It mirrors
// sendalerts.handle_going_down: refresh the deadline while the check is still
// up, otherwise flip it and enqueue the notification.
func (db *DB) ClaimGoingDown(ctx context.Context, now time.Time) (*engine.Flip, error) {
	var flip *engine.Flip

	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx,
			`SELECT `+checkColumns+` FROM checks
			   WHERE alert_after IS NOT NULL AND alert_after < ? AND status != 'down'
			   ORDER BY alert_after LIMIT 1`, toDB(now))
		c, err := scanCheck(row)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		deadline := *c.AlertAfter

		status, err := engine.StatusOf(c, now, db.Sched, false)
		if err == nil && status != engine.DisplayDown {
			at, ok, aerr := engine.GoingDownAfter(c, db.Sched)
			if aerr != nil {
				err = aerr
			} else if ok {
				return setAlertAfter(ctx, tx, c.ID, &at)
			} else {
				return setAlertAfter(ctx, tx, c.ID, nil)
			}
		}
		if err != nil {
			// A broken schedule must not spin: push the deadline an hour out and
			// let the caller log it.
			if uerr := pushAlertAfter(ctx, tx, c.ID, now.Add(time.Hour)); uerr != nil {
				return uerr
			}
			return err
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE checks SET status = 'down', alert_after = NULL WHERE id = ? AND status = ?`,
			c.ID, string(c.Status))
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			// Another worker already flipped it.
			return nil
		}

		f := engine.MarkDown(c, deadline)
		if f == nil {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO flips(owner_id, created, old_status, new_status, reason)
			 VALUES(?,?,?,?,?)`,
			c.ID, toDB(f.Created), string(f.OldStatus), string(f.NewStatus), string(f.Reason)); err != nil {
			return err
		}
		flip = f
		return nil
	})
	if err != nil {
		return nil, err
	}
	return flip, nil
}

func pushAlertAfter(ctx context.Context, tx *sql.Tx, checkID int64, at time.Time) error {
	return setAlertAfter(ctx, tx, checkID, &at)
}

func setAlertAfter(ctx context.Context, tx *sql.Tx, checkID int64, at *time.Time) error {
	_, err := tx.ExecContext(ctx, `UPDATE checks SET alert_after = ? WHERE id = ?`,
		nullTime(at), checkID)
	return err
}

type PendingFlip struct {
	ID      int64
	Check   *engine.Check
	Flip    engine.Flip
	DownDur *time.Duration
}

// ClaimNextFlip pops the oldest unprocessed flip together with its check. The
// conditional update is the queue's only lock: whoever moves processed from NULL
// to a timestamp owns the row.
func (db *DB) ClaimNextFlip(ctx context.Context, now time.Time) (*PendingFlip, bool, error) {
	var out *PendingFlip
	claimed := false

	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		var (
			id, ownerID, created int64
			oldStatus, newStatus string
			reason               string
		)
		err := tx.QueryRowContext(ctx,
			`SELECT id, owner_id, created, old_status, new_status, reason FROM flips
			   WHERE processed IS NULL ORDER BY id LIMIT 1`).
			Scan(&id, &ownerID, &created, &oldStatus, &newStatus, &reason)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		res, err := tx.ExecContext(ctx,
			`UPDATE flips SET processed = ? WHERE id = ? AND processed IS NULL`,
			toDB(now), id)
		if err != nil {
			return err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if affected == 0 {
			return nil
		}

		c, err := scanCheck(tx.QueryRowContext(ctx,
			`SELECT `+checkColumns+` FROM checks WHERE id = ?`, ownerID))
		if err != nil {
			return err
		}

		f := engine.Flip{
			CheckID:   ownerID,
			Created:   fromDB(created),
			Processed: &now,
			OldStatus: engine.Status(oldStatus),
			NewStatus: engine.Status(newStatus),
			Reason:    engine.Reason(reason),
		}
		out = &PendingFlip{ID: id, Check: c, Flip: f, DownDur: db.downDuration(ctx, tx, ownerID, f)}
		claimed = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return out, claimed, nil
}

// downDuration is how long the check sat in down before going back up.
func (db *DB) downDuration(ctx context.Context, tx *sql.Tx, checkID int64, f engine.Flip) *time.Duration {
	if f.NewStatus != engine.StatusUp {
		return nil
	}
	var created int64
	var newStatus string
	err := tx.QueryRowContext(ctx,
		`SELECT created, new_status FROM flips
		   WHERE owner_id = ? AND created < ? ORDER BY created DESC LIMIT 1`,
		checkID, toDB(f.Created)).Scan(&created, &newStatus)
	if errors.Is(err, sql.ErrNoRows) || newStatus != string(engine.StatusDown) {
		return nil
	}
	if err != nil {
		return nil
	}
	d := f.Created.Sub(fromDB(created))
	return &d
}

// Pause flips the check to paused inside one transaction and returns the stored
// row so the API can echo it back.
func (db *DB) Pause(ctx context.Context, checkID int64) (*engine.Check, bool, error) {
	var (
		out     *engine.Check
		changed bool
	)
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		c, err := scanCheck(tx.QueryRowContext(ctx,
			`SELECT `+checkColumns+` FROM checks WHERE id = ?`, checkID))
		if err != nil {
			return err
		}
		out = c
		f := engine.Pause(c, time.Now().UTC())
		if f == nil {
			return nil
		}
		if err := insertFlip(ctx, tx, checkID, f); err != nil {
			return err
		}
		if err := updateCheckState(ctx, tx, c); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return out, changed, err
}

// Resume sends a paused check back to new. The second result is false when the
// check was not paused, which the API answers with 409.
func (db *DB) Resume(ctx context.Context, checkID int64) (*engine.Check, bool, error) {
	var (
		out     *engine.Check
		changed bool
	)
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		c, err := scanCheck(tx.QueryRowContext(ctx,
			`SELECT `+checkColumns+` FROM checks WHERE id = ?`, checkID))
		if err != nil {
			return err
		}
		out = c
		f := engine.Resume(c, time.Now().UTC())
		if f == nil {
			return nil
		}
		if err := insertFlip(ctx, tx, checkID, f); err != nil {
			return err
		}
		if err := updateCheckState(ctx, tx, c); err != nil {
			return err
		}
		changed = true
		return nil
	})
	return out, changed, err
}

func insertFlip(ctx context.Context, tx *sql.Tx, checkID int64, f *engine.Flip) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO flips(owner_id, created, processed, old_status, new_status, reason)
		 VALUES(?,?,?,?,?,?)`,
		checkID, toDB(f.Created), nullTime(f.Processed),
		string(f.OldStatus), string(f.NewStatus), string(f.Reason))
	return err
}

func updateCheckState(ctx context.Context, tx *sql.Tx, c *engine.Check) error {
	_, err := tx.ExecContext(ctx,
		`UPDATE checks SET last_ping=?, last_start=?, last_start_rid=?, last_duration_us=?,
			status=?, alert_after=?, fail_count=?, flapping=?, flap_since=? WHERE id=?`,
		nullTime(c.LastPing), nullTime(c.LastStart), nullStr(c.LastStartRid),
		nullDur(c.LastDuration), string(c.Status), nullTime(c.AlertAfter),
		c.FailCount, boolInt(c.Flapping), nullTime(c.FlapSince), c.ID)
	return err
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

type FlipEvent struct {
	Timestamp time.Time
	Up        bool
	OldStatus engine.Status
	NewStatus engine.Status
	Reason    engine.Reason
}

// Flips returns the transition history for the API and the timeline chart.
func (db *DB) Flips(ctx context.Context, checkID int64, start, end *time.Time) ([]FlipEvent, error) {
	q := newQuery(`SELECT created, old_status, new_status, reason FROM flips WHERE owner_id = ?`, checkID)
	if start != nil {
		q.raw(" AND created >= ?")
		q.args = append(q.args, toDB(*start))
	}
	if end != nil {
		q.raw(" AND created <= ?")
		q.args = append(q.args, toDB(*end))
	}
	q.raw(" ORDER BY created ASC")

	rows, err := db.Read.QueryContext(ctx, q.String(), q.Args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []FlipEvent
	for rows.Next() {
		var created int64
		var oldStatus, newStatus, reason string
		if err := rows.Scan(&created, &oldStatus, &newStatus, &reason); err != nil {
			return nil, err
		}
		out = append(out, FlipEvent{
			Timestamp: fromDB(created),
			Up:        newStatus == string(engine.StatusUp),
			OldStatus: engine.Status(oldStatus),
			NewStatus: engine.Status(newStatus),
			Reason:    engine.Reason(reason),
		})
	}
	return out, rows.Err()
}

// LastDownFlip reports when the check most recently entered the down state and
// why, so reminders can say how long the outage has lasted.
func (db *DB) LastDownFlip(ctx context.Context, checkID int64) (time.Time, engine.Reason, bool, error) {
	var created int64
	var reason string
	err := db.Read.QueryRowContext(ctx,
		`SELECT created, reason FROM flips
		  WHERE owner_id = ? AND new_status = 'down' ORDER BY id DESC LIMIT 1`,
		checkID).Scan(&created, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, engine.ReasonNone, false, nil
	}
	if err != nil {
		return time.Time{}, engine.ReasonNone, false, err
	}
	return fromDB(created), engine.Reason(reason), true, nil
}

// Downtimes computes per-month outage statistics by replaying the flip history
// backwards from the current status.
func (db *DB) Downtimes(ctx context.Context, c *engine.Check, months int, tz string, now time.Time) ([]engine.DowntimeRecord, error) {
	loc, err := tzutil.Load(tz)
	if err != nil {
		return nil, err
	}
	boundaries := engine.MonthBoundaries(now, loc, months)
	if len(boundaries) == 0 {
		return nil, nil
	}
	oldest := boundaries[len(boundaries)-1]

	rows, err := db.Read.QueryContext(ctx,
		`SELECT created, old_status FROM flips WHERE owner_id = ? AND created > ?
		  ORDER BY created DESC`, c.ID, toDB(oldest))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []engine.Event
	for rows.Next() {
		var created int64
		var old string
		if err := rows.Scan(&created, &old); err != nil {
			return nil, err
		}
		events = append(events, engine.Event{When: fromDB(created), OldStatus: engine.Status(old)})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return engine.Downtimes(boundaries, c.Created, c.Status, events, now), nil
}
