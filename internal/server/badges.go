package server

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Felix2yu/feixi/internal/badge"
	"github.com/Felix2yu/feixi/internal/config"
	"github.com/Felix2yu/feixi/internal/engine"
	"github.com/Felix2yu/feixi/internal/store"
)

type badgeHandler struct {
	db  *store.DB
	cfg *config.Config
}

func (h *badgeHandler) secret() string {
	secret, _ := h.db.Setting(context.Background(), "secret_key")
	return secret
}

// aggregate folds the statuses of every check under a tag: one down check makes
// the whole badge down, and grace only shows up on three-state badges.
// stopEarly mirrors the original's SVG shortcut, where the first down is enough.
func (h *badgeHandler) aggregate(checks []*engine.Check, tag string, withLate, stopEarly bool) (string, int, int, int) {
	status := "up"
	total, grace, down := 0, 0, 0
	now := time.Now().UTC()

	for _, c := range checks {
		if tag != "*" && !hasTag(c.Tags, tag) {
			continue
		}
		total++
		s, err := engine.StatusOf(c, now, h.db.Sched, false)
		if err != nil {
			continue
		}
		switch s {
		case engine.DisplayDown:
			down++
			status = "down"
			if stopEarly {
				return status, total, grace, down
			}
		case engine.DisplayGrace:
			grace++
			if status == "up" && withLate {
				status = "late"
			}
		}
	}
	return status, total, grace, down
}

func (h *badgeHandler) aggregateOne(c *engine.Check, states int) (string, int, int, int) {
	s, err := engine.StatusOf(c, time.Now().UTC(), h.db.Sched, false)
	if err != nil {
		return "up", 1, 0, 0
	}
	status, grace, down := "up", 0, 0
	switch s {
	case engine.DisplayDown:
		status, down = "down", 1
	case engine.DisplayGrace:
		grace = 1
		if states == 3 {
			status = "late"
		}
	}
	return status, 1, grace, down
}

func hasTag(tags, want string) bool {
	for _, t := range strings.Fields(tags) {
		if t == want {
			return true
		}
	}
	return false
}

// splitSegment pulls the format off the last path element, "abc123.svg".
func splitSegment(seg string) (name, format string) {
	i := strings.LastIndex(seg, ".")
	if i < 0 {
		return seg, ""
	}
	return seg[:i], seg[i+1:]
}

func validFormat(format string) bool {
	switch format {
	case "svg", "json", "shields":
		return true
	}
	return false
}

// badgeAll serves /badge/{badgeKey}/{sig}.{fmt} — the all-tags badge.
func (h *badgeHandler) badgeAll(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "badgeKey")
	sig, format := splitSegment(chi.URLParam(r, "seg"))
	h.render(w, r, key, "*", sig, format)
}

// badgeTag serves /badge/{badgeKey}/{sig}/{tag}.{fmt}.
func (h *badgeHandler) badgeTag(w http.ResponseWriter, r *http.Request) {
	key := chi.URLParam(r, "badgeKey")
	sig := chi.URLParam(r, "sig")
	tag, format := splitSegment(chi.URLParam(r, "tagfmt"))
	h.render(w, r, key, tag, sig, format)
}

func (h *badgeHandler) render(w http.ResponseWriter, r *http.Request, key, tag, sig, format string) {
	if !validFormat(format) {
		writeText(w, http.StatusNotFound, "未找到")
		return
	}
	if !badge.Check(h.secret(), key, tag, sig) {
		writeText(w, http.StatusNotFound, "未找到")
		return
	}

	project, err := h.db.ProjectByBadgeKey(r.Context(), key)
	if err != nil {
		writeText(w, http.StatusNotFound, "未找到")
		return
	}
	checks, err := h.db.ListChecks(r.Context(), project.ID, store.CheckFilter{})
	if err != nil {
		writeText(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	label := tag
	if tag == "*" {
		label = h.cfg.MasterBadgeLabel
	}
	status, total, grace, down := h.aggregate(checks, tag,
		badge.WithLate(sig), format == "svg")
	h.respond(w, format, label, status, total, grace, down)
}

// checkBadge serves /b/{2|3}/{badgeKey}.{fmt} for a single check.
func (h *badgeHandler) checkBadge(w http.ResponseWriter, r *http.Request) {
	states, _ := strconv.Atoi(chi.URLParam(r, "states"))
	key, format := splitSegment(chi.URLParam(r, "seg"))
	if !validFormat(format) || (states != 2 && states != 3) {
		writeText(w, http.StatusNotFound, "未找到")
		return
	}

	c, err := h.db.CheckByBadgeKey(r.Context(), key)
	if err != nil {
		writeText(w, http.StatusNotFound, "未找到")
		return
	}
	label := c.NameThenCode()
	status, total, grace, down := h.aggregateOne(c, states)
	h.respond(w, format, label, status, total, grace, down)
}

func (h *badgeHandler) respond(w http.ResponseWriter, format, label, status string, total, grace, down int) {
	switch format {
	case "json":
		writeJSON(w, http.StatusOK, map[string]any{
			"status": status, "total": total, "grace": grace, "down": down,
		})
	case "shields":
		writeJSON(w, http.StatusOK, map[string]any{
			"schemaVersion": 1, "label": label, "message": status,
			"color": badge.ShieldsColors[status],
		})
	default:
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		io.WriteString(w, badge.SVG(label, status))
	}
}

// list backs GET /api/v3/badges/: signed URLs for every tag, in all formats.
func (h *badgeHandler) list(w http.ResponseWriter, r *http.Request) {
	project, err := h.db.Project(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	checks, err := h.db.ListChecks(r.Context(), project.ID, store.CheckFilter{})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	tags := []string{"*"}
	seen := map[string]bool{"*": true}
	for _, c := range checks {
		for _, tag := range strings.Fields(c.Tags) {
			if !seen[tag] {
				seen[tag] = true
				tags = append(tags, tag)
			}
		}
	}

	secret := h.secret()
	out := map[string]map[string]string{}
	for _, tag := range tags {
		suffix := ""
		if tag != "*" {
			suffix = "/" + tag
		}
		base2 := h.cfg.SiteRoot + "/badge/" + project.BadgeKey + "/" +
			badge.Sign(secret, project.BadgeKey, tag, false) + suffix
		base3 := h.cfg.SiteRoot + "/badge/" + project.BadgeKey + "/" +
			badge.Sign(secret, project.BadgeKey, tag, true) + suffix
		out[tag] = map[string]string{
			"svg":      base2 + ".svg",
			"json":     base2 + ".json",
			"shields":  base2 + ".shields",
			"svg3":     base3 + ".svg",
			"json3":    base3 + ".json",
			"shields3": base3 + ".shields",
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"badges": out})
}
