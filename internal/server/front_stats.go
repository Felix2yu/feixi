package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Felix2yu/feixi/internal/cronex"
	"github.com/Felix2yu/feixi/internal/engine"
	"github.com/Felix2yu/feixi/internal/store"
	"github.com/Felix2yu/feixi/internal/tzutil"
)

// mountStats adds the detail-page reads plus the few mutations the dashboard
// offers on a single check.
func (h *frontHandler) mountStats(r chi.Router) {
	r.Get("/project", h.project)
	r.Post("/project", h.updateProject)
	r.Get("/checks/{code}/downtimes", h.downtimes)
	r.Get("/checks/{code}/events", h.events)
	r.Get("/checks/{code}/status", h.checkStatusText)
	r.Post("/checks/{code}/clear-events", h.clearEvents)
	r.Get("/notifications", h.notifications)
	r.Post("/notifications/{code}/retry", h.retryNotification)
	r.Post("/schedule/preview", h.schedulePreview)
}

// clearEvents drops the ping history and returns the check to new, for a job
// whose shape changed and whose numbers should start over.
func (h *frontHandler) clearEvents(w http.ResponseWriter, r *http.Request) {
	c, ok := h.checkByName(w, r, chi.URLParam(r, "code"))
	if !ok {
		return
	}
	if _, err := h.db.ClearEvents(r.Context(), c.ID); err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeText(w, http.StatusOK, "OK")
}

func (h *frontHandler) project(w http.ResponseWriter, r *http.Request) {
	project, err := h.db.Project(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	count, _ := h.db.CountChecks(r.Context(), project.ID)
	writeJSON(w, http.StatusOK, h.projectPayload(project, count))
}

// updateProject writes the two settings the page owns; fields left out of the
// body keep their current value.
func (h *frontHandler) updateProject(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name      *string `json:"name"`
		ShowSlugs *bool   `json:"show_slugs"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "不是合法的 JSON")
		return
	}

	project, err := h.db.Project(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	name, showSlugs := project.Name, project.ShowSlugs
	if body.Name != nil {
		name = strings.TrimSpace(*body.Name)
		if n := len([]rune(name)); n == 0 || n > 200 {
			jsonError(w, http.StatusBadRequest, "项目名需要 1 到 200 个字符")
			return
		}
	}
	if body.ShowSlugs != nil {
		showSlugs = *body.ShowSlugs
	}

	if err := h.db.SetProject(r.Context(), project.ID, name, showSlugs); err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	project.Name, project.ShowSlugs = name, showSlugs
	count, _ := h.db.CountChecks(r.Context(), project.ID)
	writeJSON(w, http.StatusOK, h.projectPayload(project, count))
}

func (h *frontHandler) projectPayload(project *store.Project, checkCount int) map[string]any {
	return map[string]any{
		"name":               project.Name,
		"badge_key":          project.BadgeKey,
		"ping_key":           project.PingKey,
		"show_slugs":         project.ShowSlugs,
		"check_count":        checkCount,
		"max_checks":         h.cfg.MaxChecks,
		"ping_body_limit":    h.cfg.PingBodyLimit,
		"ping_log_limit":     h.cfg.PingLogLimit,
		"site_root":          h.cfg.SiteRoot,
		"master_badge_label": h.cfg.MasterBadgeLabel,
	}
}

// boundedInt parses a positive integer query parameter without strconv's panic
// surface; anything odd falls back to the default.
func boundedInt(raw string, def, min, max int) (int, bool) {
	if raw == "" {
		return def, true
	}
	n := 0
	for _, ch := range raw {
		if ch < '0' || ch > '9' {
			return def, false
		}
		n = n*10 + int(ch-'0')
		if n > max {
			return def, false
		}
	}
	if n < min {
		return def, false
	}
	return n, true
}

func (h *frontHandler) checkByName(w http.ResponseWriter, r *http.Request, code string) (*engine.Check, bool) {
	c, err := h.db.CheckByCode(r.Context(), code)
	if err != nil {
		jsonError(w, http.StatusNotFound, "未找到")
		return nil, false
	}
	project, err := h.db.Project(r.Context())
	if err != nil || c.ProjectID != project.ID {
		jsonError(w, http.StatusNotFound, "未找到")
		return nil, false
	}
	return c, true
}

// downtimes feeds the monthly outage grid on the detail page.
func (h *frontHandler) downtimes(w http.ResponseWriter, r *http.Request) {
	c, ok := h.checkByName(w, r, chi.URLParam(r, "code"))
	if !ok {
		return
	}

	tz := r.URL.Query().Get("tz")
	if tz == "" {
		tz = "UTC"
	}
	loc, err := tzutil.Load(tz)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "未知的时区")
		return
	}
	months, valid := boundedInt(r.URL.Query().Get("months"), 6, 1, 12)
	if !valid {
		jsonError(w, http.StatusBadRequest, "months 需要在 1 到 12 之间")
		return
	}

	records, err := h.db.Downtimes(r.Context(), c, months, tz, time.Now().UTC())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	out := make([]map[string]any, 0, len(records))
	for _, rec := range records {
		out = append(out, map[string]any{
			"boundary": rec.Boundary.In(loc).Format("2006-01-02"),
			"seconds":  int(rec.Duration.Seconds()),
			"count":    rec.Count,
			"uptime":   rec.Uptime(loc),
			"no_data":  rec.NoData,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"downtimes": out, "tz": tz})
}

func (h *frontHandler) notifications(w http.ResponseWriter, r *http.Request) {
	limit, valid := boundedInt(r.URL.Query().Get("limit"), 50, 1, 500)
	if !valid {
		limit = 50
	}

	rows, err := h.db.Notifications(r.Context(), limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	out := make([]map[string]any, 0, len(rows))
	for _, n := range rows {
		created := n.Created
		out = append(out, map[string]any{
			"code":         n.Code,
			"created":      isoTime(&created),
			"check_name":   n.CheckName,
			"channel_name": n.ChannelName,
			"status":       string(n.CheckStatus),
			"error":        n.Error,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out})
}

// retryNotification re-sends one past alert through its original channel.
func (h *frontHandler) retryNotification(w http.ResponseWriter, r *http.Request) {
	if h.resender == nil {
		jsonError(w, http.StatusServiceUnavailable, "通知器未就绪")
		return
	}
	n, err := h.db.NotificationByCode(r.Context(), chi.URLParam(r, "code"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "未找到")
		} else {
			jsonError(w, http.StatusInternalServerError, "服务器错误")
		}
		return
	}
	if err := h.resender.RetryNotification(r.Context(), n); err != nil {
		jsonError(w, http.StatusBadGateway, errTextForUI(err))
		return
	}
	writeText(w, http.StatusOK, "OK")
}

// schedulePreview powers the cron builder: it echoes the next occurrences so the
// user sees what the expression means before saving.
func (h *frontHandler) schedulePreview(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Schedule string `json:"schedule"`
		TZ       string `json:"tz"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&body); err != nil {
		jsonError(w, http.StatusBadRequest, "不是合法的 JSON")
		return
	}
	if err := cronex.Validate(body.Schedule); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	tz := body.TZ
	if tz == "" {
		tz = "UTC"
	}
	loc, err := tzutil.Load(tz)
	if err != nil {
		jsonError(w, http.StatusBadRequest, "未知的时区")
		return
	}

	after := time.Now().UTC()
	out := make([]string, 0, 3)
	for i := 0; i < 3; i++ {
		next, ok, err := cronex.Scheduler{}.Next(body.Schedule, tz, after)
		if err != nil || !ok {
			break
		}
		out = append(out, next.In(loc).Format("2006-01-02 15:04:05 MST"))
		after = next
	}
	writeJSON(w, http.StatusOK, map[string]any{"next": out, "tz": tz})
}
