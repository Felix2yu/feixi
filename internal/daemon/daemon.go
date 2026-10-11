// Package daemon runs the two background loops: the going-down scanner and the
// notification queue.
package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/yufei/feixi/internal/config"
	"github.com/yufei/feixi/internal/store"
)

// Notifier delivers one flip to its channels.
type Notifier interface {
	Process(ctx context.Context, flip *store.PendingFlip) error
}

type Daemon struct {
	DB       *store.DB
	Cfg      *config.Config
	Notifier Notifier
	Log      *slog.Logger

	prune chan int64
}

func New(db *store.DB, cfg *config.Config, log *slog.Logger) *Daemon {
	return &Daemon{
		DB:       db,
		Cfg:      cfg,
		Notifier: noopNotifier{},
		Log:      log,
		prune:    make(chan int64, 128),
	}
}

type noopNotifier struct{}

func (noopNotifier) Process(context.Context, *store.PendingFlip) error { return nil }

// QueuePrune asks for trimming without making the ping request wait for it.
func (d *Daemon) QueuePrune(checkID int64) {
	select {
	case d.prune <- checkID:
	default:
		// The nightly sweep still runs; dropping the hint is not data loss.
	}
}

func (d *Daemon) Run(ctx context.Context) {
	go d.scanGoingDown(ctx)
	go d.drainFlips(ctx)
	go d.pruneLoop(ctx)
}

func (d *Daemon) scanGoingDown(ctx context.Context) {
	ticker := time.NewTicker(d.Cfg.AlertScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.sweep(ctx)
		}
	}
}

// sweep drains every check whose deadline has passed, one transaction at a
// time so no scan holds the write lock across many rows.
func (d *Daemon) sweep(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		flip, err := d.DB.ClaimGoingDown(ctx, time.Now().UTC())
		if err != nil {
			d.Log.Warn("扫描到期检查项失败", "err", err)
			return
		}
		if flip == nil {
			return
		}
		d.Log.Info("检查项超时转宕机", "check", flip.CheckID, "from", flip.OldStatus)
	}
}

func (d *Daemon) drainFlips(ctx context.Context) {
	sem := make(chan struct{}, d.Cfg.NotifyConcurrency)

	for {
		if ctx.Err() != nil {
			return
		}

		flip, claimed, err := d.DB.ClaimNextFlip(ctx, time.Now().UTC())
		if err != nil {
			d.Log.Warn("取通知任务失败", "err", err)
			sleep(ctx, 2*time.Second)
			continue
		}
		if !claimed {
			sleep(ctx, d.Cfg.FlipIdleSleep)
			continue
		}

		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		go func(f *store.PendingFlip) {
			defer func() { <-sem }()
			dlog := d.Log.With("check", f.Check.ID, "to", f.Flip.NewStatus)
			if err := d.Notifier.Process(ctx, f); err != nil {
				dlog.Warn("通知发送失败", "err", err)
				return
			}
			dlog.Info("通知已处理")
		}(flip)
	}
}

func (d *Daemon) pruneLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-d.prune:
			if err := d.DB.PruneCheck(ctx, id, d.Cfg.PingLogLimit); err != nil {
				d.Log.Warn("清理旧 ping 失败", "check", id, "err", err)
			}
		}
	}
}

func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
