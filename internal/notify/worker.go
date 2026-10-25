// Package notify turns status flips into apprise notifications.
package notify

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	apprise "github.com/unraid/apprise-go"

	"github.com/Felix2yu/feixi/internal/config"
	"github.com/Felix2yu/feixi/internal/engine"
	"github.com/Felix2yu/feixi/internal/store"
)

type Worker struct {
	DB       *store.DB
	Cfg      *config.Config
	Log      *slog.Logger
	pingRoot string
	siteRoot string
}

func NewWorker(db *store.DB, cfg *config.Config, log *slog.Logger) *Worker {
	return &Worker{DB: db, Cfg: cfg, Log: log, pingRoot: cfg.SiteRoot, siteRoot: cfg.SiteRoot}
}

// Process delivers one flip to every channel that should hear about it. The
// flapping gate may replace the per-flip alerts with a single flap notice.
func (w *Worker) Process(ctx context.Context, f *store.PendingFlip) error {
	if !engine.ShouldNotify(&f.Flip) {
		return nil
	}

	decision := store.FlapNormal
	if w.Cfg.FlapThreshold > 0 {
		// Anchor the window at the flip's own time: notification queue delays
		// (backoff, concurrency) must not change what counts as a burst.
		d, err := w.DB.ResolveFlipAlert(ctx, f.Check.ID, f.Flip.Created, w.Cfg.FlapWindow, w.Cfg.FlapThreshold)
		if err != nil {
			return err
		}
		decision = d
	}

	switch decision {
	case store.FlapSuppress:
		w.Log.Info("抖动期内抑制逐条告警", "check", f.Check.ID, "to", f.Flip.NewStatus)
		return nil
	case store.FlapEnter:
		w.Log.Info("检查项进入抖动状态", "check", f.Check.ID)
		return w.sendFlapEnter(ctx, f.Check)
	}

	channels, err := w.DB.ChannelsForCheck(ctx, f.Check.ID)
	if err != nil {
		return err
	}

	var firstErr error
	for _, ch := range channels {
		if ch.IsNoop(f.Flip.NewStatus) {
			continue
		}
		if err := w.deliver(ctx, ch, f); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (w *Worker) deliver(ctx context.Context, ch *store.Channel, f *store.PendingFlip) error {
	detailURL := w.siteRoot + "/checks/" + f.Check.Code
	message := body(f.Check, &f.Flip, f.DownDur, detailURL)
	return w.recordedSend(ctx, ch, f.Check, f.Flip.NewStatus, f.Flip.Reason, message, f.Flip.Created,
		title(f.Check, f.Flip.NewStatus))
}

// sendFlapEnter announces that per-flip alerts are being merged. Flap notices
// go to the channels that listen for outages.
func (w *Worker) sendFlapEnter(ctx context.Context, c *engine.Check) error {
	channels, err := w.DB.ChannelsForCheck(ctx, c.ID)
	if err != nil {
		return err
	}
	detailURL := w.siteRoot + "/checks/" + c.Code
	message := flapEnterBody(c, w.Cfg.FlapWindow, w.Cfg.FlapThreshold, detailURL)

	var firstErr error
	for _, ch := range channels {
		if ch.IsNoop(engine.StatusDown) {
			continue
		}
		if err := w.recordedSend(ctx, ch, c, engine.StatusFlap, engine.ReasonNone, message, time.Now().UTC(),
			flapTitle(c)); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// SettleFlaps closes one or more flap episodes. A check that calmed down
// while healthy gets a "stable" notice; one that is still down gets the
// ordinary down alert it never received while alerts were merged.
func (w *Worker) SettleFlaps(ctx context.Context, settles []store.FlapSettle) error {
	var firstErr error
	for _, s := range settles {
		c := s.Check
		channels, err := w.DB.ChannelsForCheck(ctx, c.ID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		detailURL := w.siteRoot + "/checks/" + c.Code

		for _, ch := range channels {
			switch c.Status {
			case engine.StatusUp:
				if ch.IsNoop(engine.StatusUp) {
					continue
				}
				message := stableBody(c, w.Cfg.FlapWindow, detailURL)
				err = w.recordedSend(ctx, ch, c, engine.StatusStable, engine.ReasonNone, message, time.Now().UTC(),
					stableTitle(c))
			case engine.StatusDown:
				if ch.IsNoop(engine.StatusDown) {
					continue
				}
				_, reason, _, _ := w.DB.LastDownFlip(ctx, c.ID)
				message := body(c, &engine.Flip{NewStatus: engine.StatusDown, Reason: reason}, nil, detailURL)
				err = w.recordedSend(ctx, ch, c, engine.StatusDown, reason, message, time.Now().UTC(),
					title(c, engine.StatusDown))
			default:
				// new/paused: settle silently.
				continue
			}
			if err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// Nag re-sends a down alert for a check that has not recovered. The delivery
// lands as a normal notification row, so both the history and the nag dedupe
// see it.
func (w *Worker) Nag(ctx context.Context, cand store.NagCandidate) error {
	ch := cand.Channel
	if ch.Disabled || ch.IsNoop(engine.StatusDown) {
		return nil
	}
	downFor := time.Duration(0)
	if !cand.DownSince.IsZero() {
		downFor = time.Now().UTC().Sub(cand.DownSince)
	}
	detailURL := w.siteRoot + "/checks/" + cand.Check.Code
	message := nagBody(cand.Check, cand.Reason, downFor, detailURL)
	return w.recordedSend(ctx, ch, cand.Check, engine.StatusDown, cand.Reason, message, time.Now().UTC(),
		title(cand.Check, engine.StatusDown))
}

// RetryNotification re-delivers one past alert to its original channel and
// records a fresh delivery row, so retries show up in the notification history.
func (w *Worker) RetryNotification(ctx context.Context, n *store.Notification) error {
	if n.CheckID == nil || n.ChannelID == nil {
		return errors.New("原始记录缺少检查项或渠道，无法重试")
	}
	ch, err := w.DB.ChannelByID(ctx, *n.ChannelID)
	if err != nil {
		return errors.New("渠道已删除，无法重试")
	}
	c, err := w.DB.CheckByID(ctx, *n.CheckID)
	if err != nil {
		return errors.New("检查项已删除，无法重试")
	}
	if ch.Disabled {
		return errors.New("渠道已停用，请先在渠道页启用")
	}

	detailURL := w.siteRoot + "/checks/" + c.Code
	switch n.CheckStatus {
	case engine.StatusFlap:
		if ch.IsNoop(engine.StatusDown) {
			return errors.New("该渠道当前不接收宕机类通知")
		}
		message := flapEnterBody(c, w.Cfg.FlapWindow, w.Cfg.FlapThreshold, detailURL)
		return w.recordedSend(ctx, ch, c, engine.StatusFlap, engine.ReasonNone, message, n.Created, flapTitle(c))
	case engine.StatusStable:
		if ch.IsNoop(engine.StatusUp) {
			return errors.New("该渠道当前不接收恢复类通知")
		}
		message := stableBody(c, w.Cfg.FlapWindow, detailURL)
		return w.recordedSend(ctx, ch, c, engine.StatusStable, engine.ReasonNone, message, n.Created, stableTitle(c))
	}
	if ch.IsNoop(n.CheckStatus) {
		return errors.New("该渠道当前不接收这类通知")
	}

	reason := engine.ReasonNone
	if n.CheckStatus == engine.StatusDown {
		if _, r, ok, lerr := w.DB.LastDownFlip(ctx, c.ID); lerr == nil && ok {
			reason = r
		}
	}
	message := body(c, &engine.Flip{Created: n.Created, NewStatus: n.CheckStatus, Reason: reason}, nil, detailURL)
	return w.recordedSend(ctx, ch, c, n.CheckStatus, reason, message, n.Created, title(c, n.CheckStatus))
}

// recordedSend is the shared delivery path: substitute variables, record the
// notification row, retry with backoff, and update the channel health fields.
// varsAt is the timestamp relative to which $BODY/$EXITSTATUS are resolved.
func (w *Worker) recordedSend(ctx context.Context, ch *store.Channel, c *engine.Check,
	status engine.Status, reason engine.Reason, message string, varsAt time.Time, alertTitle string) error {
	raw := strings.Join(ch.URLs, "\n")
	vars, err := w.buildVars(ctx, c,
		&engine.Flip{Created: varsAt, OldStatus: status, NewStatus: status, Reason: reason}, raw)
	if err != nil {
		return err
	}
	urls := make([]string, 0, len(ch.URLs))
	for _, u := range ch.URLs {
		urls = append(urls, vars.apply(u))
	}

	notifyType := apprise.NotifyFailure
	if status == engine.StatusUp || status == engine.StatusStable {
		notifyType = apprise.NotifySuccess
	}

	notif, err := w.DB.StartNotification(ctx, &c.ID, ch.ID, status)
	if err != nil {
		return err
	}

	var lastErr error
	for attempt := 0; attempt <= w.Cfg.NotifyRetries; attempt++ {
		if attempt > 0 {
			// Back off 5s, then 15s, without outliving the request context.
			delay := time.Duration(5+10*(attempt-1)) * time.Second
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}

		start := time.Now()
		err := w.send(urls, alertTitle, message, notifyType)
		elapsed := time.Since(start)

		if err == nil {
			if ferr := w.DB.FinishNotification(ctx, notif.ID, ""); ferr != nil {
				return ferr
			}
			// 审计记录写失败不影响「通知已经送达」这个事实，只留日志
			if aerr := w.DB.RecordAttempt(ctx, ch.ID, time.Now().UTC(), elapsed, "", false); aerr != nil {
				w.Log.Warn("记录通知尝试失败", "channel", ch.Name, "err", aerr)
			}
			return nil
		}

		lastErr = err
		text := errText(err)
		permanent := isPermanent(err) || isClientError(err)
		if ferr := w.DB.FinishNotification(ctx, notif.ID, text); ferr != nil {
			w.Log.Warn("更新通知结果失败", "channel", ch.Name, "err", ferr)
		}
		if aerr := w.DB.RecordAttempt(ctx, ch.ID, time.Now().UTC(), elapsed, text, permanent); aerr != nil {
			w.Log.Warn("记录通知尝试失败", "channel", ch.Name, "err", aerr)
		}
		if permanent {
			w.Log.Warn("渠道被禁用", "channel", ch.Name, "err", text)
			return err
		}
		w.Log.Warn("通知发送失败", "channel", ch.Name, "attempt", attempt+1, "err", text)
	}
	return lastErr
}

// TestMessage is the payload for the "send test" button: it reports the check's
// current status instead of a transition.
func (w *Worker) TestMessage(ch *store.Channel, c *engine.Check, status engine.Status) error {
	raw := strings.Join(ch.URLs, "\n")
	vars, err := w.buildVars(context.Background(), c, &engine.Flip{Created: time.Now().UTC(), NewStatus: status}, raw)
	if err != nil {
		return err
	}
	urls := make([]string, 0, len(ch.URLs))
	for _, u := range ch.URLs {
		urls = append(urls, vars.apply(u))
	}

	notifyType := apprise.NotifyFailure
	if status == engine.StatusUp {
		notifyType = apprise.NotifySuccess
	}
	message := "这是一条测试消息。"
	return w.send(urls, title(c, status), message, notifyType)
}

// errText flattens apprise's joined errors into one line for the notification
// log. TargetError already prefixes its URL, so a partial failure stays readable.
func errText(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ReplaceAll(err.Error(), "\n", "; ")
	if len(msg) > 500 {
		msg = msg[:500]
	}
	return msg
}

// isClientError maps apprise's "unexpected status: <reason>" onto the small set
// of answers that mean the target is wrong rather than busy. Anything else —
// DNS, timeouts, 5xx — stays retryable, because disabling a channel by mistake
// silences alerts, which is far worse than a noisy retry.
func isClientError(err error) bool {
	text := errText(err)
	i := strings.Index(text, "unexpected status: ")
	if i < 0 {
		return false
	}
	reason := text[i+len("unexpected status: "):]
	if j := strings.IndexAny(reason, ";,"); j >= 0 {
		reason = reason[:j]
	}
	switch strings.TrimSpace(reason) {
	case http.StatusText(http.StatusUnauthorized),
		http.StatusText(http.StatusForbidden),
		http.StatusText(http.StatusNotFound),
		http.StatusText(http.StatusGone):
		return true
	}
	return false
}
