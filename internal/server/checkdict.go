package server

import (
	"context"
	"strconv"
	"time"

	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/format"
	"github.com/yufei/feixi/internal/store"
)

// pingURL honours the project's preferred URL shape: the slug form reads better
// in scripts, but a check without a slug keeps its UUID address.
func pingURL(siteRoot string, project *store.Project, c *engine.Check) string {
	if project != nil && project.ShowSlugs && c.Slug != "" {
		return siteRoot + "/ping/" + project.PingKey + "/" + c.Slug
	}
	return siteRoot + "/ping/" + c.Code
}

// CheckDict is the API representation of a check. Field names and JSON shapes
// are the compatibility surface for existing agents and scripts.
type CheckDict struct {
	Name              string  `json:"name"`
	Slug              string  `json:"slug"`
	Tags              string  `json:"tags"`
	Desc              string  `json:"desc"`
	Grace             int     `json:"grace"`
	NPings            int64   `json:"n_pings"`
	Status            string  `json:"status"`
	Started           bool    `json:"started"`
	LastPing          *string `json:"last_ping"`
	NextPing          *string `json:"next_ping"`
	ManualResume      bool    `json:"manual_resume"`
	Methods           string  `json:"methods"`
	Subject           string  `json:"subject"`
	SubjectFail       string  `json:"subject_fail"`
	StartKW           string  `json:"start_kw"`
	SuccessKW         string  `json:"success_kw"`
	FailureKW         string  `json:"failure_kw"`
	FilterSubject     bool    `json:"filter_subject"` // 只有邮件通道用得上，本版本恒 false
	FilterBody        bool    `json:"filter_body"`    // 同上；留字段是为了读取方的字段表完整
	FilterHTTPBody    bool    `json:"filter_http_body"`
	FilterDefaultFail bool    `json:"filter_default_fail"`
	FailureTolerance  int     `json:"failure_tolerance"`
	FailureCount      int     `json:"failure_count,omitempty"`
	Flapping          bool    `json:"flapping,omitempty"`
	BadgeURL          string  `json:"badge_url"`

	UUID      string `json:"uuid"`
	PingURL   string `json:"ping_url"`
	UpdateURL string `json:"update_url"`
	PauseURL  string `json:"pause_url"`
	ResumeURL string `json:"resume_url"`
	Channels  string `json:"channels"`

	Timeout      *int    `json:"timeout,omitempty"`
	Schedule     *string `json:"schedule,omitempty"`
	TZ           *string `json:"tz,omitempty"`
	LastDuration *int    `json:"last_duration,omitempty"`
}

func (h *apiHandler) checkDict(ctx context.Context, c *engine.Check, version int, project *store.Project) (CheckDict, error) {
	// Only API v1 exposes "started"; every other reader wants the five real
	// statuses.
	withStarted := version == 1

	status, err := engine.StatusOf(c, time.Now().UTC(), h.db.Sched, withStarted)
	if err != nil {
		return CheckDict{}, err
	}
	graceStart, hasGraceStart, err := engine.GraceStart(c, h.db.Sched, true)
	if err != nil {
		return CheckDict{}, err
	}

	d := CheckDict{
		Name:              c.Name,
		Slug:              c.Slug,
		Tags:              c.Tags,
		Desc:              c.Description,
		Grace:             format.DurationSeconds(c.Grace),
		NPings:            c.NPings,
		Status:            string(status),
		Started:           c.LastStart != nil,
		LastPing:          format.ISOString(c.LastPing),
		ManualResume:      c.ManualResume,
		Methods:           c.Methods,
		Subject:           c.SuccessKW,
		SubjectFail:       c.FailureKW,
		StartKW:           c.StartKW,
		SuccessKW:         c.SuccessKW,
		FailureKW:         c.FailureKW,
		FilterHTTPBody:    c.FilterHTTPBody,
		FilterDefaultFail: c.FilterDefaultFail,
		FailureTolerance:  c.FailTolerance,
		FailureCount:      c.FailCount,
		Flapping:          c.Flapping,
		BadgeURL:          h.cfg.SiteRoot + "/b/2/" + c.BadgeKey + ".svg",
		UUID:              c.Code,
		PingURL:           pingURL(h.cfg.SiteRoot, project, c),
		Channels:          h.channelCodesFor(ctx, c.ID),
	}
	if hasGraceStart {
		d.NextPing = format.ISOString(&graceStart)
	}
	// A zero-length run is falsy in the original (`if self.last_duration`), so
	// the key disappears rather than showing 0.
	if c.LastDuration != nil && *c.LastDuration > 0 && *c.LastDuration < engine.MaxDuration {
		v := format.DurationSeconds(*c.LastDuration)
		d.LastDuration = &v
	}

	updateURL := h.cfg.SiteRoot + "/api/v" + strconv.Itoa(version) + "/checks/" + c.Code
	d.UpdateURL = updateURL
	d.PauseURL = updateURL + "/pause"
	d.ResumeURL = updateURL + "/resume"

	if c.Kind == engine.KindSimple {
		v := format.DurationSeconds(c.Timeout)
		d.Timeout = &v
	} else {
		schedule, tz := c.Schedule, c.TZ
		d.Schedule, d.TZ = &schedule, &tz
	}
	return d, nil
}
