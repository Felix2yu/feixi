package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yufei/feixi/internal/engine"
)

// PruneCheck keeps the last `limit` pings per check, then trims notifications
// older than the oldest retained ping and flips older than both that ping and
// 93 days — three months of flips is what the downtime statistics need.
func (db *DB) PruneCheck(ctx context.Context, checkID int64, limit int) error {
	if limit <= 0 {
		limit = engine.PingLogLimit
	}
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		var nPings int64
		err := tx.QueryRowContext(ctx, `SELECT n_pings FROM checks WHERE id = ?`, checkID).Scan(&nPings)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		threshold := nPings - int64(limit)
		if threshold > 0 {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM pings WHERE owner_id = ? AND n <= ?`, checkID, threshold); err != nil {
				return err
			}
		}

		var earliest int64
		err = tx.QueryRowContext(ctx,
			`SELECT created FROM pings WHERE owner_id = ? ORDER BY created ASC LIMIT 1`, checkID).
			Scan(&earliest)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM notifications WHERE owner_id = ? AND created < ?`,
			checkID, earliest); err != nil {
			return err
		}

		cutoff := earliest
		if now := toDB(time.Now().UTC()) - engine.FlipRetention.Microseconds(); now < cutoff {
			cutoff = now
		}
		_, err = tx.ExecContext(ctx,
			`DELETE FROM flips WHERE owner_id = ? AND created < ?`, checkID, cutoff)
		return err
	})
}

// PruneAll sweeps every check; the daemon runs it at startup and then once per
// FEIXI_PRUNE_INTERVAL, so checks that stopped reporting in still get trimmed.
func (db *DB) PruneAll(ctx context.Context, limit int) (int, error) {
	rows, err := db.Read.QueryContext(ctx, `SELECT id FROM checks ORDER BY id`)
	if err != nil {
		return 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()

	n := 0
	for _, id := range ids {
		if err := db.PruneCheck(ctx, id, limit); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}
