package engine

import "time"

type Flip struct {
	CheckID   int64
	Created   time.Time
	Processed *time.Time
	OldStatus Status
	NewStatus Status
	Reason    Reason
}

// EffectiveAction applies the "sticky pause" rule: while a check with
// manual_resume is paused, incoming signals are recorded but change nothing.
func EffectiveAction(c *Check, action Action) Action {
	if c.Status == StatusPaused && c.ManualResume && action != ActionIgn {
		return ActionIgn
	}
	return action
}

// ApplyPing advances the check for one incoming ping and returns the Flip when
// its status changed.
func ApplyPing(c *Check, action Action, now time.Time, rid *string, sch Scheduler) (*Flip, error) {
	action = EffectiveAction(c, action)

	var flip *Flip

	switch action {
	case ActionStart:
		c.LastStart = &now
		c.LastStartRid = rid

	case ActionSuccess, ActionFail:
		c.LastPing = &now
		c.LastDuration = nil
		if c.LastStart != nil {
			switch {
			case sameRid(c.LastStartRid, rid):
				d := now.Sub(*c.LastStart)
				c.LastDuration = &d
				c.LastStart = nil
				c.LastStartRid = nil
			case action == ActionFail || rid == nil:
				c.LastStart = nil
				c.LastStartRid = nil
			}
		}

		newStatus := StatusUp
		if action == ActionFail {
			newStatus = StatusDown
		}
		if c.Status != newStatus {
			reason := ReasonNone
			if action == ActionFail {
				reason = ReasonFail
			}
			flip = &Flip{CheckID: c.ID, Created: now, OldStatus: c.Status, NewStatus: newStatus, Reason: reason}
			c.Status = newStatus
		}

	case ActionLog, ActionIgn:
	}

	aad, ok, err := GoingDownAfter(c, sch)
	if err != nil {
		return nil, err
	}
	if ok {
		c.AlertAfter = &aad
	} else {
		c.AlertAfter = nil
	}
	return flip, nil
}

// Pause freezes monitoring. History (last_ping, flips) is kept, so the detail
// page still shows the previous run. Returns nil if already paused.
func Pause(c *Check, now time.Time) *Flip {
	if c.Status == StatusPaused {
		return nil
	}
	flip := &Flip{
		CheckID:   c.ID,
		Created:   now,
		Processed: &now,
		OldStatus: c.Status,
		NewStatus: StatusPaused,
	}
	c.Status = StatusPaused
	c.LastStart = nil
	c.LastStartRid = nil
	c.AlertAfter = nil
	return flip
}

// Resume sends the check back to "new": it needs a fresh ping before it can be
// considered up again. Returns nil if the check is not paused.
func Resume(c *Check, now time.Time) *Flip {
	if c.Status != StatusPaused {
		return nil
	}
	flip := &Flip{
		CheckID:   c.ID,
		Created:   now,
		Processed: &now,
		OldStatus: c.Status,
		NewStatus: StatusNew,
	}
	c.Status = StatusNew
	c.LastStart = nil
	c.LastStartRid = nil
	c.LastPing = nil
	c.LastDuration = nil
	c.AlertAfter = nil
	return flip
}

// MarkDown takes a check down on schedule expiry. The flip is left unprocessed
// so the notifier picks it up.
func MarkDown(c *Check, at time.Time) *Flip {
	if c.Status == StatusDown {
		return nil
	}
	flip := &Flip{
		CheckID:   c.ID,
		Created:   at,
		OldStatus: c.Status,
		NewStatus: StatusDown,
		Reason:    ReasonTimeout,
	}
	c.Status = StatusDown
	c.AlertAfter = nil
	return flip
}

// ShouldNotify reports whether a flip triggers alerts. Recoveries from new or
// paused are silent: the check was never actually failing.
func ShouldNotify(f *Flip) bool {
	if f.NewStatus == StatusUp && (f.OldStatus == StatusNew || f.OldStatus == StatusPaused) {
		return false
	}
	return f.NewStatus == StatusUp || f.NewStatus == StatusDown
}

func sameRid(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
