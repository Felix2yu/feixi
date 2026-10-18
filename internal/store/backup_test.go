package store

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

// 导出进程只负责读：库不存在就报错，绝不建库、不跑迁移。
func TestOpenForExportRefusesMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.db")
	if _, err := OpenForExport(path); err == nil {
		t.Fatal("expect an error")
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("导出打开创建了不存在的数据库：%s", path)
	}
}

// OpenForExport 不跑迁移：删掉迁移记录模拟"待升级的旧库"，导出前后源库的
// schema_migrations 都必须还是空的（应用自己的 Open 会在这里重放全部迁移）。
func TestOpenForExportDoesNotMigrate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feixi.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Seed(context.Background(), "secret"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.Write.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatalf("clear migration history: %v", err)
	}
	db.Close()

	exp, err := OpenForExport(path)
	if err != nil {
		t.Fatalf("open for export: %v", err)
	}
	var buf bytes.Buffer
	if err := exp.WriteSQLiteSnapshot(&buf); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	exp.Close()

	// 源库：导出连接不该把迁移记录写回来。
	check, err := OpenForExport(path)
	if err != nil {
		t.Fatalf("reopen source: %v", err)
	}
	defer check.Close()
	var names int
	if err := check.Read.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&names); err != nil {
		t.Fatalf("count source migrations: %v", err)
	}
	if names != 0 {
		t.Errorf("源库 schema_migrations = %d 行，导出进程不该跑迁移", names)
	}

	// 快照里同样不该有被重放的迁移记录。
	snapPath := filepath.Join(t.TempDir(), "snapshot.db")
	if err := os.WriteFile(snapPath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := OpenForExport(snapPath)
	if err != nil {
		t.Fatalf("reopen snapshot: %v", err)
	}
	defer snap.Close()
	if err := snap.Read.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&names); err != nil {
		t.Fatalf("count snapshot migrations: %v", err)
	}
	if names != 0 {
		t.Errorf("快照 schema_migrations = %d 行，想要 0", names)
	}
	var projects int
	if err := snap.Read.QueryRow(`SELECT COUNT(*) FROM projects`).Scan(&projects); err != nil {
		t.Fatalf("count snapshot projects: %v", err)
	}
	if projects != 1 {
		t.Errorf("快照 projects = %d，想要 1（seed 的那一行）", projects)
	}
}

// 导出连接不改写库头部：源库保持应用设好的 WAL。
func TestOpenForExportKeepsJournalMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feixi.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.Close()

	exp, err := OpenForExport(path)
	if err != nil {
		t.Fatalf("open for export: %v", err)
	}
	var buf bytes.Buffer
	if err := exp.WriteSQLiteSnapshot(&buf); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	exp.Close()

	after, err := OpenForExport(path)
	if err != nil {
		t.Fatalf("reopen source: %v", err)
	}
	defer after.Close()
	var mode string
	if err := after.Read.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("源库 journal_mode = %q，导出前后都应是 wal", mode)
	}
}
