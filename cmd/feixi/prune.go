package main

import (
	"context"
	"log/slog"

	"github.com/yufei/feixi/internal/config"
	"github.com/yufei/feixi/internal/cronex"
	"github.com/yufei/feixi/internal/store"
)

// runPrune trims every check's ping history in one pass; the per-ping hint only
// covers checks that are still being pinged.
func runPrune(ctx context.Context, log *slog.Logger) error {
	cfg := config.Default()
	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()
	db.Sched = cronex.Scheduler{}

	n, err := db.PruneAll(ctx, cfg.PingLogLimit)
	if err != nil {
		return err
	}
	log.Info("清理完成", "checks", n)
	return nil
}
