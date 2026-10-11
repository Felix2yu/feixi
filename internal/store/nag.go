package store

import (
	"context"
	"time"

	"github.com/yufei/feixi/internal/engine"
)

// NagCandidate pairs one down check with a channel that should receive a
// repeated reminder now.
type NagCandidate struct {
	Check   *engine.Check
	Channel *Channel
	// DownSince is when the current outage started; zero when unknown.
	DownSince time.Time
	Reason    engine.Reason
}

// NagDue lists every (check, channel) pair whose last successfully delivered
// down notification is at least `period` old. Only checks still down and
// channels explicitly opted into reminders are considered. The notifications
// table is the dedupe record, so a restart cannot send twice in one period.
func (db *DB) NagDue(ctx context.Context, period time.Duration, now time.Time) ([]NagCandidate, error) {
	if period <= 0 {
		return nil, nil
	}

	rows, err := db.Read.QueryContext(ctx,
		`SELECT `+checkColumns+` FROM checks WHERE status = 'down'`)
	if err != nil {
		return nil, err
	}
	var checks []*engine.Check
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		checks = append(checks, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var out []NagCandidate
	for _, c := range checks {
		channels, err := db.ChannelsForCheck(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		downSince, reason, _, _ := db.LastDownFlip(ctx, c.ID)
		for _, ch := range channels {
			if !ch.Conf.Nag {
				continue
			}
			last, err := db.LastDeliveredNotice(ctx, c.ID, ch.ID, engine.StatusDown)
			if err != nil {
				return nil, err
			}
			// Never nag unless the original alert was delivered successfully.
			if last == nil || now.Sub(*last) < period {
				continue
			}
			out = append(out, NagCandidate{Check: c, Channel: ch, DownSince: downSince, Reason: reason})
		}
	}
	return out, nil
}
