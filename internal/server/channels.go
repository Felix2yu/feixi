package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	apprise "github.com/unraid/apprise-go"

	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/store"
)

// Tester sends a test notification through a channel. The notify worker
// satisfies it; keeping the interface here avoids a server → notify import.
type Tester interface {
	TestMessage(ch *store.Channel, c *engine.Check, status engine.Status) error
}

type channelDict struct {
	Code               string   `json:"code"`
	Name               string   `json:"name"`
	Kind               string   `json:"kind"`
	URLs               []string `json:"urls"`
	Up                 bool     `json:"up"`
	Down               bool     `json:"down"`
	Disabled           bool     `json:"disabled"`
	LastNotify         *string  `json:"last_notify"`
	LastNotifyDuration *int     `json:"last_notify_duration"`
	LastError          string   `json:"last_error"`
}

func toChannelDict(ch *store.Channel) channelDict {
	d := channelDict{
		Code:      ch.Code,
		Name:      ch.Name,
		Kind:      "apprise",
		URLs:      ch.URLs,
		Up:        ch.Conf.Up,
		Down:      ch.Conf.Down,
		Disabled:  ch.Disabled,
		LastError: ch.LastError,
	}
	d.LastNotify = isoTime(ch.LastNotify)
	if ch.LastNotifyDuration != nil {
		v := int(*ch.LastNotifyDuration / 1000000)
		d.LastNotifyDuration = &v
	}
	return d
}

// validateAppriseURL asks apprise itself, so a typo is reported while the user
// is still looking at the form.
func validateAppriseURL(u string) error {
	client := apprise.New()
	if err := client.Add(u); err != nil {
		return errors.New("apprise URL 无法识别：" + err.Error())
	}
	return nil
}

func (h *frontHandler) mountChannels(r chi.Router) {
	r.Get("/channels", h.listChannels)
	r.Post("/channels", h.createChannel)
	r.Post("/channels/{code}", h.updateChannel)
	r.Delete("/channels/{code}", h.deleteChannel)
	r.Post("/channels/{code}/test", h.testChannel)
}

func (h *frontHandler) listChannels(w http.ResponseWriter, r *http.Request) {
	project, err := h.db.Project(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	channels, err := h.db.Channels(r.Context(), project.ID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	out := make([]channelDict, 0, len(channels))
	for _, ch := range channels {
		out = append(out, toChannelDict(ch))
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}

type channelBody struct {
	Name     string   `json:"name"`
	URLs     []string `json:"urls"`
	Up       *bool    `json:"up"`
	Down     *bool    `json:"down"`
	Disabled *bool    `json:"disabled"`
}

func decodeChannel(w http.ResponseWriter, r *http.Request) (*channelBody, bool) {
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		jsonError(w, http.StatusBadRequest, "请求体读取失败")
		return nil, false
	}
	b := &channelBody{}
	if err := json.Unmarshal(raw, b); err != nil {
		jsonError(w, http.StatusBadRequest, "不是合法的 JSON")
		return nil, false
	}
	return b, true
}

// cleanURLs validates each line with apprise itself, so a typo is reported now
// rather than at the next outage.
func cleanURLs(in []string) ([]string, error) {
	var out []string
	for _, line := range in {
		for _, u := range strings.Split(line, "\n") {
			u = strings.TrimSpace(u)
			if u == "" {
				continue
			}
			if err := validateAppriseURL(u); err != nil {
				return nil, err
			}
			out = append(out, u)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("至少要填一个 apprise URL")
	}
	return out, nil
}

func (h *frontHandler) createChannel(w http.ResponseWriter, r *http.Request) {
	project, err := h.db.Project(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	b, ok := decodeChannel(w, r)
	if !ok {
		return
	}
	urls, err := cleanURLs(b.URLs)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	conf := store.ChannelConf{Up: true, Down: true}
	if b.Up != nil {
		conf.Up = *b.Up
	}
	if b.Down != nil {
		conf.Down = *b.Down
	}

	ch, err := h.db.CreateChannel(r.Context(), &store.NewChannel{
		ProjectID: project.ID,
		Name:      b.Name,
		URLs:      urls,
		Conf:      conf,
	})
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeJSON(w, http.StatusCreated, toChannelDict(ch))
}

func (h *frontHandler) loadChannel(w http.ResponseWriter, r *http.Request) (*store.Channel, bool) {
	ch, err := h.db.ChannelByCode(r.Context(), chi.URLParam(r, "code"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "未找到")
		} else {
			jsonError(w, http.StatusInternalServerError, "服务器错误")
		}
		return nil, false
	}
	project, err := h.db.Project(r.Context())
	if err != nil || ch.ProjectID != project.ID {
		jsonError(w, http.StatusForbidden, "无权访问")
		return nil, false
	}
	return ch, true
}

func (h *frontHandler) updateChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok := h.loadChannel(w, r)
	if !ok {
		return
	}
	b, ok := decodeChannel(w, r)
	if !ok {
		return
	}
	if b.Name != "" {
		ch.Name = b.Name
	}
	if len(b.URLs) > 0 {
		urls, err := cleanURLs(b.URLs)
		if err != nil {
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		ch.URLs = urls
	}
	if b.Up != nil {
		ch.Conf.Up = *b.Up
	}
	if b.Down != nil {
		ch.Conf.Down = *b.Down
	}
	if b.Disabled != nil {
		ch.Disabled = *b.Disabled
		if !*b.Disabled {
			ch.LastError = ""
		}
	}
	if err := h.db.UpdateChannel(r.Context(), ch); err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeJSON(w, http.StatusOK, toChannelDict(ch))
}

func (h *frontHandler) deleteChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok := h.loadChannel(w, r)
	if !ok {
		return
	}
	if err := h.db.DeleteChannel(r.Context(), ch.ID); err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testChannel sends a probe so a misconfigured URL fails in front of the user,
// not during the next outage.
func (h *frontHandler) testChannel(w http.ResponseWriter, r *http.Request) {
	ch, ok := h.loadChannel(w, r)
	if !ok {
		return
	}
	if h.tester == nil {
		jsonError(w, http.StatusServiceUnavailable, "通知器未就绪")
		return
	}
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
	subject := &engine.Check{Name: "测试渠道", Code: ch.Code, Kind: engine.KindSimple,
		Timeout: engine.DefaultTimeout, Grace: engine.DefaultGrace, Status: engine.StatusUp}
	if len(checks) > 0 {
		subject = checks[0]
	}

	if err := h.tester.TestMessage(ch, subject, engine.StatusUp); err != nil {
		jsonError(w, http.StatusBadGateway, errTextForUI(err))
		return
	}
	writeText(w, http.StatusOK, "OK")
}

func errTextForUI(err error) string {
	if err == nil {
		return ""
	}
	msg := strings.ReplaceAll(err.Error(), "\n", "; ")
	if len(msg) > 300 {
		msg = msg[:300]
	}
	return msg
}
