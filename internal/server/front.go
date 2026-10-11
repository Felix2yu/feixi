package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yufei/feixi/internal/config"
	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/format"
	"github.com/yufei/feixi/internal/store"
)

// isoTime exposes an optional timestamp as an ISO string or null.
func isoTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := format.ISOString(t)
	return s
}

// mountFront registers the polling endpoint the dashboard refreshes against and
// the SPA itself.
func mountFront(r chi.Router, db *store.DB, cfg *config.Config, schemas []string, tester Tester) {
	h := &frontHandler{db: db, cfg: cfg, schemas: schemas, tester: tester}
	r.Route("/front", func(r chi.Router) {
		r.Get("/status", h.status)
		r.Get("/meta", h.meta)
		h.mountChannels(r)
		h.mountStats(r)
	})
}

type frontHandler struct {
	db      *store.DB
	cfg     *config.Config
	schemas []string
	tester  Tester
}

func sha256sum(b []byte) []byte {
	sum := sha256.Sum256(b)
	return sum[:]
}

type statusRow struct {
	Code     string  `json:"code"`
	Status   string  `json:"status"`
	Started  bool    `json:"started"`
	LastPing *string `json:"last_ping"`
	NextPing *string `json:"next_ping"`
}

// status returns the derived status of every check. The original renders the
// last-ping cell as HTML here; we send data instead and let the SPA format it,
// which keeps styling in the frontend and removes an injection surface.
func (h *frontHandler) status(w http.ResponseWriter, r *http.Request) {
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

	now := time.Now().UTC()
	payload := map[string]any{}
	rows := make([]statusRow, 0, len(checks))
	byTag := map[string]string{}
	numDown := 0
	numGrace := 0

	// rank orders statuses so a tag shows its worst member.
	rank := map[engine.Display]int{
		engine.DisplayDown:   5,
		engine.DisplayGrace:  4,
		engine.DisplayUp:     3,
		engine.DisplayNew:    2,
		engine.DisplayPaused: 1,
	}

	for _, c := range checks {
		status, err := engine.StatusOf(c, now, h.db.Sched, false)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "服务器错误")
			return
		}
		row := statusRow{Code: c.Code, Status: string(status), Started: c.LastStart != nil,
			LastPing: format.ISOString(c.LastPing)}
		if status == engine.DisplayDown {
			numDown++
		}
		if status == engine.DisplayGrace {
			numGrace++
		}
		if gs, ok, err := engine.GraceStart(c, h.db.Sched, true); err == nil && ok {
			row.NextPing = format.ISOString(&gs)
		}
		rows = append(rows, row)

		for _, tag := range strings.Fields(c.Tags) {
			if cur, seen := byTag[tag]; !seen || rank[engine.Display(cur)] < rank[status] {
				byTag[tag] = string(status)
			}
		}
	}

	if numDown > 0 {
		payload["title"] = strconv.Itoa(numDown) + " 宕机"
	} else if numGrace > 0 {
		payload["title"] = strconv.Itoa(numGrace) + " 延迟"
	} else {
		payload["title"] = "飞檄"
	}
	payload["details"] = rows
	payload["tags"] = byTag

	body, err := json.Marshal(payload)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	etag := `"` + hex.EncodeToString(sha256sum(body))[:16] + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "no-store")
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(body)
}

// meta exposes what the SPA needs to render pickers: the supported apprise
// URL schemes and the timezone list is left to the browser's Intl API.
func (h *frontHandler) meta(w http.ResponseWriter, r *http.Request) {
	schemas := append([]string{}, h.schemas...)
	sort.Strings(schemas)
	writeJSON(w, http.StatusOK, map[string]any{
		"ping_body_limit": h.cfg.PingBodyLimit,
		"max_checks":      h.cfg.MaxChecks,
		"site_root":       h.cfg.SiteRoot,
		"apprise_schemas": schemas,
	})
}

func logRequest(r *http.Request, start time.Time) {
	slog.Info("请求",
		"method", r.Method,
		"path", r.URL.Path,
		"ms", time.Since(start).Milliseconds(),
		"remote", r.RemoteAddr,
	)
}
