package engine

import (
	"testing"
	"time"
)

func strptr(s string) *string { return &s }

func newCheck() *Check {
	return &Check{
		ID:      1,
		Code:    "11111111-1111-1111-1111-111111111111",
		Kind:    KindSimple,
		Timeout: dur("24h"),
		Grace:   dur("1h"),
		Status:  StatusNew,
		Created: t0,
	}
}

func TestApplyPing(t *testing.T) {
	sch := &scripted{}

	t.Run("first success moves new to up", func(t *testing.T) {
		c := newCheck()
		flip, err := ApplyPing(c, ActionSuccess, t0, nil, sch)
		if err != nil {
			t.Fatal(err)
		}
		if c.Status != StatusUp {
			t.Errorf("status = %q, want up", c.Status)
		}
		if flip == nil || flip.OldStatus != StatusNew || flip.NewStatus != StatusUp {
			t.Errorf("flip = %+v", flip)
		}
		if c.AlertAfter == nil || !c.AlertAfter.Equal(t0.Add(25*time.Hour)) {
			t.Errorf("alert_after = %v, want %s", c.AlertAfter, t0.Add(25*time.Hour))
		}
		if ShouldNotify(flip) {
			t.Error("new->up must not alert")
		}
	})

	t.Run("second success in a row creates no flip", func(t *testing.T) {
		c := newCheck()
		if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
			t.Fatal(err)
		}
		flip, err := ApplyPing(c, ActionSuccess, t0.Add(time.Hour), nil, sch)
		if err != nil {
			t.Fatal(err)
		}
		if flip != nil {
			t.Errorf("flip = %+v, want none", flip)
		}
		if !c.AlertAfter.Equal(t0.Add(26 * time.Hour)) {
			t.Errorf("alert_after = %v, want it pushed from the newest ping", *c.AlertAfter)
		}
	})

	t.Run("fail signal goes down with reason fail", func(t *testing.T) {
		c := newCheck()
		if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
			t.Fatal(err)
		}
		flip, err := ApplyPing(c, ActionFail, t0.Add(time.Hour), nil, sch)
		if err != nil {
			t.Fatal(err)
		}
		if c.Status != StatusDown || c.AlertAfter != nil {
			t.Errorf("status = %q alert_after = %v", c.Status, c.AlertAfter)
		}
		if flip.Reason != ReasonFail || flip.NewStatus != StatusDown {
			t.Errorf("flip = %+v", flip)
		}
		if !ShouldNotify(flip) {
			t.Error("down must alert")
		}
	})

	t.Run("recovery from down alerts", func(t *testing.T) {
		c := newCheck()
		if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyPing(c, ActionFail, t0.Add(time.Hour), nil, sch); err != nil {
			t.Fatal(err)
		}
		flip, err := ApplyPing(c, ActionSuccess, t0.Add(2*time.Hour), nil, sch)
		if err != nil {
			t.Fatal(err)
		}
		if flip.OldStatus != StatusDown || flip.NewStatus != StatusUp {
			t.Errorf("flip = %+v", flip)
		}
		if !ShouldNotify(flip) {
			t.Error("down->up must alert")
		}
	})

	t.Run("start leaves last_ping alone", func(t *testing.T) {
		c := newCheck()
		if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
			t.Fatal(err)
		}
		start := t0.Add(time.Hour)
		flip, err := ApplyPing(c, ActionStart, start, nil, sch)
		if err != nil {
			t.Fatal(err)
		}
		if flip != nil {
			t.Errorf("flip = %+v, want none", flip)
		}
		if !c.LastPing.Equal(t0) {
			t.Errorf("last_ping moved to %v", *c.LastPing)
		}
		if c.LastStart == nil || !c.LastStart.Equal(start) {
			t.Errorf("last_start = %v", c.LastStart)
		}
		// S16 again: going down is now driven by last_start, not the timeout.
		if !c.AlertAfter.Equal(start.Add(time.Hour)) {
			t.Errorf("alert_after = %v, want %s", *c.AlertAfter, start.Add(time.Hour))
		}
	})

	ridCases := []struct {
		name         string
		startRid     *string
		endRid       *string
		action       Action
		wantDuration bool
		wantRunOpen  bool
	}{
		{"matching rid computes duration", strptr("a"), strptr("a"), ActionSuccess, true, false},
		{"both nil rid still pairs", nil, nil, ActionSuccess, true, false},
		{"mismatched rid keeps the run open", strptr("a"), strptr("b"), ActionSuccess, false, true},
		{"fail closes the run regardless of rid", strptr("a"), strptr("b"), ActionFail, false, false},
		{"success without rid closes it", strptr("a"), nil, ActionSuccess, false, false},
	}
	for _, tt := range ridCases {
		t.Run(tt.name, func(t *testing.T) {
			c := newCheck()
			if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
				t.Fatal(err)
			}
			if _, err := ApplyPing(c, ActionStart, t0.Add(10*time.Minute), tt.startRid, sch); err != nil {
				t.Fatal(err)
			}
			if _, err := ApplyPing(c, tt.action, t0.Add(30*time.Minute), tt.endRid, sch); err != nil {
				t.Fatal(err)
			}
			if tt.wantDuration {
				if c.LastDuration == nil || c.LastDuration.Minutes() != 20 {
					t.Errorf("last_duration = %v, want 20m", c.LastDuration)
				}
			} else if c.LastDuration != nil {
				t.Errorf("last_duration = %v, want none", *c.LastDuration)
			}
			if (c.LastStart != nil) != tt.wantRunOpen {
				t.Errorf("last_start = %v, run stays open=%v", c.LastStart, tt.wantRunOpen)
			}
		})
	}

	t.Run("log and ign leave the check untouched", func(t *testing.T) {
		c := newCheck()
		if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
			t.Fatal(err)
		}
		before := *c
		for _, a := range []Action{ActionLog, ActionIgn} {
			flip, err := ApplyPing(c, a, t0.Add(time.Hour), nil, sch)
			if err != nil {
				t.Fatal(err)
			}
			if flip != nil {
				t.Errorf("%s produced a flip", a)
			}
		}
		if !c.LastPing.Equal(*before.LastPing) {
			t.Errorf("%s moved last_ping", ActionLog)
		}
	})

	t.Run("S20 manual resume swallows pings while paused", func(t *testing.T) {
		c := newCheck()
		if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
			t.Fatal(err)
		}
		if Pause(c, t0.Add(time.Hour)) == nil {
			t.Fatal("pause produced no flip")
		}
		c.ManualResume = true

		flip, err := ApplyPing(c, ActionSuccess, t0.Add(2*time.Hour), nil, sch)
		if err != nil {
			t.Fatal(err)
		}
		if flip != nil {
			t.Errorf("flip = %+v, want none", flip)
		}
		if c.Status != StatusPaused {
			t.Errorf("status = %q, want paused", c.Status)
		}
		if !c.LastPing.Equal(t0) {
			t.Errorf("last_ping = %v, must stay at the pre-pause value", *c.LastPing)
		}
	})

	t.Run("pinging a paused check resumes it silently", func(t *testing.T) {
		c := newCheck()
		if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
			t.Fatal(err)
		}
		Pause(c, t0.Add(time.Hour))

		flip, err := ApplyPing(c, ActionSuccess, t0.Add(2*time.Hour), nil, sch)
		if err != nil {
			t.Fatal(err)
		}
		if c.Status != StatusUp {
			t.Errorf("status = %q, want up", c.Status)
		}
		if flip == nil || flip.OldStatus != StatusPaused {
			t.Errorf("flip = %+v", flip)
		}
		if ShouldNotify(flip) {
			t.Error("paused->up must not alert")
		}
	})
}

func TestPauseResume(t *testing.T) {
	c := newCheck()
	sch := &scripted{}
	if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPing(c, ActionStart, t0.Add(time.Hour), nil, sch); err != nil {
		t.Fatal(err)
	}

	at := t0.Add(2 * time.Hour)
	flip := Pause(c, at)
	if flip == nil {
		t.Fatal("pause produced no flip")
	}
	if c.Status != StatusPaused || c.LastStart != nil || c.AlertAfter != nil {
		t.Errorf("after pause: %+v", c)
	}
	if c.LastPing == nil {
		t.Error("pause must keep last_ping")
	}
	if flip.Processed == nil {
		t.Error("pause flips must not be queued for notification")
	}

	if again := Pause(c, at.Add(time.Minute)); again != nil {
		t.Error("pausing twice produced a second flip")
	}

	if f := Resume(c, at); f == nil {
		t.Fatal("resume of a paused check produced no flip")
	}
	if c.Status != StatusNew || c.LastPing != nil || c.LastStart != nil || c.AlertAfter != nil {
		t.Errorf("after resume: %+v", c)
	}
	if c.LastDuration != nil {
		t.Error("resume must clear last_duration")
	}

	c2 := &Check{Status: StatusUp}
	if f := Resume(c2, at); f != nil {
		t.Error("resuming an up check must be rejected by the caller")
	}
}

func TestMarkDown(t *testing.T) {
	c := newCheck()
	sch := &scripted{}
	if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
		t.Fatal(err)
	}

	at := t0.Add(25 * time.Hour)
	flip := MarkDown(c, at)
	if flip == nil {
		t.Fatal("mark down produced no flip")
	}
	if c.Status != StatusDown || c.AlertAfter != nil {
		t.Errorf("after mark down: status=%q alert_after=%v", c.Status, c.AlertAfter)
	}
	if flip.Reason != ReasonTimeout || flip.Processed != nil {
		t.Errorf("flip = %+v, want unprocessed with reason timeout", flip)
	}
	if !flip.Created.Equal(at) {
		t.Errorf("flip created = %v, want %v (the deadline, not the scan time)", flip.Created, at)
	}

	if again := MarkDown(c, at.Add(time.Minute)); again != nil {
		t.Error("marking a down check down again produced a flip")
	}
}

func TestFailTolerance(t *testing.T) {
	sch := &scripted{}

	t.Run("tolerated failures keep the check up and accumulate", func(t *testing.T) {
		c := newCheck()
		c.FailTolerance = 2
		if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
			t.Fatal(err)
		}
		f1, err := ApplyPing(c, ActionFail, t0.Add(time.Minute), nil, sch)
		if err != nil {
			t.Fatal(err)
		}
		if f1 != nil || c.Status != StatusUp || c.FailCount != 1 {
			t.Fatalf("first tolerated fail: flip=%v status=%q count=%d", f1, c.Status, c.FailCount)
		}
		f2, err := ApplyPing(c, ActionFail, t0.Add(2*time.Minute), nil, sch)
		if err != nil {
			t.Fatal(err)
		}
		if f2 != nil || c.Status != StatusUp || c.FailCount != 2 {
			t.Fatalf("second tolerated fail: flip=%v status=%q count=%d", f2, c.Status, c.FailCount)
		}
	})

	t.Run("the failure past tolerance flips down with reason fail", func(t *testing.T) {
		c := newCheck()
		c.FailTolerance = 1
		if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyPing(c, ActionFail, t0.Add(time.Minute), nil, sch); err != nil {
			t.Fatal(err)
		}
		flip, err := ApplyPing(c, ActionFail, t0.Add(2*time.Minute), nil, sch)
		if err != nil {
			t.Fatal(err)
		}
		if c.Status != StatusDown || c.FailCount != 2 {
			t.Fatalf("status=%q count=%d", c.Status, c.FailCount)
		}
		if flip == nil || flip.Reason != ReasonFail || flip.NewStatus != StatusDown {
			t.Fatalf("flip = %+v", flip)
		}
	})

	t.Run("a success resets the consecutive failure count", func(t *testing.T) {
		c := newCheck()
		c.FailTolerance = 1
		if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
			t.Fatal(err)
		}
		// fail, success (count back to 0), fail: still within tolerance.
		if _, err := ApplyPing(c, ActionFail, t0.Add(time.Minute), nil, sch); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyPing(c, ActionSuccess, t0.Add(2*time.Minute), nil, sch); err != nil {
			t.Fatal(err)
		}
		flip, err := ApplyPing(c, ActionFail, t0.Add(3*time.Minute), nil, sch)
		if err != nil {
			t.Fatal(err)
		}
		if flip != nil || c.Status != StatusUp || c.FailCount != 1 {
			t.Fatalf("flip=%v status=%q count=%d", flip, c.Status, c.FailCount)
		}
	})

	t.Run("pause and resume clear the failure count", func(t *testing.T) {
		c := newCheck()
		c.FailTolerance = 2
		if _, err := ApplyPing(c, ActionSuccess, t0, nil, sch); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyPing(c, ActionFail, t0.Add(time.Minute), nil, sch); err != nil {
			t.Fatal(err)
		}
		Pause(c, t0.Add(2*time.Minute))
		if c.FailCount != 0 {
			t.Errorf("pause left fail count = %d", c.FailCount)
		}
		Resume(c, t0.Add(3*time.Minute))
		if c.FailCount != 0 {
			t.Errorf("resume left fail count = %d", c.FailCount)
		}
	})
}
