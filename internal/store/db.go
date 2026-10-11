// Package store is the only place that touches the database.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/yufei/feixi/internal/engine"

	_ "modernc.org/sqlite"
)

type DB struct {
	// Write serializes every mutation: a single connection means our own
	// transactions can never contend for the SQLite write lock.
	Write *sql.DB
	// Read serves queries; WAL lets them run while a write is in flight.
	Read *sql.DB
	Path string

	// Limits, wired from config after Open.
	MaxChecks    int
	PingLogLimit int

	// Sched computes cron occurrences for the status machine.
	Sched engine.Scheduler
}

const pragmas = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)" +
	"&_pragma=synchronous(NORMAL)&_pragma=foreign_keys(1)" +
	"&_pragma=temp_store(MEMORY)&_pragma=cache_size(-20000)"

func Open(path string) (*DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据目录：%w", err)
		}
	}

	write, err := openConn(path, pragmas+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	write.SetMaxOpenConns(1)

	read, err := openConn(path, pragmas)
	if err != nil {
		write.Close()
		return nil, err
	}
	read.SetMaxOpenConns(4)

	db := &DB{Write: write, Read: read, Path: path}
	if err := db.Migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func openConn(path, query string) (*sql.DB, error) {
	dsn := "file:" + filepath.Clean(path) + "?" + query
	handle, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库：%w", err)
	}
	if err := handle.Ping(); err != nil {
		handle.Close()
		return nil, fmt.Errorf("连接数据库：%w", err)
	}
	return handle, nil
}

func (db *DB) Close() error {
	return errors.Join(db.Read.Close(), db.Write.Close())
}

// WriteTx runs fn inside BEGIN IMMEDIATE … COMMIT, so the row it reads and the
// rows it writes cannot be interleaved with another writer.
func (db *DB) WriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := db.Write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// DB stores timestamps as integer microseconds so SQLite keeps ordering and
// range scans correct without any timezone parsing.
func toDB(t time.Time) int64 { return t.UnixMicro() }

func fromDB(us int64) time.Time { return time.UnixMicro(us).UTC() }

func nullTime(t *time.Time) sql.NullInt64 {
	if t == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: t.UnixMicro(), Valid: true}
}

func timePtr(v sql.NullInt64) *time.Time {
	if !v.Valid {
		return nil
	}
	t := time.UnixMicro(v.Int64).UTC()
	return &t
}

func nullDur(d *time.Duration) sql.NullInt64 {
	if d == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: d.Microseconds(), Valid: true}
}

func durPtr(v sql.NullInt64) *time.Duration {
	if !v.Valid {
		return nil
	}
	d := time.Duration(v.Int64) * time.Microsecond
	return &d
}

func nullStr(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}

func strPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func nullBool(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func fromNullString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return v.String
}
