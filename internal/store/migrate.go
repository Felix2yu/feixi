package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

//go:embed migrate/*.sql
var migrations embed.FS

func (db *DB) Migrate() error {
	ctx := context.Background()
	if _, err := db.Write.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (
			name       TEXT PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return fmt.Errorf("建迁移表：%w", err)
	}

	entries, err := fs.ReadDir(migrations, "migrate")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		var seen string
		err := db.Read.QueryRowContext(ctx,
			`SELECT name FROM schema_migrations WHERE name = ?`, name).Scan(&seen)
		switch {
		case err == nil:
			continue
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}

		body, err := migrations.ReadFile("migrate/" + name)
		if err != nil {
			return err
		}
		if err := db.WriteTx(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, string(body)); err != nil {
				return fmt.Errorf("执行迁移 %s：%w", name, err)
			}
			_, err := tx.ExecContext(ctx,
				`INSERT INTO schema_migrations(name, applied_at) VALUES(?, ?)`,
				name, toDB(time.Now().UTC()))
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

type Project struct {
	ID        int64
	Code      string
	Name      string
	BadgeKey  string
	PingKey   string
	ShowSlugs bool
	Created   time.Time
}

func (db *DB) Project(ctx context.Context) (*Project, error) {
	row := db.Read.QueryRowContext(ctx,
		`SELECT id, code, name, badge_key, ping_key, show_slugs, created
		   FROM projects ORDER BY id LIMIT 1`)
	p := &Project{}
	var show, created int64
	if err := row.Scan(&p.ID, &p.Code, &p.Name, &p.BadgeKey, &p.PingKey, &show, &created); err != nil {
		return nil, err
	}
	p.ShowSlugs = show != 0
	p.Created = fromDB(created)
	return p, nil
}

// Seed creates the single project row and the badge signing key. An explicit
// FEIXI_SECRET_KEY always wins, so a operator can rotate or pin it.
func (db *DB) Seed(ctx context.Context, overrideSecret string) (*Project, error) {
	project, err := db.Project(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO projects(code, name, badge_key, ping_key, show_slugs, created)
			 VALUES(?, ?, ?, ?, 0, ?)`,
			uuid.NewString(), "默认项目", uuid.NewString(), randomKey(7), toDB(time.Now().UTC())); err != nil {
			return nil, err
		}
		project, err = db.Project(ctx)
	}
	if err != nil {
		return nil, err
	}

	stored, err := db.Setting(ctx, "secret_key")
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if overrideSecret != "" {
			err = db.PutSetting(ctx, "secret_key", overrideSecret)
		} else {
			buf := make([]byte, 32)
			if _, err := rand.Read(buf); err != nil {
				return nil, err
			}
			err = db.PutSetting(ctx, "secret_key", hex.EncodeToString(buf))
		}
		if err != nil {
			return nil, err
		}
	case err != nil:
		return nil, err
	case overrideSecret != "" && overrideSecret != stored:
		if err := db.PutSetting(ctx, "secret_key", overrideSecret); err != nil {
			return nil, err
		}
	}
	return project, nil
}

func (db *DB) ProjectByPingKey(ctx context.Context, pingKey string) (*Project, error) {
	if pingKey == "" {
		return nil, ErrNotFound
	}
	rows, err := db.Read.QueryContext(ctx,
		`SELECT id, code, name, badge_key, ping_key, show_slugs, created
		   FROM projects WHERE ping_key = ?`, pingKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var found *Project
	for rows.Next() {
		if found != nil {
			return nil, ErrNotUnique
		}
		p := &Project{}
		var show, created int64
		if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.BadgeKey, &p.PingKey, &show, &created); err != nil {
			return nil, err
		}
		p.ShowSlugs = show != 0
		p.Created = fromDB(created)
		found = p
	}
	if found == nil {
		return nil, ErrNotFound
	}
	return found, rows.Err()
}

// ProjectByBadgeKey resolves the shared badge URL's project.
func (db *DB) ProjectByBadgeKey(ctx context.Context, badgeKey string) (*Project, error) {
	rows, err := db.Read.QueryContext(ctx,
		`SELECT id, code, name, badge_key, ping_key, show_slugs, created FROM projects WHERE badge_key = ?`,
		badgeKey)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, ErrNotFound
	}
	p := &Project{}
	var show, created int64
	if err := rows.Scan(&p.ID, &p.Code, &p.Name, &p.BadgeKey, &p.PingKey, &show, &created); err != nil {
		return nil, err
	}
	p.ShowSlugs = show != 0
	p.Created = fromDB(created)
	return p, rows.Err()
}

func (db *DB) Setting(ctx context.Context, key string) (string, error) {
	var value string
	err := db.Read.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	return value, err
}

func (db *DB) PutSetting(ctx context.Context, key, value string) error {
	_, err := db.Write.ExecContext(ctx,
		`INSERT INTO settings(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

const keyAlphabet = "abcdefghijkmnopqrstuvwxyz23456789"

// randomKey spells a short, human-friendly prefix for slug ping URLs.
func randomKey(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		panic(err)
	}
	out := make([]byte, n)
	for i := range out {
		out[i] = keyAlphabet[int(buf[i])%len(keyAlphabet)]
	}
	return string(out)
}
