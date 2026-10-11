package engine

import "time"

// never is the year-3000 sentinel: a schedule that will not fire again keeps
// the check up indefinitely.
var never = time.Date(3000, 1, 1, 0, 0, 0, 0, time.UTC)

// Scheduler computes the next occurrence of a schedule.
type Scheduler interface {
	// Next returns the first occurrence strictly after `after`, evaluated in the
	// named timezone and returned in UTC. Callers must only add durations to the
	// UTC value — adding to a local-zone time is off by an hour across DST.
	Next(schedule, tz string, after time.Time) (time.Time, bool, error)
}

// GraceStart returns when the grace period begins: the moment a ping was due.
// The second result is false when the check will not go down on its own.
func GraceStart(c *Check, sch Scheduler, includeStart bool) (time.Time, bool, error) {
	result := never

	if c.Status == StatusUp && c.LastPing != nil {
		switch c.Kind {
		case KindSimple:
			result = c.LastPing.Add(c.Timeout)
		case KindCron:
			next, ok, err := sch.Next(c.Schedule, c.TZ, c.LastPing.UTC())
			if err != nil {
				return time.Time{}, false, err
			}
			if ok {
				result = next.UTC()
			}
		}
	}

	if includeStart && c.LastStart != nil && c.Status != StatusDown && c.LastStart.Before(result) {
		result = *c.LastStart
	}

	if result.Equal(never) {
		return time.Time{}, false, nil
	}
	return result, true, nil
}

// GoingDownAfter returns when the check goes down if nothing pings first.
func GoingDownAfter(c *Check, sch Scheduler) (time.Time, bool, error) {
	gs, ok, err := GraceStart(c, sch, true)
	if err != nil || !ok {
		return time.Time{}, false, err
	}
	return gs.Add(c.Grace), true, nil
}

// StatusOf derives the status for display. withStarted is what makes the API v1
// and the detail page report "started"; the going-down scanner must not.
func StatusOf(c *Check, now time.Time, sch Scheduler, withStarted bool) (Display, error) {
	if c.LastStart != nil {
		if !now.Before(c.LastStart.Add(c.Grace)) {
			return DisplayDown, nil
		}
		if withStarted {
			return DisplayStarted, nil
		}
	}

	switch c.Status {
	case StatusNew, StatusPaused, StatusDown:
		return Display(c.Status), nil
	}

	gs, ok, err := GraceStart(c, sch, false)
	if err != nil {
		return "", err
	}
	if !ok {
		return DisplayUp, nil
	}
	if !now.Before(gs.Add(c.Grace)) {
		return DisplayDown, nil
	}
	if !now.Before(gs) {
		return DisplayGrace, nil
	}
	return DisplayUp, nil
}
