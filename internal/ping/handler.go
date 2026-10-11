// Package ping implements the incoming ping endpoints.
package ping

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/store"
)

type Handler struct {
	DB        *store.DB
	BodyLimit int
	// OnPrune is called after a ping that crossed the prune threshold, so the
	// trimming itself happens outside the request.
	OnPrune func(checkID int64)
}

// "success" is an explicit alias of the bare URL, so a script can name the
// signal it sends instead of relying on the empty suffix.
var actions = map[string]engine.Action{
	"success": engine.ActionSuccess,
	"start":   engine.ActionStart,
	"fail":    engine.ActionFail,
	"log":     engine.ActionLog,
	"ign":     engine.ActionIgn,
}

// Root dispatches /ping/{first}[/{second}[/{third}]].
//
// The two URL shapes are told apart by their first segment: a UUID is a check
// code, anything else is a project ping_key plus a slug. chi cannot hold both
// patterns on one segment, so the rule lives here instead.
func (h *Handler) Root(w http.ResponseWriter, r *http.Request) {
	first, second, third := chi.URLParam(r, "first"), chi.URLParam(r, "second"), chi.URLParam(r, "third")

	if _, err := uuid.Parse(first); err == nil && third == "" {
		h.byCode(w, r, first, second)
		return
	}
	h.bySlug(w, r, first, second, third)
}

func (h *Handler) byCode(w http.ResponseWriter, r *http.Request, code, suffix string) {
	c, err := h.DB.CheckByCode(r.Context(), code)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			h.write(w, http.StatusNotFound, "未找到")
		} else {
			h.write(w, http.StatusInternalServerError, "服务器错误")
		}
		return
	}
	status, body := h.process(r, c, suffix)
	h.write(w, status, body)
}

// bySlug serves /ping/{pingKey}/{slug}; with ?create=1 the first ping also
// provisions the check, so task names can be invented on the fly.
func (h *Handler) bySlug(w http.ResponseWriter, r *http.Request, pingKey, slug, suffix string) {
	ctx := r.Context()
	if slug == "" || slug != strings.ToLower(slug) {
		h.write(w, http.StatusBadRequest, "无效的链接格式")
		return
	}

	project, err := h.DB.ProjectByPingKey(ctx, pingKey)
	if err != nil {
		h.write(w, http.StatusNotFound, "未找到")
		return
	}

	c, err := h.DB.CheckBySlugStrict(ctx, project.ID, slug)
	created := false
	switch {
	case errors.Is(err, store.ErrNotUnique):
		h.write(w, http.StatusConflict, "标识存在歧义")
		return
	case errors.Is(err, store.ErrNotFound):
		if r.URL.Query().Get("create") != "1" {
			h.write(w, http.StatusNotFound, "未找到")
			return
		}
		ids, _ := h.DB.AllChannelIDs(ctx, project.ID)
		newCheck, cerr := h.DB.CreateCheck(ctx, &store.NewCheck{
			ProjectID:  project.ID,
			Name:       slug,
			Slug:       slug,
			Kind:       engine.KindSimple,
			Timeout:    engine.DefaultTimeout,
			Grace:      engine.DefaultGrace,
			TZ:         "UTC",
			ChannelIDs: ids,
		})
		if cerr != nil {
			if errors.Is(cerr, store.ErrTooManyChecks) {
				h.write(w, http.StatusNotFound, "未找到")
				return
			}
			h.write(w, http.StatusInternalServerError, "服务器错误")
			return
		}
		c, created = newCheck, true
	case err != nil:
		h.write(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	status, body := h.process(r, c, suffix)
	if created && status == http.StatusOK {
		status, body = http.StatusCreated, "Created"
	}
	h.write(w, status, body)
}

// process turns one HTTP request into a recorded ping and returns the status
// and body the caller should send.
func (h *Handler) process(r *http.Request, c *engine.Check, suffix string) (int, string) {
	action, exitstatus, ok := parseSuffix(suffix)
	if !ok {
		return http.StatusNotFound, "未找到"
	}
	if exitstatus != nil && *exitstatus > 0 && *exitstatus <= 255 {
		action = engine.ActionFail
	} else if exitstatus != nil && *exitstatus > 255 {
		return http.StatusBadRequest, "无效的链接格式"
	}
	rid, err := parseRid(r)
	if err != nil {
		return http.StatusBadRequest, "无效的 UUID 格式"
	}

	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, int64(h.BodyLimit)+1))
	}
	if len(body) > h.BodyLimit {
		body = body[:h.BodyLimit]
	}

	action = resolveAction(c, action, r.Method, body)

	_, _, _, prune, err := h.DB.RecordPing(r.Context(), c.ID, action, store.PingInput{
		Scheme:     scheme(r),
		RemoteAddr: remoteAddr(r),
		Method:     r.Method,
		UA:         truncate(r.UserAgent(), 200),
		Body:       body,
		ExitStatus: exitstatus,
		Rid:        rid,
	})
	if err != nil {
		return http.StatusInternalServerError, "服务器错误"
	}
	if prune && h.OnPrune != nil {
		h.OnPrune(c.ID)
	}
	return http.StatusOK, "OK"
}

func (h *Handler) write(w http.ResponseWriter, status int, body string) {
	head := w.Header()
	head.Set("Ping-Body-Limit", strconv.Itoa(h.BodyLimit))
	head.Set("Access-Control-Allow-Origin", "*")
	head.Set("Cache-Control", "no-cache")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

func resolveAction(c *engine.Check, action engine.Action, method string, body []byte) engine.Action {
	if c.Methods == "POST" && method != "POST" {
		action = engine.ActionIgn
	}
	if action == engine.ActionIgn || !c.FilterHTTPBody {
		return action
	}

	text := string(body)
	switch {
	case c.FailureKW != "" && matchKeywords(text, c.FailureKW):
		return engine.ActionFail
	case c.SuccessKW != "" && matchKeywords(text, c.SuccessKW):
		return engine.ActionSuccess
	case c.StartKW != "" && matchKeywords(text, c.StartKW):
		return engine.ActionStart
	case c.FilterDefaultFail:
		return engine.ActionFail
	default:
		return engine.ActionIgn
	}
}

// matchKeywords is the original comma-separated, case-sensitive substring list.
func matchKeywords(haystack, keywords string) bool {
	for _, s := range strings.Split(keywords, ",") {
		s = strings.TrimSpace(s)
		if s != "" && strings.Contains(haystack, s) {
			return true
		}
	}
	return false
}

func parseSuffix(s string) (engine.Action, *int64, bool) {
	if s == "" {
		return engine.ActionSuccess, nil, true
	}
	if a, ok := actions[s]; ok {
		return a, nil, true
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return "", nil, false
	}
	v := n
	return engine.ActionSuccess, &v, true
}

func parseRid(r *http.Request) (*string, error) {
	raw := r.URL.Query().Get("rid")
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, err
	}
	s := id.String()
	return &s, nil
}

func remoteAddr(r *http.Request) string {
	addr := r.Header.Get("X-Forwarded-For")
	if addr == "" {
		// Go's RemoteAddr is "ip:port"; Django hands the bare address over.
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
			return host
		}
		addr = r.RemoteAddr
	}
	addr = strings.TrimSpace(strings.Split(addr, ",")[0])
	if net.ParseIP(addr) == nil {
		// Some platforms report "ipv4:port" behind a proxy; drop the port.
		parts := strings.Split(addr, ".")
		if len(parts) == 4 && strings.Contains(parts[3], ":") {
			addr = strings.Split(addr, ":")[0]
		}
	}
	return addr
}

func scheme(r *http.Request) string {
	if s := r.Header.Get("X-Forwarded-Proto"); s != "" {
		return s
	}
	if r.TLS != nil {
		return "https"
	}
	return "http"
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
