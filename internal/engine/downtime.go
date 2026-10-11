package engine

import (
	"sort"
	"time"
)

// DowntimeRecord is one month (or week) of outage statistics.
type DowntimeRecord struct {
	Boundary time.Time
	Duration time.Duration
	Count    int
	// NoData marks an interval that began before the check existed.
	NoData bool
}

// Uptime is the share of the interval the check was not down.
func (r DowntimeRecord) Uptime(loc *time.Location) float64 {
	span := intervalSeconds(r.Boundary, loc)
	if span <= 0 {
		return 1
	}
	up := float64(span) - r.Duration.Seconds()
	if up < 0 {
		up = 0
	}
	return up / float64(span)
}

// MonthBoundaries returns the first instant of the current month and the
// preceding `months-1` months, newest first, in the given location.
func MonthBoundaries(now time.Time, loc *time.Location, months int) []time.Time {
	y, m, _ := now.In(loc).Date()
	out := make([]time.Time, 0, months)
	for i := 0; i < months; i++ {
		out = append(out, time.Date(y, m, 1, 0, 0, 0, 0, loc))
		m--
		if m == 0 {
			m = 12
			y--
		}
	}
	return out
}

func intervalSeconds(boundary time.Time, loc *time.Location) int {
	y, m, d := boundary.In(loc).Date()
	next := time.Date(y, time.Month(m)+1, d, 0, 0, 0, 0, loc)
	return int(next.Sub(boundary.In(loc)).Seconds())
}

// Event is a flip or an interval boundary, replayed newest first.
type Event struct {
	When      time.Time
	OldStatus Status
	// Boundary marks a synthetic month-start event, which carries no status.
	Boundary bool
}

// Downtimes walks the transition history backwards and accumulates the time the
// check spent in "down" inside each interval. Copied from
// Check.downtimes_by_boundary: the counters start from the current status and
// the current moment, then each earlier event closes the segment it precedes.
func Downtimes(boundaries []time.Time, created time.Time, current Status, flips []Event, now time.Time) []DowntimeRecord {
	records := make([]DowntimeRecord, 0, len(boundaries))
	var prev time.Time
	for _, b := range boundaries {
		// The check did not exist yet if it was created after the *previous*
		// (i.e. later) boundary.
		records = append(records, DowntimeRecord{Boundary: b, NoData: !prev.IsZero() && created.After(prev)})
		prev = b
	}

	events := make([]Event, 0, len(boundaries)+len(flips))
	for _, b := range boundaries {
		events = append(events, Event{When: b, Boundary: true})
	}
	events = append(events, flips...)

	// Sort descending by time; equal timestamps keep the boundary first, which
	// matches Python's tuple ordering where the "---" marker sorts before names.
	sortEventsDesc(events)

	dt, status := now, current
	for _, e := range events {
		if status == StatusDown {
			// Subtract in UTC: local-zone arithmetic drifts across DST.
			delta := dt.UTC().Sub(e.When.UTC())
			if delta < 0 {
				delta = 0
			}
			addTo(records, e.When, delta)
		}
		dt = e.When
		if !e.Boundary {
			status = e.OldStatus
		}
	}
	return records
}

func addTo(records []DowntimeRecord, when time.Time, delta time.Duration) {
	for i := range records {
		if !when.Before(records[i].Boundary) {
			records[i].Duration += delta
			records[i].Count++
			return
		}
	}
}

func sortEventsDesc(events []Event) {
	sort.SliceStable(events, func(i, j int) bool {
		if !events[i].When.Equal(events[j].When) {
			return events[i].When.After(events[j].When)
		}
		// A flip at exactly a boundary is replayed first, so the segment that
		// ends there keeps the status of the interval it belongs to.
		return !events[i].Boundary && events[j].Boundary
	})
}
