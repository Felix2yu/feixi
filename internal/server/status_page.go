package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Felix2yu/feixi/internal/engine"
	"github.com/Felix2yu/feixi/internal/format"
	"github.com/Felix2yu/feixi/internal/store"
)

// statusPageCheck is the public, read-only projection of one check. It must
// never carry ping URLs, channels or other management data: the badge_key in
// the URL is an unguessable capability, but the payload stays presentational.
type statusPageCheck struct {
	Name     string  `json:"name"`
	Tags     string  `json:"tags"`
	Status   string  `json:"status"`
	Started  bool    `json:"started"`
	Flapping bool    `json:"flapping"`
	LastPing *string `json:"last_ping"`
}

type statusPagePayload struct {
	Project string `json:"project"`
	Summary string `json:"summary"`
	Total   int    `json:"total"`
	Down    int    `json:"down"`
	Grace   int    `json:"grace"`
	Updated string `json:"updated_at"`

	Checks []statusPageCheck `json:"checks"`
}

// statusPage serves GET /front/status-page/{badgeKey}: a public snapshot for
// the shareable status page. A wrong key behaves like a missing resource.
func (h *frontHandler) statusPage(w http.ResponseWriter, r *http.Request) {
	project, err := h.db.ProjectByBadgeKey(r.Context(), chi.URLParam(r, "key"))
	if err != nil {
		writeText(w, http.StatusNotFound, "未找到")
		return
	}
	checks, err := h.db.ListChecks(r.Context(), project.ID, store.CheckFilter{})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	now := time.Now().UTC()
	payload := statusPagePayload{
		Project: project.Name,
		Summary: "ok",
		Updated: *format.ISOString(&now),
		Checks:  make([]statusPageCheck, 0, len(checks)),
	}

	for _, c := range checks {
		status, err := engine.StatusOf(c, now, h.db.Sched, false)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "服务器错误")
			return
		}
		if status == engine.DisplayDown {
			payload.Down++
		}
		if status == engine.DisplayGrace {
			payload.Grace++
		}
		payload.Checks = append(payload.Checks, statusPageCheck{
			Name:     c.NameThenCode(),
			Tags:     c.Tags,
			Status:   string(status),
			Started:  c.LastStart != nil,
			Flapping: c.Flapping,
			LastPing: format.ISOString(c.LastPing),
		})
	}
	payload.Total = len(checks)
	switch {
	case payload.Down > 0:
		payload.Summary = "down"
	case payload.Grace > 0:
		payload.Summary = "grace"
	}
	writeJSON(w, http.StatusOK, payload)
}
