// Package engine holds the check status state machine. It is pure — no IO, no
// database — so every timing rule can be tested with an injected clock.
package engine

import "time"

type Kind string

const (
	KindSimple Kind = "simple"
	KindCron   Kind = "cron"
)

// Status is the persisted state. "grace" and "started" are derived at read time.
type Status string

const (
	StatusUp     Status = "up"
	StatusDown   Status = "down"
	StatusNew    Status = "new"
	StatusPaused Status = "paused"
)

// Display is a status as shown in the UI and badges.
type Display string

const (
	DisplayUp      Display = "up"
	DisplayGrace   Display = "grace"
	DisplayDown    Display = "down"
	DisplayNew     Display = "new"
	DisplayPaused  Display = "paused"
	DisplayStarted Display = "started"
)

// Reason explains why a check went down.
type Reason string

const (
	ReasonNone    Reason = ""
	ReasonTimeout Reason = "timeout"
	ReasonFail    Reason = "fail"
)

// Action is what an incoming ping signals.
type Action string

const (
	ActionSuccess Action = "success"
	ActionStart   Action = "start"
	ActionFail    Action = "fail"
	ActionLog     Action = "log"
	ActionIgn     Action = "ign"
)

const (
	DefaultTimeout = 24 * time.Hour
	DefaultGrace   = time.Hour
	// MaxDuration bounds a reported run length: a stale start ping must not
	// produce a multi-week duration.
	MaxDuration     = 72 * time.Hour
	PingBodyLimit   = 10000
	PingLogLimit    = 100
	FlipRetention   = 93 * 24 * time.Hour
	PruneEveryNPing = 100
)

type Check struct {
	ID          int64
	Code        string
	BadgeKey    string
	ProjectID   int64
	Name        string
	Slug        string
	Tags        string
	Description string
	Created     time.Time

	Kind     Kind
	Timeout  time.Duration
	Grace    time.Duration
	Schedule string
	TZ       string

	NPings       int64
	LastPing     *time.Time
	LastStart    *time.Time
	LastStartRid *string
	LastDuration *time.Duration
	AlertAfter   *time.Time
	Status       Status

	Methods           string
	ManualResume      bool
	FilterHTTPBody    bool
	FilterDefaultFail bool
	StartKW           string
	SuccessKW         string
	FailureKW         string
}

func (c *Check) NameThenCode() string {
	if c.Name != "" {
		return c.Name
	}
	return c.Code
}
