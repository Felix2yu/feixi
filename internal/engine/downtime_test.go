package engine

import (
	"testing"
	"time"
)

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestDowntimesReplay(t *testing.T) {
	loc := mustZone(t, "UTC")
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	boundaries := MonthBoundaries(now, loc, 3) // Oct, Sep, Aug (descending)

	// Aug 5 10:00 down → Aug 5 10:30 up → 30 minutes lost in August.
	// Sep 1 00:00 down → Sep 1 02:00 up → 2 hours lost in September.
	flips := []Event{
		{When: time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC), OldStatus: StatusDown},
		{When: time.Date(2026, 9, 1, 0, 5, 0, 0, time.UTC), OldStatus: StatusUp},
		{When: time.Date(2026, 8, 5, 10, 30, 0, 0, time.UTC), OldStatus: StatusDown},
		{When: time.Date(2026, 8, 5, 10, 0, 0, 0, time.UTC), OldStatus: StatusUp},
	}

	records := Downtimes(boundaries, created, StatusUp, flips, now)
	if len(records) != 3 {
		t.Fatalf("records = %d, want 3", len(records))
	}
	if records[0].Boundary.Month() != time.October || records[1].Boundary.Month() != time.September {
		t.Errorf("boundaries must be newest first: %v %v", records[0].Boundary, records[1].Boundary)
	}
	if records[0].Count != 0 || records[0].Duration != 0 {
		t.Errorf("october = %+v, want no downtime", records[0])
	}
	if records[1].Count != 1 || records[1].Duration != 115*time.Minute {
		t.Errorf("september = %+v, want 1 event of 2h", records[1])
	}
	if records[2].Count != 1 || records[2].Duration != 30*time.Minute {
		t.Errorf("august = %+v, want 1 event of 30m", records[2])
	}
	if records[0].NoData || records[1].NoData || records[2].NoData {
		t.Error("the check predates all three months")
	}

	// Uptime is the complement of downtime over the whole interval.
	up := records[1].Uptime(loc)
	secondsInSeptember := 30.0 * 24 * 3600
	want := 1 - (115.0*60)/secondsInSeptember
	if diff := up - want; diff > 1e-9 || diff < -1e-9 {
		t.Errorf("uptime(september) = %v, want %v", up, want)
	}
}

func TestDowntimesOpenSegmentStillDown(t *testing.T) {
	loc := mustZone(t, "UTC")
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	boundaries := MonthBoundaries(now, loc, 2)
	start := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)

	records := Downtimes(boundaries, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		StatusDown, []Event{{When: start, OldStatus: StatusUp}}, now)

	if records[0].Count != 1 || records[0].Duration != 3*time.Hour {
		t.Errorf("current month = %+v, want 3h of downtime so far", records[0])
	}
}

func TestDowntimesPauseIsNotDowntime(t *testing.T) {
	loc := mustZone(t, "UTC")
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	boundaries := MonthBoundaries(now, loc, 1)

	// down at 10:00, paused at 11:00. The walk starts from "paused", so the
	// 11:00-12:00 tail is not downtime; the genuine 10:00-11:00 hour still is.
	events := []Event{
		{When: time.Date(2026, 10, 10, 11, 0, 0, 0, time.UTC), OldStatus: StatusDown},
		{When: time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC), OldStatus: StatusUp},
	}
	records := Downtimes(boundaries, now.AddDate(0, -1, 0), StatusPaused, events, now)
	if records[0].Duration != time.Hour || records[0].Count != 1 {
		t.Errorf("paused tail = %+v, want only the 10:00-11:00 hour counted", records[0])
	}

	// Starting from "down" also counts the open segment from now back to the
	// 11:00 flip, which is how the original walk behaves.
	records = Downtimes(boundaries, now.AddDate(0, -1, 0), StatusDown, events, now)
	if records[0].Duration != 2*time.Hour || records[0].Count != 2 {
		t.Errorf("down again after pausing = %+v, want 2h over 2 segments", records[0])
	}
}

// TestDowntimesNoDataBeforeCreation pins the original's rule: an interval is
// flagged when the check was created after the start of the *following* one.
func TestDowntimesNoDataBeforeCreation(t *testing.T) {
	loc := mustZone(t, "UTC")
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	created := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	boundaries := MonthBoundaries(now, loc, 3)

	records := Downtimes(boundaries, created, StatusUp, nil, now)
	if !records[1].NoData || !records[2].NoData {
		t.Errorf("months before creation must be flagged: %+v", records)
	}
	if records[0].NoData {
		t.Error("the current month exists")
	}
}

func TestDowntimesAcrossDST(t *testing.T) {
	// Berlin turns back on the last Sunday of October 2026: local 02:00-03:00
	// happens twice. A downtime measured across that shift must stay exact.
	loc := mustZone(t, "Europe/Berlin")
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	boundaries := MonthBoundaries(now, loc, 2)

	// Down from 2026-10-24 12:00 UTC to 2026-10-25 12:00 UTC: the day of the
	// clock change, so local arithmetic would report 23h.
	events := []Event{
		{When: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC), OldStatus: StatusDown},
		{When: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC), OldStatus: StatusUp},
	}
	records := Downtimes(boundaries, created, StatusUp, events, now)
	if records[1].Duration != 24*time.Hour {
		t.Errorf("october duration = %v, want exactly 24h across the DST change", records[1].Duration)
	}
	if records[0].Duration != 0 {
		t.Errorf("november duration = %v, want none", records[0].Duration)
	}
}

func TestMonthBoundariesDescending(t *testing.T) {
	loc := mustZone(t, "UTC")
	now := time.Date(2026, 2, 15, 0, 0, 0, 0, time.UTC)
	boundaries := MonthBoundaries(now, loc, 3)
	want := []time.Month{time.February, time.January, time.December}
	for i, w := range want {
		if boundaries[i].Month() != w {
			t.Errorf("boundary %d = %v, want %v", i, boundaries[i], w)
		}
	}
	if boundaries[2].Year() != 2025 {
		t.Errorf("year rollover wrong: %v", boundaries[2])
	}
}
