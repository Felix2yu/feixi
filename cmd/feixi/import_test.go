package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yufei/feixi/internal/cronex"
	"github.com/yufei/feixi/internal/store"
)

const exportFixture = `{"checks":[
 {"name":"备份数据库","uuid":"3d2f8bd8-cb1a-4a6c-b0a2-6b8e0f8b7c11","grace":600,"timeout":3600,
  "channels":"11111111-1111-1111-1111-111111111111","last_ping":null,"next_ping":null},
 {"name":"工作日每4小时","schedule":"Mon-Fri *-*-* 0/4:00","grace":600,"tz":"Asia/Shanghai"},
 {"name":"月末清理","schedule":"*-*-~1 12:00","grace":600}
]}`

func openImportDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "feixi.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	db.Sched = cronex.Scheduler{}
	db.MaxChecks = 100
	db.PingLogLimit = 100
	return db
}

func TestImportChecksWritesDefinitionsAndSkipsTheRest(t *testing.T) {
	db := openImportDB(t)
	var buf bytes.Buffer
	s, err := importChecks(context.Background(), &buf, db, []byte(exportFixture), false)
	if err != nil {
		t.Fatalf("importChecks: %v", err)
	}
	if s.created != 2 || s.skipped != 1 || s.duplicates != 0 {
		t.Errorf("汇总 = %+v，想要 导入 2 跳过 1 已存在 0", s)
	}
	report := buf.String()
	for _, want := range []string{
		"3d2f8bd8-cb1a-4a6c-b0a2-6b8e0f8b7c11",
		"0 0/4 * * Mon-Fri",
		"Asia/Shanghai",
		"timeout=3600s，UTC",
		"建议改成月初",
		"源渠道 1 个未迁移",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("报告里缺 %q：\n%s", want, report)
		}
	}

	c, err := db.CheckByCode(context.Background(), "3d2f8bd8-cb1a-4a6c-b0a2-6b8e0f8b7c11")
	if err != nil {
		t.Fatalf("按源 UUID 查不到：%v", err)
	}
	if c.Timeout != time.Hour || c.Grace != 10*time.Minute || c.Kind != "simple" {
		t.Errorf("导入后的形状不对：%+v", c)
	}
	if ids, _ := db.CheckChannels(context.Background(), c.ID); len(ids) != 0 {
		t.Errorf("渠道应该留空等人手勾，得到 %v", ids)
	}
}

func TestImportIsIdempotentOnTheSourceUUID(t *testing.T) {
	db := openImportDB(t)
	ctx := context.Background()
	if _, err := importChecks(ctx, &bytes.Buffer{}, db, []byte(exportFixture), false); err != nil {
		t.Fatalf("first import: %v", err)
	}
	var buf bytes.Buffer
	s, err := importChecks(ctx, &buf, db, []byte(exportFixture), false)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	// 只有源里带 uuid 的行能按 UUID 对上；只读 key 导出的行每次都会新建。
	if s.created != 1 || s.duplicates != 1 {
		t.Errorf("重跑应该把带 uuid 的那条报成已存在，得到 %+v", s)
	}
	if !strings.Contains(buf.String(), "库里已有同一个 UUID") {
		t.Errorf("报告：\n%s", buf.String())
	}
}

func TestImportDryRunLeavesTheDatabaseEmpty(t *testing.T) {
	db := openImportDB(t)
	ctx := context.Background()
	var buf bytes.Buffer
	s, err := importChecks(ctx, &buf, db, []byte(exportFixture), true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if s.created != 2 {
		t.Errorf("dry run 也该给出将要导入的条数，得到 %+v", s)
	}
	if !strings.Contains(buf.String(), "没有写库") {
		t.Errorf("报告里该说明没写库：\n%s", buf.String())
	}
	project, err := db.Project(ctx)
	if err != nil {
		t.Fatalf("project: %v", err)
	}
	checks, err := db.ListChecks(ctx, project.ID, store.CheckFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(checks) != 0 {
		t.Errorf("--dry-run 写进了 %d 条", len(checks))
	}
}

func TestImportRejectsAFullDatabase(t *testing.T) {
	db := openImportDB(t)
	db.MaxChecks = 1
	ctx := context.Background()
	project, err := db.Seed(ctx, "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := db.CreateCheck(ctx, &store.NewCheck{
		ProjectID: project.ID, Name: "先占位", Kind: "simple",
		Timeout: time.Hour, Grace: time.Minute, TZ: "UTC",
	}); err != nil {
		t.Fatalf("占位：%v", err)
	}
	var buf bytes.Buffer
	s, err := importChecks(ctx, &buf, db, []byte(`{"checks":[{"name":"进不去","timeout":600,"grace":600}]}`), false)
	if err != nil {
		t.Fatalf("importChecks: %v", err)
	}
	if s.created != 0 || s.skipped != 1 {
		t.Errorf("上限该拦下这条，得到 %+v", s)
	}
	if !strings.Contains(buf.String(), "FEIXI_MAX_CHECKS") {
		t.Errorf("报告该告诉他怎么放宽：\n%s", buf.String())
	}
}

func TestParseImportArgsAcceptsFlagsAfterPath(t *testing.T) {
	// flag 包在第一个位置参数后就停止解析，`import checks.json --dry-run` 得自己接住。
	for _, args := range [][]string{
		{"checks.json", "--dry-run"},
		{"-dry-run", "checks.json"},
	} {
		path, dryRun, err := parseImportArgs(args)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if path != "checks.json" || !dryRun {
			t.Errorf("%v 解析成 %q / %v", args, path, dryRun)
		}
	}
	for _, args := range [][]string{{}, {"a.json", "b.json"}, {"--nope"}} {
		if _, _, err := parseImportArgs(args); err == nil {
			t.Errorf("%v 应该报错", args)
		}
	}
}
