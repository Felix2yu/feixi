package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yufei/feixi/internal/engine"
)

type ChannelConf struct {
	Up   bool `json:"up"`
	Down bool `json:"down"`
	// Nag opts into repeated reminders while a check stays down, at the
	// instance-wide cadence configured by FEIXI_NAG_PERIOD.
	Nag bool `json:"nag"`
}

type Channel struct {
	ID                 int64
	Code               string
	ProjectID          int64
	Name               string
	URLs               []string
	Conf               ChannelConf
	Disabled           bool
	LastNotify         *time.Time
	LastNotifyDuration *time.Duration
	LastError          string
	Created            time.Time
}

// IsNoop reports whether this channel skips the given status.
func (ch *Channel) IsNoop(status engine.Status) bool {
	switch status {
	case engine.StatusUp:
		return !ch.Conf.Up
	case engine.StatusDown:
		return !ch.Conf.Down
	}
	return true
}

// Value is the apprise URL list joined the way apprise-go expects (one Add per URL).
func (ch *Channel) Value() string { return strings.Join(ch.URLs, "\n") }

type NewChannel struct {
	ProjectID int64
	Name      string
	URLs      []string
	Conf      ChannelConf
}

func (db *DB) CreateChannel(ctx context.Context, in *NewChannel) (*Channel, error) {
	code := uuid.NewString()
	conf, err := json.Marshal(in.Conf)
	if err != nil {
		return nil, err
	}
	_, err = db.Write.ExecContext(ctx,
		`INSERT INTO channels(code, project_id, name, urls, conf, created) VALUES(?,?,?,?,?,?)`,
		code, in.ProjectID, in.Name, strings.Join(in.URLs, "\n"), string(conf),
		toDB(time.Now().UTC()))
	if err != nil {
		return nil, fmt.Errorf("建渠道：%w", err)
	}
	return db.ChannelByCode(ctx, code)
}

func (db *DB) UpdateChannel(ctx context.Context, ch *Channel) error {
	conf, err := json.Marshal(ch.Conf)
	if err != nil {
		return err
	}
	_, err = db.Write.ExecContext(ctx,
		`UPDATE channels SET name=?, urls=?, conf=?, disabled=? WHERE id=?`,
		ch.Name, strings.Join(ch.URLs, "\n"), string(conf), nullBool(ch.Disabled), ch.ID)
	return err
}

func (db *DB) DeleteChannel(ctx context.Context, id int64) error {
	res, err := db.Write.ExecContext(ctx, `DELETE FROM channels WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

const channelColumns = `id, code, project_id, name, urls, conf, disabled,
	last_notify, last_notify_duration_us, last_error, created`

func scanChannel(s Scanner) (*Channel, error) {
	ch := &Channel{}
	var urls, conf string
	var created int64
	var disabled int64
	var lastNotify sql.NullInt64
	var lastNotifyDuration sql.NullInt64
	err := s.Scan(&ch.ID, &ch.Code, &ch.ProjectID, &ch.Name, &urls, &conf, &disabled,
		&lastNotify, &lastNotifyDuration, &ch.LastError, &created)
	if err != nil {
		return nil, err
	}
	ch.URLs = splitLines(urls)
	if err := json.Unmarshal([]byte(conf), &ch.Conf); err != nil {
		ch.Conf = ChannelConf{Up: true, Down: true}
	}
	ch.Disabled = disabled != 0
	ch.Created = fromDB(created)
	ch.LastNotify = timePtr(lastNotify)
	ch.LastNotifyDuration = durPtr(lastNotifyDuration)
	return ch, nil
}

func splitLines(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func (db *DB) ChannelByID(ctx context.Context, id int64) (*Channel, error) {
	ch, err := scanChannel(db.Read.QueryRowContext(ctx,
		`SELECT `+channelColumns+` FROM channels WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return ch, err
}

func (db *DB) ChannelByCode(ctx context.Context, code string) (*Channel, error) {
	ch, err := scanChannel(db.Read.QueryRowContext(ctx,
		`SELECT `+channelColumns+` FROM channels WHERE code = ?`, code))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return ch, err
}

func (db *DB) Channels(ctx context.Context, projectID int64) ([]*Channel, error) {
	rows, err := db.Read.QueryContext(ctx,
		`SELECT `+channelColumns+` FROM channels WHERE project_id = ? ORDER BY id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Channel
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// ChannelsForCheck lists the fast senders first (by last notify duration) so a
// slow channel cannot hold up the rest of the alerts.
func (db *DB) ChannelsForCheck(ctx context.Context, checkID int64) ([]*Channel, error) {
	rows, err := db.Read.QueryContext(ctx,
		`SELECT `+channelColumns+` FROM channels
		  WHERE id IN (SELECT channel_id FROM channel_checks WHERE check_id = ?)
		    AND disabled = 0
		  ORDER BY (last_notify_duration_us IS NULL) DESC, last_notify_duration_us ASC, id`,
		checkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Channel
	for rows.Next() {
		ch, err := scanChannel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// RecordAttempt writes the channel health fields after a send.
func (db *DB) RecordAttempt(ctx context.Context, chID int64, at time.Time, dur time.Duration, errText string, disabled bool) error {
	_, err := db.Write.ExecContext(ctx,
		`UPDATE channels SET last_notify=?, last_notify_duration_us=?, last_error=?, disabled=?
		  WHERE id=?`,
		toDB(at), dur.Microseconds(), errText, nullBool(disabled), chID)
	return err
}

type Notification struct {
	ID          int64
	Code        string
	CheckID     *int64
	ChannelID   *int64
	ChannelName string
	CheckName   string
	CheckStatus engine.Status
	Created     time.Time
	Error       string
}

func (db *DB) StartNotification(ctx context.Context, checkID *int64, channelID int64, status engine.Status) (*Notification, error) {
	code := uuid.NewString()
	res, err := db.Write.ExecContext(ctx,
		`INSERT INTO notifications(code, owner_id, channel_id, check_status, created, error)
		 VALUES(?,?,?,?,?, 'Sending')`,
		code, nullInt64(checkID), channelID, string(status), toDB(time.Now().UTC()))
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Notification{ID: id, Code: code, CheckID: checkID, ChannelID: &channelID,
		CheckStatus: status, Created: time.Now().UTC()}, nil
}

func (db *DB) FinishNotification(ctx context.Context, id int64, errText string) error {
	_, err := db.Write.ExecContext(ctx, `UPDATE notifications SET error = ? WHERE id = ?`,
		errText, id)
	return err
}

// SetNotificationError applies a third-party delivery report.
func (db *DB) SetNotificationError(ctx context.Context, code, errText string, markDisabled bool) (*Notification, bool, error) {
	var (
		notif   *Notification
		matched bool
	)
	err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		var (
			id                int64
			channelID         sql.NullInt64
			ownerID           sql.NullInt64
			status, checkName string
			created           int64
		)
		err := tx.QueryRowContext(ctx,
			`SELECT n.id, n.owner_id, n.channel_id, n.check_status, n.created, c.name
			   FROM notifications n LEFT JOIN checks c ON c.id = n.owner_id
			  WHERE n.code = ?`, code).
			Scan(&id, &ownerID, &channelID, &status, &created, &checkName)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		// Anything older than an hour is a retry of a report we already handled.
		if time.Since(fromDB(created)) > time.Hour {
			return nil
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE notifications SET error = ? WHERE id = ?`, errText, id); err != nil {
			return err
		}
		if markDisabled && channelID.Valid {
			if _, err := tx.ExecContext(ctx,
				`UPDATE channels SET disabled = 1, last_error = ? WHERE id = ?`,
				errText, channelID.Int64); err != nil {
				return err
			}
		}
		notif = &Notification{ID: id, Code: code, CheckID: int64Ptr(ownerID),
			ChannelID: int64Ptr(channelID), CheckStatus: engine.Status(status),
			CheckName: checkName, Created: fromDB(created)}
		matched = true
		return nil
	})
	return notif, matched, err
}

func (db *DB) Notifications(ctx context.Context, limit int) ([]*Notification, error) {
	rows, err := db.Read.QueryContext(ctx,
		`SELECT n.id, n.code, n.owner_id, n.channel_id, n.check_status, n.created, n.error,
				COALESCE(c.name, ''), COALESCE(ch.name, '')
		   FROM notifications n
		   LEFT JOIN checks c ON c.id = n.owner_id
		   LEFT JOIN channels ch ON ch.id = n.channel_id
		  ORDER BY n.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Notification
	for rows.Next() {
		var (
			n                  = &Notification{}
			created            int64
			ownerID, channelID sql.NullInt64
			status             string
		)
		if err := rows.Scan(&n.ID, &n.Code, &ownerID, &channelID, &status, &created,
			&n.Error, &n.CheckName, &n.ChannelName); err != nil {
			return nil, err
		}
		n.CheckID = int64Ptr(ownerID)
		n.ChannelID = int64Ptr(channelID)
		n.CheckStatus = engine.Status(status)
		n.Created = fromDB(created)
		out = append(out, n)
	}
	return out, rows.Err()
}

// NotificationByCode loads one delivery record by its public code.
func (db *DB) NotificationByCode(ctx context.Context, code string) (*Notification, error) {
	var (
		n                  = &Notification{}
		created            int64
		ownerID, channelID sql.NullInt64
		status             string
	)
	err := db.Read.QueryRowContext(ctx,
		`SELECT n.id, n.code, n.owner_id, n.channel_id, n.check_status, n.created, n.error,
				COALESCE(c.name, ''), COALESCE(ch.name, '')
		   FROM notifications n
		   LEFT JOIN checks c ON c.id = n.owner_id
		   LEFT JOIN channels ch ON ch.id = n.channel_id
		  WHERE n.code = ?`, code).
		Scan(&n.ID, &n.Code, &ownerID, &channelID, &status, &created,
			&n.Error, &n.CheckName, &n.ChannelName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	n.CheckID = int64Ptr(ownerID)
	n.ChannelID = int64Ptr(channelID)
	n.CheckStatus = engine.Status(status)
	n.Created = fromDB(created)
	return n, nil
}

// NotificationsForCheck lists delivery records for one check since `since`,
// newest first. It feeds the per-check event stream.
func (db *DB) NotificationsForCheck(ctx context.Context, checkID int64, since time.Time, limit int) ([]*Notification, error) {
	rows, err := db.Read.QueryContext(ctx,
		`SELECT n.id, n.code, n.owner_id, n.channel_id, n.check_status, n.created, n.error,
				COALESCE(c.name, ''), COALESCE(ch.name, '')
		   FROM notifications n
		   LEFT JOIN checks c ON c.id = n.owner_id
		   LEFT JOIN channels ch ON ch.id = n.channel_id
		  WHERE n.owner_id = ? AND n.created >= ?
		  ORDER BY n.id DESC LIMIT ?`, checkID, toDB(since), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Notification
	for rows.Next() {
		var (
			n                  = &Notification{}
			created            int64
			ownerID, channelID sql.NullInt64
			status             string
		)
		if err := rows.Scan(&n.ID, &n.Code, &ownerID, &channelID, &status, &created,
			&n.Error, &n.CheckName, &n.ChannelName); err != nil {
			return nil, err
		}
		n.CheckID = int64Ptr(ownerID)
		n.ChannelID = int64Ptr(channelID)
		n.CheckStatus = engine.Status(status)
		n.Created = fromDB(created)
		out = append(out, n)
	}
	return out, rows.Err()
}

// LastDeliveredNotice reports when the most recent *successful* notification of
// the given status reached a channel. Rows still marked Sending or carrying an
// error do not count, so reminders wait for a real delivery.
func (db *DB) LastDeliveredNotice(ctx context.Context, checkID, channelID int64, status engine.Status) (*time.Time, error) {
	var created int64
	err := db.Read.QueryRowContext(ctx,
		`SELECT created FROM notifications
		  WHERE owner_id = ? AND channel_id = ? AND check_status = ? AND error = ''
		  ORDER BY id DESC LIMIT 1`,
		checkID, channelID, string(status)).Scan(&created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t := fromDB(created)
	return &t, nil
}

func nullInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func int64Ptr(v sql.NullInt64) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}
