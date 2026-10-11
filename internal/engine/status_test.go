package engine

import (
	"testing"
	"time"
)

var (
	t0   = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	day1 = t0.Add(24 * time.Hour)
)

// scripted replays the queued next-occurrence answers, repeating the last one:
// each test calls Status, GraceStart and GoingDownAfter against the same check.
// A zero time means "this schedule will never fire again".
type scripted struct {
	answers []time.Time
	i       int
	err     error
}

func (s *scripted) Next(schedule, tz string, after time.Time) (time.Time, bool, error) {
	if s.err != nil {
		return time.Time{}, false, s.err
	}
	if len(s.answers) == 0 {
		return time.Time{}, false, nil
	}
	a := s.answers[s.i]
	if s.i < len(s.answers)-1 {
		s.i++
	}
	if a.IsZero() {
		return time.Time{}, false, nil
	}
	return a, true, nil
}

func dur(s string) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		panic(err)
	}
	return d
}

func ptr(t time.Time) *time.Time { return &t }

func simple(status Status, lastPing *time.Time) *Check {
	return &Check{
		Code:     "c1",
		Kind:     KindSimple,
		Timeout:  dur("24h"),
		Grace:    dur("1h"),
		Status:   status,
		LastPing: lastPing,
	}
}

func TestGraceStartAndStatus(t *testing.T) {
	tests := []struct {
		name        string
		check       *Check
		sch         Scheduler
		now         time.Time
		withStarted bool
		wantDisplay Display
		wantGS      *time.Time
		wantDownAt  *time.Time
	}{
		{
			name:        "S1 simple up before deadline",
			check:       simple(StatusUp, ptr(t0)),
			now:         t0.Add(23 * time.Hour),
			wantDisplay: DisplayUp,
			wantGS:      ptr(day1),
			wantDownAt:  ptr(day1.Add(time.Hour)),
		},
		{
			name:        "S2 simple up exactly at deadline",
			check:       simple(StatusUp, ptr(t0)),
			now:         day1,
			wantDisplay: DisplayGrace,
			wantGS:      ptr(day1),
			wantDownAt:  ptr(day1.Add(time.Hour)),
		},
		{
			name:        "S3 simple up inside grace",
			check:       simple(StatusUp, ptr(t0)),
			now:         day1.Add(59*time.Minute + 59*time.Second),
			wantDisplay: DisplayGrace,
			wantGS:      ptr(day1),
			wantDownAt:  ptr(day1.Add(time.Hour)),
		},
		{
			name:        "S4 simple up grace elapsed",
			check:       simple(StatusUp, ptr(t0)),
			now:         day1.Add(time.Hour),
			wantDisplay: DisplayDown,
			wantGS:      ptr(day1),
			wantDownAt:  ptr(day1.Add(time.Hour)),
		},
		{
			name:        "S5 simple up last microsecond of grace",
			check:       simple(StatusUp, ptr(t0)),
			now:         day1.Add(time.Hour - time.Microsecond),
			wantDisplay: DisplayGrace,
			wantGS:      ptr(day1),
			wantDownAt:  ptr(day1.Add(time.Hour)),
		},
		{
			name:        "S6 new check never goes down on its own",
			check:       simple(StatusNew, nil),
			now:         t0,
			wantDisplay: DisplayNew,
		},
		{
			name:        "S7 paused stays paused",
			check:       simple(StatusPaused, ptr(t0.Add(-30*24*time.Hour))),
			now:         t0,
			wantDisplay: DisplayPaused,
		},
		{
			name:        "S8 running within grace reports started",
			check:       withStart(simple(StatusUp, ptr(t0)), t0.Add(22*time.Hour+30*time.Minute), nil),
			now:         t0.Add(23 * time.Hour),
			withStarted: true,
			wantDisplay: DisplayStarted,
			wantGS:      ptr(day1),
			wantDownAt:  ptr(t0.Add(23*time.Hour + 30*time.Minute)),
		},
		{
			name:        "S9 started is only for withStarted readers",
			check:       withStart(simple(StatusUp, ptr(t0)), t0.Add(22*time.Hour+30*time.Minute), nil),
			now:         t0.Add(23 * time.Hour),
			wantDisplay: DisplayUp,
			wantGS:      ptr(day1),
			wantDownAt:  ptr(t0.Add(23*time.Hour + 30*time.Minute)),
		},
		{
			name:        "S10 run outliving grace is down",
			check:       withStart(simple(StatusUp, ptr(t0)), t0.Add(-2*time.Hour), nil),
			now:         t0.Add(90 * time.Minute),
			withStarted: true,
			wantDisplay: DisplayDown,
			wantGS:      ptr(day1),
			wantDownAt:  ptr(t0.Add(-1 * time.Hour)),
		},
		{
			name:        "S17 down does not absorb last_start",
			check:       withStart(simple(StatusDown, ptr(t0)), t0.Add(30*time.Minute), nil),
			now:         t0,
			wantDisplay: DisplayDown,
		},
		{
			name:        "S19 paused keeps history but stays paused",
			check:       simple(StatusPaused, ptr(t0.Add(-10*24*time.Hour))),
			now:         t0,
			wantDisplay: DisplayPaused,
		},
		{
			name:        "S11 cron up inside grace",
			check:       cronCheck(StatusUp, ptr(time.Date(2026, 10, 1, 10, 3, 0, 0, time.UTC))),
			sch:         &scripted{answers: []time.Time{time.Date(2026, 10, 1, 10, 15, 0, 0, time.UTC)}},
			now:         time.Date(2026, 10, 1, 10, 20, 0, 0, time.UTC),
			wantDisplay: DisplayGrace,
			wantGS:      ptr(time.Date(2026, 10, 1, 10, 15, 0, 0, time.UTC)),
			wantDownAt:  ptr(time.Date(2026, 10, 1, 11, 15, 0, 0, time.UTC)),
		},
		{
			name:        "S11b cron up past grace is down",
			check:       cronCheck(StatusUp, ptr(time.Date(2026, 10, 1, 10, 3, 0, 0, time.UTC))),
			sch:         &scripted{answers: []time.Time{time.Date(2026, 10, 1, 10, 15, 0, 0, time.UTC)}},
			now:         time.Date(2026, 10, 1, 11, 16, 0, 0, time.UTC),
			wantDisplay: DisplayDown,
			wantGS:      ptr(time.Date(2026, 10, 1, 10, 15, 0, 0, time.UTC)),
			wantDownAt:  ptr(time.Date(2026, 10, 1, 11, 15, 0, 0, time.UTC)),
		},
		{
			name:        "S15 schedule that never fires stays up",
			check:       cronCheck(StatusUp, ptr(t0)),
			sch:         &scripted{answers: []time.Time{{}}},
			now:         t0.Add(10 * 365 * 24 * time.Hour),
			wantDisplay: DisplayUp,
		},
		{
			name:        "S16 started earlier than schedule wins for going-down",
			check:       withStart(cronCheck(StatusUp, ptr(t0)), t0.Add(5*time.Minute), nil),
			sch:         &scripted{answers: []time.Time{t0.Add(2 * time.Hour), t0.Add(2 * time.Hour)}},
			now:         t0,
			wantDisplay: DisplayUp,
			wantGS:      ptr(t0.Add(2 * time.Hour)),
			wantDownAt:  ptr(t0.Add(5*time.Minute + time.Hour)),
		},
		{
			name:        "up with no last_ping is tolerated",
			check:       simple(StatusUp, nil),
			now:         t0,
			wantDisplay: DisplayUp,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sch := tt.sch
			if sch == nil {
				sch = &scripted{}
			}

			got, err := StatusOf(tt.check, tt.now, sch, tt.withStarted)
			if err != nil {
				t.Fatalf("StatusOf: %v", err)
			}
			if got != tt.wantDisplay {
				t.Errorf("Status = %q, want %q", got, tt.wantDisplay)
			}

			gs, ok, err := GraceStart(tt.check, sch, false)
			if err != nil {
				t.Fatalf("GraceStart: %v", err)
			}
			if tt.wantGS == nil {
				if ok {
					t.Errorf("GraceStart = %v, want none", gs)
				}
			} else if !ok || !gs.Equal(*tt.wantGS) {
				t.Errorf("GraceStart = %v,%v want %v,true", gs, ok, *tt.wantGS)
			}

			down, ok, err := GoingDownAfter(tt.check, sch)
			if err != nil {
				t.Fatalf("GoingDownAfter: %v", err)
			}
			if tt.wantDownAt == nil {
				if ok {
					t.Errorf("GoingDownAfter = %v, want none", down)
				}
			} else if !ok || !down.Equal(*tt.wantDownAt) {
				t.Errorf("GoingDownAfter = %v,%v want %v,true", down, ok, *tt.wantDownAt)
			}
		})
	}
}

func withStart(c *Check, at time.Time, rid *string) *Check {
	c.LastStart = ptr(at)
	c.LastStartRid = rid
	return c
}

func cronCheck(status Status, lastPing *time.Time) *Check {
	c := simple(status, lastPing)
	c.Kind = KindCron
	c.Schedule = "*/15 * * * *"
	c.TZ = "UTC"
	return c
}
