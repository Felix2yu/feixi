package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yufei/feixi/internal/engine"
)

// ErrNotFound is returned by lookups that found no row.
var ErrNotFound = errors.New("未找到")

// ErrNotUnique means a slug matches more than one check in the project.
var ErrNotUnique = errors.New("slug 对应多个检查项")

// ErrTooManyChecks blocks unbounded creation (slug autoprovisioning in particular).
var ErrTooManyChecks = errors.New("检查项数量已达上限")

type Scanner interface {
	Scan(dest ...any) error
}

const checkColumns = `id, code, badge_key, project_id, name, slug, tags, description, created,
	kind, timeout_us, grace_us, schedule, tz, n_pings, last_ping, last_start, last_start_rid,
	last_duration_us, alert_after, status, methods, manual_resume, filter_http_body,
	filter_default_fail, start_kw, success_kw, failure_kw,
	fail_tolerance, fail_count, flapping, flap_since`

func scanCheck(s Scanner) (*engine.Check, error) {
	var (
		created, timeoutUS, graceUS, npings             int64
		lastDurationUS                                  sql.NullInt64
		lastPing, lastStart, alertAfter, flapSince      sql.NullInt64
		lastStartRid                                    sql.NullString
		kind, status, methods                           string
		manualResume, filterHTTPBody, filterDefaultFail int64
		failTolerance, failCount, flapping              int64
	)
	c := &engine.Check{}
	err := s.Scan(&c.ID, &c.Code, &c.BadgeKey, &c.ProjectID, &c.Name, &c.Slug, &c.Tags,
		&c.Description, &created, &kind, &timeoutUS, &graceUS, &c.Schedule, &c.TZ, &npings,
		&lastPing, &lastStart, &lastStartRid, &lastDurationUS, &alertAfter, &status, &methods,
		&manualResume, &filterHTTPBody, &filterDefaultFail, &c.StartKW, &c.SuccessKW, &c.FailureKW,
		&failTolerance, &failCount, &flapping, &flapSince)
	if err != nil {
		return nil, err
	}

	c.Created = fromDB(created)
	c.Kind = engine.Kind(kind)
	c.Status = engine.Status(status)
	c.Timeout = time.Duration(timeoutUS) * time.Microsecond
	c.Grace = time.Duration(graceUS) * time.Microsecond
	c.NPings = npings
	c.Methods = methods
	c.ManualResume = manualResume != 0
	c.FilterHTTPBody = filterHTTPBody != 0
	c.FilterDefaultFail = filterDefaultFail != 0
	c.LastPing = timePtr(lastPing)
	c.LastStart = timePtr(lastStart)
	c.LastStartRid = strPtr(lastStartRid)
	c.LastDuration = durPtr(lastDurationUS)
	c.AlertAfter = timePtr(alertAfter)
	c.FailTolerance = int(failTolerance)
	c.FailCount = int(failCount)
	c.Flapping = flapping != 0
	c.FlapSince = timePtr(flapSince)
	return c, nil
}

func (db *DB) CheckByCode(ctx context.Context, code string) (*engine.Check, error) {
	return oneCheck(ctx, db.Read, `WHERE code = ?`, code)
}

func (db *DB) CheckByID(ctx context.Context, id int64) (*engine.Check, error) {
	return oneCheck(ctx, db.Read, `WHERE id = ?`, id)
}

// CheckByBadgeKey resolves the single-check badge URL, which carries a separate
// unguessable key rather than the ping code.
func (db *DB) CheckByBadgeKey(ctx context.Context, key string) (*engine.Check, error) {
	c, err := scanCheck(db.Read.QueryRowContext(ctx,
		`SELECT `+checkColumns+` FROM checks WHERE badge_key = ?`, key))
	if err != nil {
		return nil, ErrNotFound
	}
	return c, err
}

func (db *DB) CheckBySlug(ctx context.Context, projectID int64, slug string) (*engine.Check, error) {
	return oneCheck(ctx, db.Read, `WHERE project_id = ? AND slug = ?`, projectID, slug)
}

func oneCheck(ctx context.Context, q queryExec, where string, args ...any) (*engine.Check, error) {
	row := q.QueryRowContext(ctx, `SELECT `+checkColumns+` FROM checks `+where, args...)
	c, err := scanCheck(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return c, err
}

type queryExec interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// CheckBySlugStrict reports ErrNotUnique when the slug is ambiguous, which the
// ping handler turns into a 409.
func (db *DB) CheckBySlugStrict(ctx context.Context, projectID int64, slug string) (*engine.Check, error) {
	rows, err := db.Read.QueryContext(ctx,
		`SELECT `+checkColumns+` FROM checks WHERE project_id = ? AND slug = ?`, projectID, slug)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var found *engine.Check
	for rows.Next() {
		if found != nil {
			return nil, ErrNotUnique
		}
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		found = c
	}
	if found == nil {
		return nil, ErrNotFound
	}
	return found, rows.Err()
}

// OrderBys are the values ?orderby= accepts; the "-" prefix is descending.
var OrderBys = []string{"name", "-name", "last_ping", "-last_ping", "created"}

type CheckFilter struct {
	Tags    []string
	Slugs   []string
	OrderBy string
}

// ValidOrderBy reports whether f is one of OrderBys.
func ValidOrderBy(s string) bool {
	for _, v := range OrderBys {
		if s == v {
			return true
		}
	}
	return false
}

func (db *DB) ListChecks(ctx context.Context, projectID int64, f CheckFilter) ([]*engine.Check, error) {
	q := newQuery(`SELECT `+checkColumns+` FROM checks WHERE project_id = ?`, projectID)

	for _, tag := range f.Tags {
		// tags is one space-separated column, so match a whole word inside padded spaces.
		q.raw(" AND (' ' || tags || ' ') LIKE ?")
		q.args = append(q.args, " %"+tag+" %")
	}
	if len(f.Slugs) > 0 {
		q.in("slug", f.Slugs)
	}

	switch f.OrderBy {
	case "name":
		q.raw(" ORDER BY name COLLATE NOCASE ASC")
	case "-name":
		q.raw(" ORDER BY name COLLATE NOCASE DESC")
	case "created":
		q.raw(" ORDER BY created ASC")
	case "last_ping":
		q.raw(" ORDER BY last_ping ASC")
	case "-last_ping":
		q.raw(" ORDER BY last_ping DESC")
	default:
		q.raw(" ORDER BY created DESC")
	}

	rows, err := db.Read.QueryContext(ctx, q.String(), q.Args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*engine.Check
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (db *DB) CountChecks(ctx context.Context, projectID int64) (int, error) {
	var n int
	err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM checks WHERE project_id = ?`, projectID).Scan(&n)
	return n, err
}

// DistinctTags lists the tags in use, each with how many checks carry it.
func (db *DB) DistinctTags(ctx context.Context, projectID int64) ([]string, error) {
	rows, err := db.Read.QueryContext(ctx, `SELECT tags FROM checks WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	set := map[string]bool{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		for _, tag := range strings.Fields(raw) {
			set[tag] = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(set))
	for tag := range set {
		out = append(out, tag)
	}
	return out, nil
}

type NewCheck struct {
	ProjectID     int64
	Name          string
	Slug          string
	Tags          string
	Desc          string
	Kind          engine.Kind
	Timeout       time.Duration
	Grace         time.Duration
	Schedule      string
	TZ            string
	Methods       string
	ManualResum   bool
	FilterHTTP    bool
	FilterFail    bool
	StartKW       string
	SuccessKW     string
	FailureKW     string
	FailTolerance int
	ChannelIDs    []int64
	// AutoCreate marks a check provisioned by a first ping rather than by a
	// person, so the caller gets headroom past MaxChecks: a job that suddenly
	// sprouts task names should still record its pings instead of 404ing.
	AutoCreate bool
}

func (db *DB) CreateCheck(ctx context.Context, in *NewCheck) (*engine.Check, error) {
	code := uuid.NewString()
	now := time.Now().UTC()

	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		if db.MaxChecks > 0 {
			limit := db.MaxChecks
			if in.AutoCreate {
				limit *= 2
			}
			var n int
			if err := tx.QueryRowContext(ctx,
				`SELECT count(*) FROM checks WHERE project_id = ?`, in.ProjectID).Scan(&n); err != nil {
				return err
			}
			if n >= limit {
				return ErrTooManyChecks
			}
		}

		res, err := tx.ExecContext(ctx,
			`INSERT INTO checks(code, badge_key, project_id, name, slug, tags, description, created,
				kind, timeout_us, grace_us, schedule, tz, status,
				methods, manual_resume, filter_http_body, filter_default_fail,
				start_kw, success_kw, failure_kw, fail_tolerance)
			VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			code, uuid.NewString(), in.ProjectID, in.Name, in.Slug, in.Tags, in.Desc, toDB(now),
			string(in.Kind), in.Timeout.Microseconds(), in.Grace.Microseconds(),
			in.Schedule, in.TZ, string(engine.StatusNew),
			in.Methods, nullBool(in.ManualResum), nullBool(in.FilterHTTP), nullBool(in.FilterFail),
			in.StartKW, in.SuccessKW, in.FailureKW, in.FailTolerance)
		if err != nil {
			return fmt.Errorf("建检查项：%w", err)
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		return assignChannels(ctx, tx, id, in.ChannelIDs)
	})
	if err != nil {
		return nil, err
	}
	return db.CheckByCode(ctx, code)
}

func assignChannels(ctx context.Context, tx *sql.Tx, checkID int64, channelIDs []int64) error {
	for _, ch := range channelIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO channel_checks(channel_id, check_id) VALUES(?, ?)`,
			ch, checkID); err != nil {
			return fmt.Errorf("关联渠道：%w", err)
		}
	}
	return nil
}

// SetCheckChannels replaces the assignment list for one check.
func (db *DB) SetCheckChannels(ctx context.Context, checkID int64, channelIDs []int64) error {
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM channel_checks WHERE check_id = ?`, checkID); err != nil {
			return err
		}
		return assignChannels(ctx, tx, checkID, channelIDs)
	})
}

func (db *DB) CheckChannels(ctx context.Context, checkID int64) ([]int64, error) {
	rows, err := db.Read.QueryContext(ctx,
		`SELECT channel_id FROM channel_checks WHERE check_id = ? ORDER BY channel_id`, checkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// AllChannelIDs returns every channel of the project, the default assignment
// for a new check.
func (db *DB) AllChannelIDs(ctx context.Context, projectID int64) ([]int64, error) {
	rows, err := db.Read.QueryContext(ctx,
		`SELECT id FROM channels WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// UpdateCheck writes the mutable fields and refreshes the going-down deadline,
// so editing a schedule takes effect immediately.
func (db *DB) UpdateCheck(ctx context.Context, c *engine.Check) error {
	return db.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`UPDATE checks SET name=?, slug=?, tags=?, description=?, kind=?, timeout_us=?,
				grace_us=?, schedule=?, tz=?, methods=?, manual_resume=?, filter_http_body=?,
				filter_default_fail=?, start_kw=?, success_kw=?, failure_kw=?, fail_tolerance=?,
				status=?, alert_after=?, last_ping=?, last_start=?, last_start_rid=?,
				last_duration_us=? WHERE id=?`,
			c.Name, c.Slug, c.Tags, c.Description, string(c.Kind), c.Timeout.Microseconds(),
			c.Grace.Microseconds(), c.Schedule, c.TZ, c.Methods, nullBool(c.ManualResume),
			nullBool(c.FilterHTTPBody), nullBool(c.FilterDefaultFail),
			c.StartKW, c.SuccessKW, c.FailureKW, c.FailTolerance,
			string(c.Status), nullTime(c.AlertAfter), nullTime(c.LastPing), nullTime(c.LastStart),
			nullStr(c.LastStartRid), nullDur(c.LastDuration), c.ID)
		return err
	})
}

// ClearEvents wipes a check's ping history and sends it back to new, so a job
// that changed shape can be re-measured from scratch. Flip rows stay: they are
// what the downtime statistics are replayed from.
func (db *DB) ClearEvents(ctx context.Context, checkID int64) (*engine.Check, error) {
	var out *engine.Check
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		c, err := scanCheck(tx.QueryRowContext(ctx,
			`SELECT `+checkColumns+` FROM checks WHERE id = ?`, checkID))
		if err != nil {
			return err
		}
		c.Status = engine.StatusNew
		c.LastPing = nil
		c.LastStart = nil
		c.LastStartRid = nil
		c.LastDuration = nil
		c.AlertAfter = nil
		c.FailCount = 0
		c.Flapping = false
		c.FlapSince = nil

		if _, err := tx.ExecContext(ctx, `DELETE FROM pings WHERE owner_id = ?`, checkID); err != nil {
			return err
		}
		if err := updateCheckState(ctx, tx, c); err != nil {
			return err
		}
		out = c
		return nil
	})
	return out, err
}

// RenameAndDelete detaches the ping URL before deleting, so a ping arriving
// mid-delete cannot hold the foreign key open.
func (db *DB) RenameAndDelete(ctx context.Context, checkID int64) (*engine.Check, error) {
	c, err := db.CheckByID(ctx, checkID)
	if err != nil {
		return nil, err
	}

	throwaway := uuid.NewString()
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE checks SET code = ?, slug = ? WHERE id = ?`, throwaway, throwaway, checkID); err != nil {
		return nil, err
	}

	// A ping that slipped in between the rename and the delete violates the
	// foreign key; renaming again and retrying makes that vanishingly unlikely.
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := db.Write.ExecContext(ctx, `DELETE FROM checks WHERE id = ?`, checkID); err != nil {
			if attempt == 0 {
				throwaway = uuid.NewString()
				if _, uerr := db.Write.ExecContext(ctx,
					`UPDATE checks SET code = ?, slug = ? WHERE id = ?`,
					throwaway, throwaway, checkID); uerr != nil {
					return nil, uerr
				}
				continue
			}
			return nil, err
		}
		break
	}
	return c, nil
}
