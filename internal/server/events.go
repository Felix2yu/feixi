package server

import (
	"math"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/format"
)

// eventLookback bounds the event stream when a check has few or no pings.
const eventLookback = 30 * 24 * time.Hour

const (
	eventPingLimit = 100
	eventMaxRows   = 300
)

// events merges pings, status flips and notification deliveries for one check
// into a single newest-first stream, the per-check log the original product has
// but feixi previously split across three unrelated screens.
func (h *frontHandler) events(w http.ResponseWriter, r *http.Request) {
	c, ok := h.checkByName(w, r, chi.URLParam(r, "code"))
	if !ok {
		return
	}
	ctx := r.Context()
	now := time.Now().UTC()

	pings, err := h.db.ListPings(ctx, c.ID, eventPingLimit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	since := now.Add(-eventLookback)
	if len(pings) > 0 {
		if oldest := pings[len(pings)-1].Created; oldest.Before(since) {
			since = oldest
		}
	}

	flips, err := h.db.Flips(ctx, c.ID, &since, nil)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	notifs, err := h.db.NotificationsForCheck(ctx, c.ID, since, eventPingLimit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	rows := make([]streamEvent, 0, len(pings)+len(flips)+len(notifs))

	for _, p := range pings {
		kind := p.Kind
		if kind == "" {
			kind = "success"
		}
		row := map[string]any{
			"type": "ping",
			"date": format.ISODateTime(p.Created),
			"n":    p.N,
			"kind": kind,
			"rid":  p.Rid,
		}
		if p.ExitStatus != nil {
			row["exit_status"] = *p.ExitStatus
		}
		if p.Duration != nil {
			row["duration"] = math.Round(p.Duration.Seconds()*100) / 100
		}
		rows = append(rows, streamEvent{at: p.Created, doc: row})
	}

	for _, f := range flips {
		row := map[string]any{
			"type":       "flip",
			"date":       format.ISOString(&f.Timestamp),
			"up":         f.Up,
			"old_status": string(f.OldStatus),
			"new_status": string(f.NewStatus),
		}
		if f.Reason != engine.ReasonNone {
			row["reason"] = string(f.Reason)
		}
		rows = append(rows, streamEvent{at: f.Timestamp, doc: row})
	}

	for _, n := range notifs {
		created := n.Created
		rows = append(rows, streamEvent{at: created, doc: map[string]any{
			"type":    "notification",
			"date":    format.ISOString(&created),
			"code":    n.Code,
			"channel": n.ChannelName,
			"status":  string(n.CheckStatus),
			"error":   n.Error,
		}})
	}

	sort.SliceStable(rows, func(i, j int) bool { return rows[i].at.After(rows[j].at) })
	if len(rows) > eventMaxRows {
		rows = rows[:eventMaxRows]
	}

	out := make([]map[string]any, 0, len(rows))
	for _, e := range rows {
		out = append(out, e.doc)
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}

type streamEvent struct {
	at  time.Time
	doc map[string]any
}

// statusText answers a single check's state in plain words, for shell scripts
// and menu-bar widgets: Up / Late / Down / New / Paused.
func (h *frontHandler) checkStatusText(w http.ResponseWriter, r *http.Request) {
	c, ok := h.checkByName(w, r, chi.URLParam(r, "code"))
	if !ok {
		return
	}
	display, err := engine.StatusOf(c, time.Now().UTC(), h.db.Sched, false)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	text := "Up"
	switch display {
	case engine.DisplayDown:
		text = "Down"
	case engine.DisplayGrace:
		text = "Late"
	case engine.DisplayNew:
		text = "New"
	case engine.DisplayPaused:
		text = "Paused"
	}
	writeText(w, http.StatusOK, text)
}
