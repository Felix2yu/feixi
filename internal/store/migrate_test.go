package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestProjectLookupsByKey(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	p := testProject(t, db)

	byPing, err := db.ProjectByPingKey(ctx, p.PingKey)
	if err != nil {
		t.Fatal(err)
	}
	if byPing.ID != p.ID || byPing.Name != p.Name || byPing.Code != p.Code {
		t.Errorf("ProjectByPingKey = %+v, want %+v", byPing, p)
	}
	if byPing.Created.IsZero() {
		t.Error("created should round-trip")
	}

	// ping_key 是空串时不能把默认值那行匹配上。
	if _, err := db.ProjectByPingKey(ctx, ""); err != ErrNotFound {
		t.Errorf("empty ping key = %v, want ErrNotFound", err)
	}
	if _, err := db.ProjectByPingKey(ctx, uuid.NewString()); err != ErrNotFound {
		t.Errorf("unknown ping key = %v, want ErrNotFound", err)
	}

	byBadge, err := db.ProjectByBadgeKey(ctx, p.BadgeKey)
	if err != nil {
		t.Fatal(err)
	}
	if byBadge.ID != p.ID || byBadge.PingKey != p.PingKey {
		t.Errorf("ProjectByBadgeKey = %+v", byBadge)
	}
	if _, err := db.ProjectByBadgeKey(ctx, "ghost"); err != ErrNotFound {
		t.Errorf("unknown badge key = %v, want ErrNotFound", err)
	}

	// show_slugs 是 0/1 存的布尔。
	if p.ShowSlugs {
		t.Error("seeded project should not show slugs")
	}
	if _, err := db.Write.ExecContext(ctx, `UPDATE projects SET show_slugs = 1 WHERE id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}
	reloaded, err := db.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.ShowSlugs {
		t.Error("show_slugs did not round-trip")
	}
}

func TestProjectByPingKeyRejectsAmbiguousKeys(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	// ping_key 上没有唯一索引，历史上的重复前缀必须显式报错而不是任选一行。
	key := randomKey(7)
	for i := 0; i < 2; i++ {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO projects(code, name, badge_key, ping_key, show_slugs, created)
			 VALUES(?,?,?,?,0,?)`,
			uuid.NewString(), "项目", uuid.NewString(), key, toDB(time.Now().UTC())); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.ProjectByPingKey(ctx, key); err != ErrNotUnique {
		t.Errorf("err = %v, want ErrNotUnique", err)
	}
}

func TestSeedKeepsOrReplacesTheSecret(t *testing.T) {
	dir := t.TempDir()
	db, err := Open(filepath.Join(dir, "seed.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()

	if _, err := db.Seed(ctx, ""); err != nil {
		t.Fatal(err)
	}
	generated, err := db.Setting(ctx, "secret_key")
	if err != nil {
		t.Fatal(err)
	}
	// 没给 FEIXI_SECRET_KEY 时随机生成 32 字节的十六进制。
	if len(generated) != 64 || strings.ContainsAny(generated, "ghijklmnopqrstuvwxyz") {
		t.Errorf("generated secret = %q, want 64 hex chars", generated)
	}

	// 再播种一次不该换掉签名密钥，否则已分发的 badge URL 全失效。
	first, err := db.Project(ctx)
	if err != nil {
		t.Fatal(err)
	}
	again, err := db.Seed(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.BadgeKey != first.BadgeKey {
		t.Errorf("reseed changed the project: %+v vs %+v", again, first)
	}
	still, err := db.Setting(ctx, "secret_key")
	if err != nil {
		t.Fatal(err)
	}
	if still != generated {
		t.Error("reseed rotated the secret")
	}

	// 显式配置的值优先级最高，且能覆盖已存的那一个。
	if _, err := db.Seed(ctx, "pinned-secret"); err != nil {
		t.Fatal(err)
	}
	pinned, err := db.Setting(ctx, "secret_key")
	if err != nil {
		t.Fatal(err)
	}
	if pinned != "pinned-secret" {
		t.Errorf("secret = %q, want the pinned value", pinned)
	}
	// 与已存值相同的 override 走「不用重写」的分支。
	if _, err := db.Seed(ctx, "pinned-secret"); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Setting(ctx, "no_such_key"); err == nil {
		t.Error("missing setting should report sql.ErrNoRows")
	}
}

func TestPutSettingOverwrites(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	if err := db.PutSetting(ctx, "log_level", "debug"); err != nil {
		t.Fatal(err)
	}
	if v, err := db.Setting(ctx, "log_level"); err != nil || v != "debug" {
		t.Fatalf("setting = %q, %v", v, err)
	}
	if err := db.PutSetting(ctx, "log_level", "warn"); err != nil {
		t.Fatal(err)
	}
	if v, err := db.Setting(ctx, "log_level"); err != nil || v != "warn" {
		t.Errorf("after upsert = %q, %v, want warn", v, err)
	}
}

func TestRandomKey(t *testing.T) {
	key := randomKey(7)
	if len(key) != 7 {
		t.Fatalf("len = %d, want 7", len(key))
	}
	// 字母表刻意去掉易混的 i/l/o/0/1。
	for _, r := range key {
		if !strings.ContainsRune(keyAlphabet, r) {
			t.Errorf("unexpected rune %q in %q", r, key)
		}
	}
	if randomKey(7) == randomKey(7) {
		t.Error("two draws collided")
	}
	if got := len(randomKey(0)); got != 0 {
		t.Errorf("len(randomKey(0)) = %d", got)
	}
}

func TestNullHelpers(t *testing.T) {
	if got := fromNullString(sql.NullString{String: "值", Valid: true}); got != "值" {
		t.Errorf("fromNullString = %q", got)
	}
	if got := fromNullString(sql.NullString{}); got != "" {
		t.Errorf("NULL should read as empty, got %q", got)
	}
	if got := nullInt64(nil); got.Valid {
		t.Error("nullInt64(nil) should be invalid")
	}
	v := int64(3)
	if got := nullInt64(&v); !got.Valid || got.Int64 != 3 {
		t.Errorf("nullInt64 = %+v", got)
	}
	if got := boolInt(false); got != 0 || boolInt(true) != 1 {
		t.Error("boolInt should map to 0/1")
	}
	if got := nullBool(false); got != 0 {
		t.Errorf("nullBool(false) = %d", got)
	}
}
