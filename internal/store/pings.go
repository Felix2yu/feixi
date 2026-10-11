package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/yufei/feixi/internal/engine"
)

type Ping struct {
	ID         int64
	N          int64
	CheckID    int64
	Created    time.Time
	Kind       string // "" is a success ping
	Scheme     string
	RemoteAddr string
	Method     string
	UA         string
	Body       []byte
	BodySize   int
	ExitStatus *int64
	Rid        *string
	Duration   *time.Duration
}

type PingInput struct {
	Scheme     string
	RemoteAddr string
	Method     string
	UA         string
	Body       []byte
	ExitStatus *int64
	Rid        *string
}

// RecordPing applies one incoming ping atomically: the check row and the ping
// row land together, so the daemon never sees a moved check without its ping.
// Network calls stay outside this transaction.
func (db *DB) RecordPing(ctx context.Context, checkID int64, action engine.Action, in PingInput) (*engine.Check, *engine.Flip, int64, bool, error) {
	now := time.Now().UTC()
	var (
		updated *engine.Check
		flip    *engine.Flip
		n       int64
		prune   bool
	)

	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		// The atomic counter doubles as our row lock: the write pool has one
		// connection, and RETURNING hands back this ping's sequence number.
		if err := tx.QueryRowContext(ctx,
			`UPDATE checks SET n_pings = n_pings + 1 WHERE id = ? RETURNING n_pings`,
			checkID).Scan(&n); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}

		c, err := scanCheck(tx.QueryRowContext(ctx,
			`SELECT `+checkColumns+` FROM checks WHERE id = ?`, checkID))
		if err != nil {
			return err
		}
		c.NPings = n
		action = engine.EffectiveAction(c, action)

		f, err := engine.ApplyPing(c, action, now, in.Rid, db.Sched)
		if err != nil {
			return err
		}
		if f != nil {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO flips(owner_id, created, old_status, new_status, reason)
				 VALUES(?,?,?,?,?)`,
				checkID, toDB(f.Created), string(f.OldStatus), string(f.NewStatus), string(f.Reason)); err != nil {
				return err
			}
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE checks SET n_pings=?, last_ping=?, last_start=?, last_start_rid=?,
				last_duration_us=?, alert_after=?, status=?, fail_count=? WHERE id=?`,
			n, nullTime(c.LastPing), nullTime(c.LastStart), nullStr(c.LastStartRid),
			nullDur(c.LastDuration), nullTime(c.AlertAfter), string(c.Status), c.FailCount,
			checkID); err != nil {
			return err
		}

		kind := ""
		switch action {
		case engine.ActionStart, engine.ActionFail, engine.ActionLog, engine.ActionIgn:
			kind = string(action)
		}
		var body []byte
		if len(in.Body) > 0 {
			body = in.Body
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO pings(n, owner_id, created, kind, scheme, remote_addr, method, ua,
				body, body_size, exitstatus, rid)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
			n, checkID, toDB(now), nullableString(kind), in.Scheme, nullableString(in.RemoteAddr),
			in.Method, in.UA, body, len(in.Body), in.ExitStatus, nullStr(in.Rid))
		if err != nil {
			return err
		}

		updated, flip, prune = c, f, n%engine.PruneEveryNPing == 0
		return nil
	})
	if err != nil {
		return nil, nil, 0, false, err
	}
	return updated, flip, n, prune, nil
}

func nullableString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func (db *DB) ListPings(ctx context.Context, checkID int64, limit int) ([]*Ping, error) {
	rows, err := db.Read.QueryContext(ctx,
		`SELECT id, n, owner_id, created, kind, scheme, remote_addr, method, ua,
			body, body_size, exitstatus, rid
		   FROM pings WHERE owner_id = ? ORDER BY id DESC LIMIT ?`, checkID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Ping
	for rows.Next() {
		p, err := scanPing(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	db.prepareDurations(ctx, out)
	return out, nil
}

type pingScanner interface {
	Scan(dest ...any) error
}

func scanPing(s pingScanner) (*Ping, error) {
	p := &Ping{}
	var created int64
	var kind, remoteAddr, rid sql.NullString
	var exitstatus sql.NullInt64
	var body []byte
	err := s.Scan(&p.ID, &p.N, &p.CheckID, &created, &kind, &p.Scheme, &remoteAddr,
		&p.Method, &p.UA, &body, &p.BodySize, &exitstatus, &rid)
	if err != nil {
		return nil, err
	}
	p.Created = fromDB(created)
	p.Kind = kind.String
	p.RemoteAddr = remoteAddr.String
	p.Body = body
	if exitstatus.Valid {
		v := exitstatus.Int64
		p.ExitStatus = &v
	}
	p.Rid = strPtr(rid)
	return p, nil
}

// prepareDurations pairs each success/fail ping with the start ping of the same
// rid in one pass over the already-fetched page, instead of querying per row.
func (db *DB) prepareDurations(ctx context.Context, pings []*Ping) {
	if len(pings) == 0 {
		return
	}

	// pings arrive newest first; walk them oldest first.
	starts := map[string]*time.Time{}
	misses := 0
	earliest := pings[len(pings)-1]
	for i := len(pings) - 1; i >= 0; i-- {
		p := pings[i]
		rid := ""
		if p.Rid != nil {
			rid = *p.Rid
		}
		switch p.Kind {
		case "start":
			t := p.Created
			starts[rid] = &t
		case "", "fail":
			if start, seen := starts[rid]; !seen {
				if p.Created.Sub(earliest.Created) < engine.MaxDuration {
					// A start may exist just outside the page; look back a bit.
					misses++
					p.Duration = db.durationFallback(ctx, p)
				}
			} else if start != nil && p.Created.Sub(*start) < engine.MaxDuration {
				d := p.Created.Sub(*start)
				p.Duration = &d
			}
			// The rid is consumed either way: the next success/fail with the same
			// rid reports no duration, exactly like the original.
			starts[rid] = nil
		}
	}

	if misses > 10 {
		for _, p := range pings {
			p.Duration = nil
		}
	}
}

// durationFallback looks backwards for the start event when the page did not
// contain it, bounded by MAX_DURATION like the original Ping.duration.
func (db *DB) durationFallback(ctx context.Context, p *Ping) *time.Duration {
	rows, err := db.Read.QueryContext(ctx,
		`SELECT kind, rid, created FROM pings
		   WHERE owner_id = ? AND id < ? AND created >= ?
		   ORDER BY id DESC LIMIT 200`,
		p.CheckID, p.ID, toDB(p.Created.Add(-engine.MaxDuration)))
	if err != nil {
		return nil
	}
	defer rows.Close()

	rid := ""
	if p.Rid != nil {
		rid = *p.Rid
	}
	for rows.Next() {
		var kind sql.NullString
		var rowRid sql.NullString
		var created int64
		if err := rows.Scan(&kind, &rowRid, &created); err != nil {
			return nil
		}
		if rowRid.String != rid {
			continue
		}
		switch kind.String {
		case "start":
			d := p.Created.Sub(fromDB(created))
			return &d
		case "", "fail":
			return nil
		}
	}
	return nil
}

func (db *DB) PingBody(ctx context.Context, checkID int64, n int64) ([]byte, error) {
	var body []byte
	err := db.Read.QueryRowContext(ctx,
		`SELECT body FROM pings WHERE owner_id = ? AND n = ?`, checkID, n).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return body, err
}

// LastPingBody returns the body of the newest ping before `before` for a check,
// used when rendering notification text.
func (db *DB) LastPingBody(ctx context.Context, checkID int64, before time.Time) ([]byte, *int64, error) {
	var (
		body       []byte
		exitstatus sql.NullInt64
	)
	err := db.Read.QueryRowContext(ctx,
		`SELECT body, exitstatus FROM pings
		   WHERE owner_id = ? AND created <= ? AND (kind IS NULL OR kind = 'fail')
		   ORDER BY id DESC LIMIT 1`, checkID, toDB(before)).Scan(&body, &exitstatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	var exit *int64
	if exitstatus.Valid {
		v := exitstatus.Int64
		exit = &v
	}
	return body, exit, nil
}
