package cronex

import (
	"testing"
	"time"

	"github.com/Felix2yu/feixi/internal/engine"
)

func mustPtr(t time.Time) *time.Time { return &t }

func TestNext(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
		tz       string
		after    time.Time
		want     time.Time
		wantOK   bool
	}{
		{
			name:     "every quarter hour",
			schedule: "*/15 * * * *",
			tz:       "UTC",
			after:    time.Date(2026, 10, 1, 10, 3, 0, 0, time.UTC),
			want:     time.Date(2026, 10, 1, 10, 15, 0, 0, time.UTC),
			wantOK:   true,
		},
		{
			name:     "fixed wall clock in utc offset zone",
			schedule: "0 9 * * *",
			tz:       "Asia/Shanghai",
			after:    time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC),
			want:     time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC),
			wantOK:   true,
		},
		{
			// S12: on 2026-03-29 Berlin springs forward, 02:30 does not exist,
			// so the occurrence rolls to the next day — and the result must come
			// back in UTC so the grace addition stays exact.
			name:     "S12 spring forward skips the nonexistent wall clock",
			schedule: "30 2 * * *",
			tz:       "Europe/Berlin",
			after:    time.Date(2026, 3, 28, 1, 30, 0, 0, time.UTC),
			want:     time.Date(2026, 3, 30, 0, 30, 0, 0, time.UTC),
			wantOK:   true,
		},
		{
			name:     "control day before the transition",
			schedule: "30 2 * * *",
			tz:       "Europe/Berlin",
			after:    time.Date(2026, 3, 27, 1, 0, 0, 0, time.UTC),
			want:     time.Date(2026, 3, 27, 1, 30, 0, 0, time.UTC),
			wantOK:   true,
		},
		{
			// S13: on 2026-10-25 Berlin falls back, so 02:00 happens twice. The
			// first pass wins, keeping alert_after monotonic.
			name:     "S13 fall back picks the first ambiguous occurrence",
			schedule: "0 2 * * *",
			tz:       "Europe/Berlin",
			after:    time.Date(2026, 10, 24, 22, 30, 0, 0, time.UTC),
			want:     time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
			wantOK:   true,
		},
		{
			// A second-pass local time is not re-matched; the next hit is tomorrow.
			name:     "S13b repeated local hour is not matched twice",
			schedule: "30 1 * * *",
			tz:       "Europe/Berlin",
			after:    time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
			want:     time.Date(2026, 10, 26, 0, 30, 0, 0, time.UTC),
			wantOK:   true,
		},
		{
			name:     "S24 leap day",
			schedule: "0 0 29 2 *",
			tz:       "UTC",
			after:    time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC),
			want:     time.Date(2028, 2, 29, 0, 0, 0, 0, time.UTC),
			wantOK:   true,
		},
		{
			name:     "descriptor",
			schedule: "@daily",
			tz:       "UTC",
			after:    time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC),
			want:     time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC),
			wantOK:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok, err := Scheduler{}.Next(tt.schedule, tt.tz, tt.after)
			if err != nil {
				t.Fatalf("Next(%q, %q): %v", tt.schedule, tt.tz, err)
			}
			if ok != tt.wantOK {
				t.Fatalf("Next ok = %v, want %v", ok, tt.wantOK)
			}
			if !got.Equal(tt.want) {
				t.Errorf("Next = %s, want %s", got.Format(time.RFC3339), tt.want.Format(time.RFC3339))
			}
		})
	}
}

// S14: the grace period is added on the UTC axis, so a check whose schedule
// rolls across the spring transition goes down exactly grace after the hit.
func TestGoingDownAfterAcrossDST(t *testing.T) {
	lastPing := time.Date(2026, 3, 28, 1, 30, 0, 0, time.UTC)
	c := &engine.Check{
		Kind:     engine.KindCron,
		Schedule: "30 2 * * *",
		TZ:       "Europe/Berlin",
		Grace:    time.Hour,
		Status:   engine.StatusUp,
		LastPing: mustPtr(lastPing),
	}

	down, ok, err := engine.GoingDownAfter(c, Scheduler{})
	if err != nil {
		t.Fatalf("GoingDownAfter: %v", err)
	}
	if !ok {
		t.Fatal("GoingDownAfter: expected a deadline")
	}
	want := time.Date(2026, 3, 30, 1, 30, 0, 0, time.UTC)
	if !down.Equal(want) {
		t.Errorf("GoingDownAfter = %s, want %s", down.Format(time.RFC3339), want.Format(time.RFC3339))
	}

	// Just before the deadline the check still reads grace, never a full hour early.
	st, err := engine.StatusOf(c, want.Add(-time.Microsecond), Scheduler{}, false)
	if err != nil {
		t.Fatalf("StatusOf: %v", err)
	}
	if st != engine.DisplayGrace {
		t.Errorf("status before deadline = %q, want grace", st)
	}
}

func TestUnknownZone(t *testing.T) {
	_, _, err := Scheduler{}.Next("0 9 * * *", "Mars/Rover", time.Now())
	if err == nil {
		t.Fatal("expected an error for an unknown timezone")
	}
}

func TestValidate(t *testing.T) {
	ok := []string{
		"*/15 * * * *",
		"15 5 * * *",
		"0 0 * * 1-5",
		"@daily",
		"@weekly",
		"0 2 * * MON,FRI",
		"30 2 29 2 *",
	}
	for _, s := range ok {
		if err := Validate(s); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", s, err)
		}
	}

	bad := []string{
		"",
		"  ",
		"0 15 5 * * *",   // 6 fields
		"0 0 15 5 * * *", // 7 fields
		"@every 5m",
		"0 2 * * ?",
		"0 0 L * *",
		"0 0 15W * *",
		"0 0 * * 5#3",
		"99 * * * *",
		"* * * * 8",
	}
	for _, s := range bad {
		if err := Validate(s); err == nil {
			t.Errorf("Validate(%q) = nil, want an error", s)
		}
	}
}
