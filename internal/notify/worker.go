// Package notify turns status flips into apprise notifications.
package notify

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	apprise "github.com/unraid/apprise-go"

	"github.com/yufei/feixi/internal/config"
	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/store"
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

// Process delivers one flip to every channel that should hear about it.
func (w *Worker) Process(ctx context.Context, f *store.PendingFlip) error {
	if !engine.ShouldNotify(&f.Flip) {
		return nil
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
	raw := strings.Join(ch.URLs, "\n")
	vars, err := w.buildVars(ctx, f.Check, &f.Flip, raw)
	if err != nil {
		return err
	}
	urls := make([]string, 0, len(ch.URLs))
	for _, u := range ch.URLs {
		urls = append(urls, vars.apply(u))
	}

	notifyType := apprise.NotifyFailure
	if f.Flip.NewStatus == engine.StatusUp {
		notifyType = apprise.NotifySuccess
	}
	detailURL := w.siteRoot + "/checks/" + f.Check.Code
	message := body(f.Check, &f.Flip, f.DownDur, detailURL)

	notif, err := w.DB.StartNotification(ctx, &f.Check.ID, ch.ID, f.Flip.NewStatus)
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
		err := w.send(urls, title(f.Check, f.Flip.NewStatus), message, notifyType)
		elapsed := time.Since(start)

		if err == nil {
			if ferr := w.DB.FinishNotification(ctx, notif.ID, ""); ferr != nil {
				return ferr
			}
			w.DB.RecordAttempt(ctx, ch.ID, time.Now().UTC(), elapsed, "", false)
			return nil
		}

		lastErr = err
		text := errText(err)
		permanent := isPermanent(err) || isClientError(err)
		w.DB.FinishNotification(ctx, notif.ID, text)
		w.DB.RecordAttempt(ctx, ch.ID, time.Now().UTC(), elapsed, text, permanent)
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
