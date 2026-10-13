package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/yufei/feixi/internal/config"
	"github.com/yufei/feixi/internal/cronex"
	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/hcimport"
	"github.com/yufei/feixi/internal/store"
)

// runImport seeds 飞檄 from a healthchecks export. It writes the database
// directly rather than through the API: the management endpoints are open, and
// a bulk create over unauthenticated HTTP is not a surface worth gaining just
// to move a handful of checks.
const importUsage = "用法：feixi import <checks.json|-> [--dry-run]"

// parseImportArgs 自己拆参数：flag 包在第一个位置参数之后就停止解析，
// `feixi import checks.json --dry-run` 会被当成多余的路径。
func parseImportArgs(args []string) (path string, dryRun bool, err error) {
	for _, a := range args {
		switch {
		case a == "-dry-run" || a == "--dry-run":
			dryRun = true
		case a == "-h" || a == "--help":
			return "", false, flag.ErrHelp
		case strings.HasPrefix(a, "-"):
			return "", false, fmt.Errorf("未知参数 %q（%s）", a, importUsage)
		case path != "":
			return "", false, fmt.Errorf("只能给一个文件（%s）", importUsage)
		default:
			path = a
		}
	}
	if path == "" {
		return "", false, fmt.Errorf(importUsage)
	}
	return path, dryRun, nil
}

func runImport(ctx context.Context, log *slog.Logger, args []string) error {
	path, dryRun, err := parseImportArgs(args)
	if err != nil {
		return err
	}
	raw, err := readInput(path)
	if err != nil {
		return err
	}

	cfg := config.Default()
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	db.Sched = cronex.Scheduler{}

	summary, err := importChecks(ctx, os.Stdout, db, raw, dryRun)
	if err != nil {
		return err
	}
	if summary.skipped+summary.duplicates > 0 {
		log.Warn("有检查项没有导入", "skipped", summary.skipped, "duplicates", summary.duplicates)
	}
	return nil
}

type importSummary struct {
	total, created, skipped, duplicates int
}

func importChecks(ctx context.Context, out io.Writer, db *store.DB, raw []byte, dryRun bool) (importSummary, error) {
	project, err := db.Seed(ctx, "")
	if err != nil {
		return importSummary{}, fmt.Errorf("准备项目：%w", err)
	}
	items, err := hcimport.Parse(raw, project.ID)
	if err != nil {
		return importSummary{}, err
	}
	takenCodes, takenSlugs, err := existingKeys(ctx, db, project.ID)
	if err != nil {
		return importSummary{}, err
	}

	s := importSummary{total: len(items)}
	for _, item := range items {
		switch {
		case !item.Importable():
			s.skipped++
			fmt.Fprintf(out, "[跳过] %s：%s\n", item.Name, item.Problem)
		case item.New.Code != "" && takenCodes[item.New.Code]:
			s.duplicates++
			fmt.Fprintf(out, "[已存在] %s：库里已有同一个 UUID，未改动\n", item.Name)
		case item.New.Slug != "" && takenSlugs[item.New.Slug]:
			s.skipped++
			fmt.Fprintf(out, "[跳过] %s：slug %q 已被别的检查项占用\n", item.Name, item.New.Slug)
		case dryRun:
			s.created++
			fmt.Fprintf(out, "[待导入] %s（%s）\n", item.Name, planLabel(item.New))
		default:
			c, err := db.CreateCheck(ctx, item.New)
			if err != nil {
				s.skipped++
				fmt.Fprintf(out, "[失败] %s：%s\n", item.Name, importReason(err))
				continue
			}
			s.created++
			takenCodes[item.New.Code] = true
			takenSlugs[item.New.Slug] = true
			fmt.Fprintf(out, "[导入] %s（%s）%s\n", item.Name, planLabel(item.New), c.Code)
		}
		for _, note := range item.Notes {
			fmt.Fprintf(out, "       └ %s\n", note)
		}
	}

	fmt.Fprintf(out, "\n共 %d 条：导入 %d，跳过 %d，已存在 %d\n",
		s.total, s.created, s.skipped, s.duplicates)
	if dryRun {
		fmt.Fprintln(out, "以上是 --dry-run，没有写库。")
	} else if s.created > 0 {
		fmt.Fprintln(out, "只搬了定义：历史 ping 与状态不迁移，渠道留空，去界面上逐个订阅。")
	}
	return s, nil
}

func kindLabel(k engine.Kind) string {
	if k == engine.KindCron {
		return "cron"
	}
	return "固定间隔"
}

// planLabel 报告的是落库后的形态，不是源里的写法，降段轨迹留给 Notes。
func planLabel(in *store.NewCheck) string {
	plan := fmt.Sprintf("timeout=%ds", int(in.Timeout/time.Second))
	if in.Kind == engine.KindCron {
		plan = in.Schedule
	}
	return fmt.Sprintf("%s，%s，%s", kindLabel(in.Kind), plan, in.TZ)
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return io.ReadAll(os.Stdin)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读 %s：%w", path, err)
	}
	return raw, nil
}

func existingKeys(ctx context.Context, db *store.DB, projectID int64) (codes, slugs map[string]bool, err error) {
	checks, err := db.ListChecks(ctx, projectID, store.CheckFilter{})
	if err != nil {
		return nil, nil, fmt.Errorf("列检查项：%w", err)
	}
	codes = make(map[string]bool, len(checks))
	slugs = make(map[string]bool, len(checks))
	for _, c := range checks {
		codes[c.Code] = true
		if c.Slug != "" {
			slugs[c.Slug] = true
		}
	}
	return codes, slugs, nil
}

func importReason(err error) string {
	msg := err.Error()
	switch {
	case errors.Is(err, store.ErrTooManyChecks):
		return store.ErrTooManyChecks.Error() + "（可用 FEIXI_MAX_CHECKS 临时放宽）"
	case strings.Contains(msg, "checks.code"):
		return "库里已有同一个 UUID"
	case strings.Contains(msg, "project_slug"):
		return "slug 已被别的检查项占用"
	}
	return msg
}
