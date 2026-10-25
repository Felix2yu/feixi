package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/Felix2yu/feixi/internal/engine"
)

// FlapDecision tells the notifier what to do with one alertable flip.
type FlapDecision int

const (
	// FlapNormal: deliver the down/up alert as usual.
	FlapNormal FlapDecision = iota
	// FlapEnter: the check crossed the flapping threshold on this flip;
	// suppress the flip's own alert and send one flap notice instead.
	FlapEnter
	// FlapSuppress: the check is already inside the flap window; skip.
	FlapSuppress
)

// alertableFlip counts the flips that would actually have notified, matching
// engine.ShouldNotify: every down flip, and up flips that did not leave the
// new/paused states.
const alertableFlipExpr = `(new_status = 'down'
		OR (new_status = 'up' AND old_status NOT IN ('new','paused')))`

// ResolveFlipAlert decides whether one flip alerts or is absorbed into the
// flapping state. at is the flip's own timestamp (not the processing time),
// so delivery delays cannot slide the rolling window. The flapping flag moves
// in the same transaction as the decision, so concurrent workers cannot
// double-enter.
func (db *DB) ResolveFlipAlert(ctx context.Context, checkID int64, at time.Time, window time.Duration, threshold int) (FlapDecision, error) {
	if threshold <= 0 {
		return FlapNormal, nil
	}
	cutoff := toDB(at.Add(-window))
	decision := FlapNormal

	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		var count int64
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM flips
			  WHERE owner_id = ? AND created >= ? AND created <= ? AND `+alertableFlipExpr,
			checkID, cutoff, toDB(at)).Scan(&count); err != nil {
			return err
		}

		var flapping int64
		if err := tx.QueryRowContext(ctx,
			`SELECT flapping FROM checks WHERE id = ?`, checkID).Scan(&flapping); err != nil {
			return err
		}

		if count < int64(threshold) {
			if flapping != 0 {
				// The burst has aged out before this flip: leave the flap state
				// here; this flip is a fresh, ordinary transition.
				if _, err := tx.ExecContext(ctx,
					`UPDATE checks SET flapping = 0, flap_since = NULL WHERE id = ?`, checkID); err != nil {
					return err
				}
			}
			decision = FlapNormal
			return nil
		}

		if flapping != 0 {
			decision = FlapSuppress
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE checks SET flapping = 1, flap_since = ? WHERE id = ?`, toDB(at), checkID); err != nil {
			return err
		}
		decision = FlapEnter
		return nil
	})
	if err != nil {
		return FlapNormal, err
	}
	return decision, nil
}

// FlapSettle is a flapping check whose window has stayed quiet for a full
// window; the notifier tells the channel whether it landed up or down.
type FlapSettle struct {
	Check     *engine.Check
	FlapSince time.Time
}

// FlappingDue claims flapping checks whose last alertable transition is at
// least one window old. Claiming clears the flag inside the same transaction.
func (db *DB) FlappingDue(ctx context.Context, now time.Time, window time.Duration) ([]FlapSettle, error) {
	cutoffUS := toDB(now.Add(-window))
	var out []FlapSettle

	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			`SELECT id, flap_since,
				(SELECT COALESCE(MAX(f.created), 0) FROM flips f
				  WHERE f.owner_id = checks.id AND `+alertableFlipExpr+`)
			   FROM checks WHERE flapping = 1`)
		if err != nil {
			return err
		}
		type cand struct {
			id, flapSince, lastFlip int64
		}
		var due []cand
		for rows.Next() {
			var x cand
			if err := rows.Scan(&x.id, &x.flapSince, &x.lastFlip); err != nil {
				rows.Close()
				return err
			}
			if x.lastFlip <= cutoffUS {
				due = append(due, x)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		for _, x := range due {
			res, err := tx.ExecContext(ctx,
				`UPDATE checks SET flapping = 0, flap_since = NULL
				  WHERE id = ? AND flapping = 1`, x.id)
			if err != nil {
				return err
			}
			affected, err := res.RowsAffected()
			if err != nil {
				return err
			}
			if affected == 0 {
				continue
			}
			c, err := scanCheck(tx.QueryRowContext(ctx,
				`SELECT `+checkColumns+` FROM checks WHERE id = ?`, x.id))
			if err != nil {
				return err
			}
			out = append(out, FlapSettle{Check: c, FlapSince: fromDB(x.flapSince)})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
