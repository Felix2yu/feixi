package notify

import (
	"errors"
	"time"

	apprise "github.com/unraid/apprise-go"
)

// permanentError marks a failure that retrying cannot fix, so the channel gets
// disabled instead of silently failing forever.
type permanentError struct{ error }

func isPermanent(err error) bool {
	var p permanentError
	return errors.As(err, &p)
}

// errDeadline is the watchdog firing: apprise-go exposes no context and builds
// its clients without an overall timeout, so a stalled endpoint would otherwise
// hold a worker forever.
var errDeadline = errors.New("发送超时")

// send hands the message to apprise-go in a goroutine and gives up after the
// configured deadline. The leaked goroutine cannot be cancelled — it is bounded
// by the worker's semaphore, and the queue keeps moving.
func (w *Worker) send(urls []string, title, message string, t apprise.NotifyType) error {
	done := make(chan error, 1)
	go func() {
		client := apprise.New()
		if err := client.AddAll(urls...); err != nil {
			done <- permanentError{err}
			return
		}
		done <- client.Send(message, apprise.WithTitle(title), apprise.WithNotifyType(t))
	}()

	timer := time.NewTimer(w.Cfg.NotifyDeadline)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return errDeadline
	}
}
