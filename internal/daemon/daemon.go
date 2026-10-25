// Package daemon runs the two background loops: the going-down scanner and the
// notification queue.
package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/Felix2yu/feixi/internal/config"
	"github.com/Felix2yu/feixi/internal/store"
)

// Notifier delivers one flip to its channels.
type Notifier interface {
	Process(ctx context.Context, flip *store.PendingFlip) error
	// Nag re-sends a down alert for a check that has stayed down.
	Nag(ctx context.Context, cand store.NagCandidate) error
	// SettleFlaps closes a flapping episode once the check has stayed quiet.
	SettleFlaps(ctx context.Context, settles []store.FlapSettle) error
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
func (noopNotifier) Nag(context.Context, store.NagCandidate) error     { return nil }
func (noopNotifier) SettleFlaps(context.Context, []store.FlapSettle) error {
	return nil
}

// QueuePrune asks for trimming without making the ping request wait for it.
func (d *Daemon) QueuePrune(checkID int64) {
	select {
	case d.prune <- checkID:
	default:
		// 队列满时丢掉这条提示不要紧，全量清扫迟早会补上。
	}
}

func (d *Daemon) Run(ctx context.Context) {
	go d.scanGoingDown(ctx)
	go d.drainFlips(ctx)
	go d.nagLoop(ctx)
	go d.flapLoop(ctx)
	go d.pruneLoop(ctx)
	go d.pruneSweepLoop(ctx)
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

// nagLoop periodically re-alerts channels that opted into reminders while a
// check remains down. Sweeps run sequentially: the delivery record itself is
// the dedupe key, so overlapping ticks would only add duplicate sends.
func (d *Daemon) nagLoop(ctx context.Context) {
	if d.Cfg.NagPeriod <= 0 {
		return
	}
	ticker := time.NewTicker(d.Cfg.NagScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.sweepNags(ctx)
		}
	}
}

func (d *Daemon) sweepNags(ctx context.Context) {
	candidates, err := d.DB.NagDue(ctx, d.Cfg.NagPeriod, time.Now().UTC())
	if err != nil {
		d.Log.Warn("查找催促任务失败", "err", err)
		return
	}
	for _, cand := range candidates {
		if ctx.Err() != nil {
			return
		}
		if err := d.Notifier.Nag(ctx, cand); err != nil {
			d.Log.Warn("催促通知发送失败", "check", cand.Check.ID,
				"channel", cand.Channel.Name, "err", err)
		} else {
			d.Log.Info("已发送催促通知", "check", cand.Check.ID, "channel", cand.Channel.Name)
		}
	}
}

// flapLoop settles flapping episodes once their check has stayed in one
// state for a full window. Settling is batched so one notify call may close
// several checks at once.
func (d *Daemon) flapLoop(ctx context.Context) {
	if d.Cfg.FlapThreshold <= 0 {
		return
	}
	ticker := time.NewTicker(d.Cfg.FlapScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.sweepFlaps(ctx)
		}
	}
}

func (d *Daemon) sweepFlaps(ctx context.Context) {
	settles, err := d.DB.FlappingDue(ctx, time.Now().UTC(), d.Cfg.FlapWindow)
	if err != nil {
		d.Log.Warn("查找抖动结算失败", "err", err)
		return
	}
	if len(settles) == 0 {
		return
	}
	if err := d.Notifier.SettleFlaps(ctx, settles); err != nil {
		d.Log.Warn("抖动结算通知发送失败", "err", err)
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

// pruneSweepLoop trims every check on a schedule. The ping-triggered hints in
// pruneLoop only reach checks that keep reporting in, so a job that stopped
// pinging would otherwise keep its old pings, flips and notifications forever.
// One sweep also runs at startup, when the hints queued before a restart are
// long gone.
func (d *Daemon) pruneSweepLoop(ctx context.Context) {
	if d.Cfg.PruneInterval <= 0 {
		return
	}
	d.sweepAll(ctx)

	ticker := time.NewTicker(d.Cfg.PruneInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.sweepAll(ctx)
		}
	}
}

func (d *Daemon) sweepAll(ctx context.Context) {
	n, err := d.DB.PruneAll(ctx, d.Cfg.PingLogLimit)
	if err != nil {
		d.Log.Warn("全量清理失败", "swept", n, "err", err)
		return
	}
	d.Log.Info("全量清理完成", "checks", n)
}

func sleep(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
