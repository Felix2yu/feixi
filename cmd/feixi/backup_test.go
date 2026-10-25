package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/Felix2yu/feixi/internal/cronex"
	"github.com/Felix2yu/feixi/internal/engine"
	"github.com/Felix2yu/feixi/internal/store"
)

const sqliteMagic = "SQLite format 3\x00"

// makeBackupDB 走应用正常的 Open 路径建一个真库：迁移 + seed + 一条检查项，
// 然后原样关闭，留给 backup 子命令一个"应用正在跑过的库"。
func makeBackupDB(t *testing.T, path string) {
	t.Helper()
	db, err := store.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.Sched = cronex.Scheduler{}
	if _, err := db.Seed(context.Background(), "secret"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	project, err := db.Project(context.Background())
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	if _, err := db.CreateCheck(context.Background(), &store.NewCheck{
		ProjectID: project.ID, Name: "备份验证", Kind: engine.KindSimple,
		Timeout: engine.DefaultTimeout, Grace: engine.DefaultGrace, TZ: "UTC",
	}); err != nil {
		t.Fatalf("create check: %v", err)
	}
	db.Close()
}

func TestRunBackupWritesSQLiteSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feixi.db")
	makeBackupDB(t, path)
	t.Setenv("FEIXI_DB", path)

	var buf bytes.Buffer
	if err := runBackup(&buf); err != nil {
		t.Fatalf("runBackup: %v", err)
	}
	if buf.Len() < 16 {
		t.Fatalf("快照只有 %d 字节，不像一份数据库", buf.Len())
	}
	if got := buf.Bytes()[:16]; !bytes.Equal(got, []byte(sqliteMagic)) {
		t.Errorf("前 16 字节 = %q，想要 %q", got, sqliteMagic)
	}
}

// 快照要能被同一个驱动重新打开，插入的行也必须在。
func TestRunBackupSnapshotReopensWithTheRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "feixi.db")
	makeBackupDB(t, path)
	t.Setenv("FEIXI_DB", path)

	var buf bytes.Buffer
	if err := runBackup(&buf); err != nil {
		t.Fatalf("runBackup: %v", err)
	}

	snapPath := filepath.Join(t.TempDir(), "snapshot.db")
	if err := os.WriteFile(snapPath, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := store.OpenForExport(snapPath)
	if err != nil {
		t.Fatalf("重新打开快照： %v", err)
	}
	defer snap.Close()
	var name string
	if err := snap.Read.QueryRow(`SELECT name FROM checks LIMIT 1`).Scan(&name); err != nil {
		t.Fatalf("读快照里的行： %v", err)
	}
	if name != "备份验证" {
		t.Errorf("快照里的检查项 = %q，想要 %q", name, "备份验证")
	}
}

// 库不存在时必须报错退出，绝不能顺手建一个空库。
func TestRunBackupDoesNotCreateMissingDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.db")
	t.Setenv("FEIXI_DB", path)

	var buf bytes.Buffer
	if err := runBackup(&buf); err == nil {
		t.Fatal("expect an error")
	}
	if buf.Len() != 0 {
		t.Errorf("stdout = %d 字节，想要 0", buf.Len())
	}
	if _, err := os.Stat(path); err == nil {
		t.Errorf("备份进程创建了不存在的数据库：%s", path)
	}
}
