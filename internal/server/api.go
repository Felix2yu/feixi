package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/yufei/feixi/internal/config"
	"github.com/yufei/feixi/internal/cronex"
	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/format"
	"github.com/yufei/feixi/internal/store"
	"github.com/yufei/feixi/internal/tzutil"
)

type apiHandler struct {
	db     *store.DB
	cfg    *config.Config
	badges *badgeHandler
}

func (h *apiHandler) mount(r chi.Router, version string) {
	v, _ := strconv.Atoi(version[1:])

	r.Get("/status/", h.status)
	r.Get("/checks/", func(w http.ResponseWriter, req *http.Request) { h.listChecks(w, req, v) })
	r.Post("/checks/", func(w http.ResponseWriter, req *http.Request) { h.createCheck(w, req, v) })
	r.Get("/channels/", h.channels)
	h.mountNotifications(r)

	r.Route("/checks/{code}", func(r chi.Router) {
		r.Get("/", func(w http.ResponseWriter, req *http.Request) { h.getCheck(w, req, v) })
		r.Post("/", func(w http.ResponseWriter, req *http.Request) { h.updateCheck(w, req, v) })
		r.Delete("/", func(w http.ResponseWriter, req *http.Request) { h.deleteCheck(w, req, v) })
		r.Post("/pause", func(w http.ResponseWriter, req *http.Request) { h.pause(w, req, v) })
		r.Post("/resume", func(w http.ResponseWriter, req *http.Request) { h.resume(w, req, v) })
		r.Get("/pings/", func(w http.ResponseWriter, req *http.Request) { h.pings(w, req, v) })
		r.Get("/pings/{n}/body", func(w http.ResponseWriter, req *http.Request) { h.pingBody(w, req) })
		r.Get("/flips/", h.flips)
	})
}

func jsonError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

// status proves the database answers, like the original's "SELECT 1".
func (h *apiHandler) status(w http.ResponseWriter, r *http.Request) {
	var one int
	if err := h.db.Read.QueryRowContext(r.Context(), `SELECT 1`).Scan(&one); err != nil {
		http.Error(w, "DOWN", http.StatusInternalServerError)
		return
	}
	writeText(w, http.StatusOK, "OK")
}

func writeText(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	io.WriteString(w, body)
}

// spec is the management API request body. Unknown fields and nulls are
// rejected, matching the original's strict pydantic model.
type spec struct {
	Name              *string   `json:"name"`
	Slug              *string   `json:"slug"`
	Tags              *string   `json:"tags"`
	Desc              *string   `json:"desc"`
	Timeout           *int      `json:"timeout"`
	Grace             *int      `json:"grace"`
	Schedule          *string   `json:"schedule"`
	TZ                *string   `json:"tz"`
	Channels          *string   `json:"channels"`
	Unique            *[]string `json:"unique"`
	Methods           *string   `json:"methods"`
	ManualResume      *bool     `json:"manual_resume"`
	StartKW           *string   `json:"start_kw"`
	SuccessKW         *string   `json:"success_kw"`
	FailureKW         *string   `json:"failure_kw"`
	Subject           *string   `json:"subject"`
	SubjectFail       *string   `json:"subject_fail"`
	FilterHTTPBody    *bool     `json:"filter_http_body"`
	FilterDefaultFail *bool     `json:"filter_default_fail"`
	FilterSubject     *bool     `json:"filter_subject"`
	FilterBody        *bool     `json:"filter_body"`
}

var slugPattern = regexp.MustCompile(`^[a-z0-9/_-]*$`)

const (
	minDuration = 60
	maxDuration = 31536000
)

func decodeSpec(r *http.Request) (*spec, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		return nil, errors.New("JSON 验证错误：请求体读取失败")
	}
	if len(bytes.TrimSpace(body)) == 0 {
		body = []byte("{}")
	}

	// Reject nulls first: pydantic treats them as type errors, and silently
	// mapping null to the default would differ from the original.
	var raw map[string]any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&raw); err != nil {
		return nil, errors.New("JSON 验证错误：不是合法的 JSON")
	}
	for k, v := range raw {
		if v == nil {
			return nil, fmt.Errorf("JSON 验证错误：字段 %q 不能为 null", k)
		}
	}

	s := &spec{}
	strict := json.NewDecoder(bytes.NewReader(body))
	strict.DisallowUnknownFields()
	if err := strict.Decode(s); err != nil {
		var typ *json.UnmarshalTypeError
		if errors.As(err, &typ) && typ.Field != "" {
			return nil, fmt.Errorf("JSON 验证错误：字段 %q 类型不对", typ.Field)
		}
		var unknown *json.UnmarshalTypeError
		_ = unknown
		return nil, errors.New("JSON 验证错误：" + trimJSONError(err))
	}
	return s, nil
}

func trimJSONError(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "json: "); i == 0 {
		msg = msg[6:]
	}
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return msg
}

func (s *spec) validate() error {
	if s.Name != nil && len(*s.Name) > 100 {
		return errors.New("JSON 验证错误：字段 \"name\" 太长了")
	}
	if s.Slug != nil {
		if len(*s.Slug) > 100 || !slugPattern.MatchString(*s.Slug) {
			return errors.New("JSON 验证错误：字段 \"slug\" 只能包含小写字母、数字、-、_")
		}
	}
	if s.Desc != nil && len(*s.Desc) > 20000 {
		return errors.New("JSON 验证错误：字段 \"desc\" 太长了")
	}
	for _, pair := range []struct {
		name string
		val  *int
	}{
		{"timeout", s.Timeout}, {"grace", s.Grace},
	} {
		if pair.val != nil && (*pair.val < minDuration || *pair.val > maxDuration) {
			return fmt.Errorf("JSON 验证错误：字段 %q 需要在 %d 到 %d 秒之间",
				pair.name, minDuration, maxDuration)
		}
	}
	if s.Schedule != nil {
		if len(*s.Schedule) > 100 {
			return errors.New("JSON 验证错误：字段 \"schedule\" 太长了")
		}
		if err := validateSchedule(*s.Schedule); err != nil {
			return fmt.Errorf("JSON 验证错误：%s", err)
		}
	}
	if s.TZ != nil && !tzValid(*s.TZ) {
		return errors.New("JSON 验证错误：not a valid timezone")
	}
	for _, kw := range []*string{s.StartKW, s.SuccessKW, s.FailureKW, s.Subject, s.SubjectFail} {
		if kw != nil && len(*kw) > 200 {
			return errors.New("JSON 验证错误：关键字字段最长 200 字符")
		}
	}
	if s.Methods != nil && *s.Methods != "" && *s.Methods != "POST" {
		return errors.New("JSON 验证错误：字段 \"methods\" 只能是 \"\" 或 \"POST\"")
	}
	if s.FilterSubject != nil && *s.FilterSubject {
		return errors.New("JSON 验证错误：filter_subject 需要邮件通道，本版本不支持")
	}
	if s.FilterBody != nil && *s.FilterBody {
		return errors.New("JSON 验证错误：filter_body 需要邮件通道，本版本不支持")
	}
	if s.Unique != nil {
		for _, u := range *s.Unique {
			switch u {
			case "name", "slug", "tags", "timeout", "grace":
			default:
				return fmt.Errorf("JSON 验证错误：unique 不支持 %q", u)
			}
		}
	}
	return nil
}

// resolveKind mirrors guess_kind, minus oncalendar: five whitespace-separated
// fields on one line is cron, anything else is refused.
func validateSchedule(schedule string) error {
	trimmed := strings.TrimSpace(schedule)
	if strings.Contains(trimmed, "\n") {
		return errors.New("not a valid cron expression（本版本不支持 OnCalendar 表达式）")
	}
	if len(strings.Fields(trimmed)) != 5 {
		if strings.Contains(trimmed, ":") {
			// A time component means systemd OnCalendar syntax, which this
			// build does not parse.
			return errors.New("not a valid cron expression（本版本不支持 OnCalendar 表达式）")
		}
		return errors.New("not a valid cron expression")
	}
	return cronex.Validate(trimmed)
}

func tzValid(name string) bool {
	if name == "" {
		return true
	}
	return tzutil.Valid(name)
}

// applySpec writes the provided fields onto the check and reports which ones
// changed, so an update only saves the touched columns.
func (h *apiHandler) applySpec(ctx context.Context, c *engine.Check, s *spec, v int, project *store.Project) error {
	changed := map[string]bool{}

	if s.Name != nil {
		c.Name = *s.Name
		changed["name"] = true
	}
	if s.Slug != nil {
		c.Slug = *s.Slug
		changed["slug"] = true
	} else if c.Slug == "" && v <= 2 && c.Name != "" {
		// API v1 and v2 derive a slug from the name automatically.
		c.Slug = slugify(c.Name)
		changed["slug"] = true
	}
	if s.Tags != nil {
		c.Tags = strings.Join(strings.Fields(*s.Tags), " ")
		changed["tags"] = true
	}
	if s.Desc != nil {
		c.Description = *s.Desc
		changed["description"] = true
	}
	if s.Timeout != nil {
		c.Timeout = time.Duration(*s.Timeout) * time.Second
		c.Kind = engine.KindSimple
		c.Schedule = ""
		changed["timeout_us"] = true
		changed["kind"] = true
	}
	if s.Schedule != nil {
		c.Schedule = strings.TrimSpace(*s.Schedule)
		c.Kind = engine.KindCron
		c.Timeout = engine.DefaultTimeout
		changed["schedule"] = true
		changed["kind"] = true
	}
	if s.TZ != nil {
		c.TZ = *s.TZ
		changed["tz"] = true
	}
	if s.Grace != nil {
		c.Grace = time.Duration(*s.Grace) * time.Second
		changed["grace_us"] = true
	}
	if s.Methods != nil {
		c.Methods = *s.Methods
		changed["methods"] = true
	}
	if s.ManualResume != nil {
		c.ManualResume = *s.ManualResume
		changed["manual_resume"] = true
	}
	if s.StartKW != nil {
		c.StartKW = *s.StartKW
		changed["start_kw"] = true
	}
	if s.SuccessKW != nil {
		c.SuccessKW = *s.SuccessKW
		changed["success_kw"] = true
	}
	if s.FailureKW != nil {
		c.FailureKW = *s.FailureKW
		changed["failure_kw"] = true
	}
	// subject and subject_fail are deprecated aliases of the keyword fields.
	if s.Subject != nil {
		c.SuccessKW = *s.Subject
		changed["success_kw"] = true
	}
	if s.SubjectFail != nil {
		c.FailureKW = *s.SubjectFail
		changed["failure_kw"] = true
	}
	if s.FilterHTTPBody != nil {
		c.FilterHTTPBody = *s.FilterHTTPBody
		changed["filter_http_body"] = true
	}
	if s.FilterDefaultFail != nil {
		c.FilterDefaultFail = *s.FilterDefaultFail
		changed["filter_default_fail"] = true
	}

	// A schedule needs a timezone to be evaluated; default it like the original.
	if c.Kind == engine.KindCron && c.TZ == "" {
		c.TZ = "UTC"
	}

	if err := recomputeDeadline(c, h.db.Sched); err != nil {
		return err
	}
	_ = changed
	return h.db.UpdateCheck(ctx, c)
}

func recomputeDeadline(c *engine.Check, sch engine.Scheduler) error {
	at, ok, err := engine.GoingDownAfter(c, sch)
	if err != nil {
		return err
	}
	if ok {
		c.AlertAfter = &at
	} else {
		c.AlertAfter = nil
	}
	return nil
}

func slugify(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('-')
		}
	}
	out := b.String()
	if len(out) > 100 {
		out = out[:100]
	}
	return out
}

func (h *apiHandler) resolveChannelIDs(ctx context.Context, projectID int64, raw *string) ([]int64, bool, error) {
	if raw == nil {
		return nil, false, nil
	}
	all, err := h.db.AllChannelIDs(ctx, projectID)
	if err != nil {
		return nil, false, err
	}
	switch *raw {
	case "*":
		return all, true, nil
	case "":
		return []int64{}, true, nil
	}

	want := strings.FieldsFunc(*raw, func(r rune) bool { return r == ',' || r == ' ' })
	ids := make([]int64, 0, len(want))
	for _, code := range want {
		ch, err := h.db.ChannelByCode(ctx, code)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				return nil, false, fmt.Errorf("找不到渠道 %q", code)
			}
			return nil, false, err
		}
		if ch.ProjectID != projectID {
			return nil, false, fmt.Errorf("渠道 %q 不属于这个项目", code)
		}
		ids = append(ids, ch.ID)
	}
	return ids, true, nil
}

func (h *apiHandler) channelCodesFor(ctx context.Context, checkID int64) string {
	ids, err := h.db.CheckChannels(ctx, checkID)
	if err != nil {
		return ""
	}
	var codes []string
	for _, id := range ids {
		ch, err := h.db.ChannelByID(ctx, id)
		if err == nil {
			codes = append(codes, ch.Code)
		}
	}
	return strings.Join(codes, ",")
}

func (h *apiHandler) listChecks(w http.ResponseWriter, r *http.Request, v int) {
	project, err := h.db.Project(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	filter := store.CheckFilter{}
	query := r.URL.Query()
	for _, tag := range query["tag"] {
		filter.Tags = append(filter.Tags, tag)
	}
	if slug := query.Get("slug"); slug != "" {
		filter.Slugs = append(filter.Slugs, slug)
	}

	checks, err := h.db.ListChecks(r.Context(), project.ID, filter)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	out := make([]CheckDict, 0, len(checks))
	for _, c := range checks {
		if len(filter.Tags) > 0 && !hasAllTags(c.Tags, filter.Tags) {
			continue
		}
		d, err := h.checkDict(r.Context(), c, v)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "服务器错误")
			return
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"checks": out})
}

// hasAllTags is the precise second pass over the SQL approximation.
func hasAllTags(tags string, want []string) bool {
	have := map[string]bool{}
	for _, t := range strings.Fields(tags) {
		have[t] = true
	}
	for _, t := range want {
		if !have[t] {
			return false
		}
	}
	return true
}

func (h *apiHandler) createCheck(w http.ResponseWriter, r *http.Request, v int) {
	project, err := h.db.Project(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	s, err := decodeSpec(r)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validate(); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	channelIDs, explicit, err := h.resolveChannelIDs(r.Context(), project.ID, s.Channels)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !explicit {
		channelIDs, _ = h.db.AllChannelIDs(r.Context(), project.ID)
	}

	existing, err := h.lookupByUnique(r.Context(), project.ID, s)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	if existing != nil {
		if err := h.applySpec(r.Context(), existing, s, v, project); err != nil {
			h.writeSpecError(w, err)
			return
		}
		if explicit {
			if err := h.db.SetCheckChannels(r.Context(), existing.ID, channelIDs); err != nil {
				jsonError(w, http.StatusInternalServerError, "服务器错误")
				return
			}
		}
		d, err := h.checkDict(r.Context(), existing, v)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "服务器错误")
			return
		}
		writeJSON(w, http.StatusOK, d)
		return
	}

	kind := engine.KindSimple
	if s.Schedule != nil {
		kind = engine.KindCron
	}
	timeout, grace := engine.DefaultTimeout, engine.DefaultGrace
	if s.Timeout != nil {
		timeout = time.Duration(*s.Timeout) * time.Second
	}
	if s.Grace != nil {
		grace = time.Duration(*s.Grace) * time.Second
	}
	schedule := ""
	if s.Schedule != nil {
		schedule = strings.TrimSpace(*s.Schedule)
	}
	tz := "UTC"
	if s.TZ != nil {
		tz = *s.TZ
	}
	manual, filterHTTP, filterFail := false, false, false
	if s.ManualResume != nil {
		manual = *s.ManualResume
	}
	if s.FilterHTTPBody != nil {
		filterHTTP = *s.FilterHTTPBody
	}
	if s.FilterDefaultFail != nil {
		filterFail = *s.FilterDefaultFail
	}
	methods := ""
	if s.Methods != nil {
		methods = *s.Methods
	}
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	tags := str(s.Tags)
	if tags != "" {
		tags = strings.Join(strings.Fields(tags), " ")
	}

	c, err := h.db.CreateCheck(r.Context(), &store.NewCheck{
		ProjectID:   project.ID,
		Name:        str(s.Name),
		Slug:        str(s.Slug),
		Tags:        tags,
		Desc:        str(s.Desc),
		Kind:        kind,
		Timeout:     timeout,
		Grace:       grace,
		Schedule:    schedule,
		TZ:          tz,
		Methods:     methods,
		ManualResum: manual,
		FilterHTTP:  filterHTTP,
		FilterFail:  filterFail,
		StartKW:     str(s.StartKW),
		SuccessKW:   firstNonEmpty(str(s.SuccessKW), str(s.Subject)),
		FailureKW:   firstNonEmpty(str(s.FailureKW), str(s.SubjectFail)),
		ChannelIDs:  channelIDs,
	})
	if err != nil {
		if errors.Is(err, store.ErrTooManyChecks) {
			jsonError(w, http.StatusForbidden, "检查项数量已达上限")
			return
		}
		jsonError(w, http.StatusBadRequest, "创建失败："+err.Error())
		return
	}

	d, err := h.checkDict(r.Context(), c, v)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

// writeSpecError reports a rejected spec: only the schedule and timezone can
// fail here, and both are the client's fault.
func (h *apiHandler) writeSpecError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrTooManyChecks) {
		jsonError(w, http.StatusForbidden, "检查项数量已达上限")
		return
	}
	jsonError(w, http.StatusBadRequest, "JSON 验证错误："+err.Error())
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// lookupByUnique implements the "unique" list: POST acts as an upsert when the
// listed fields identify an existing check.
func (h *apiHandler) lookupByUnique(ctx context.Context, projectID int64, s *spec) (*engine.Check, error) {
	if s.Unique == nil || len(*s.Unique) == 0 {
		return nil, nil
	}
	checks, err := h.db.ListChecks(ctx, projectID, store.CheckFilter{})
	if err != nil {
		return nil, err
	}
	for _, c := range checks {
		if matchesUnique(c, s, *s.Unique) {
			return c, nil
		}
	}
	return nil, nil
}

func matchesUnique(c *engine.Check, s *spec, fields []string) bool {
	str := func(p *string) string {
		if p == nil {
			return ""
		}
		return *p
	}
	for _, f := range fields {
		switch f {
		case "name":
			if s.Name == nil || c.Name != *s.Name {
				return false
			}
		case "slug":
			if s.Slug == nil || c.Slug != *s.Slug {
				return false
			}
		case "tags":
			want := strings.Join(strings.Fields(str(s.Tags)), " ")
			if s.Tags == nil || c.Tags != want {
				return false
			}
		case "timeout":
			if s.Timeout == nil || format.DurationSeconds(c.Timeout) != *s.Timeout {
				return false
			}
		case "grace":
			if s.Grace == nil || format.DurationSeconds(c.Grace) != *s.Grace {
				return false
			}
		}
	}
	return true
}

func (h *apiHandler) findCheck(w http.ResponseWriter, r *http.Request) (*engine.Check, *store.Project, bool) {
	project, err := h.db.Project(r.Context())
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return nil, nil, false
	}
	c, err := h.db.CheckByCode(r.Context(), chi.URLParam(r, "code"))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			jsonError(w, http.StatusNotFound, "未找到")
		} else {
			jsonError(w, http.StatusInternalServerError, "服务器错误")
		}
		return nil, nil, false
	}
	if c.ProjectID != project.ID {
		jsonError(w, http.StatusForbidden, "无权访问")
		return nil, nil, false
	}
	return c, project, true
}

func (h *apiHandler) getCheck(w http.ResponseWriter, r *http.Request, v int) {
	c, _, ok := h.findCheck(w, r)
	if !ok {
		return
	}
	d, err := h.checkDict(r.Context(), c, v)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *apiHandler) updateCheck(w http.ResponseWriter, r *http.Request, v int) {
	c, project, ok := h.findCheck(w, r)
	if !ok {
		return
	}
	s, err := decodeSpec(r)
	if err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validate(); err != nil {
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}

	before := *c
	if err := h.applySpec(r.Context(), c, s, v, project); err != nil {
		h.writeSpecError(w, err)
		return
	}

	channelIDs, explicit, err := h.resolveChannelIDs(r.Context(), project.ID, s.Channels)
	if err != nil {
		// The spec was already written; restore it, matching the original's
		// "bad channel means no change" behaviour.
		*c = before
		h.db.UpdateCheck(r.Context(), c)
		jsonError(w, http.StatusBadRequest, err.Error())
		return
	}
	if explicit {
		if err := h.db.SetCheckChannels(r.Context(), c.ID, channelIDs); err != nil {
			jsonError(w, http.StatusInternalServerError, "服务器错误")
			return
		}
	}

	d, err := h.checkDict(r.Context(), c, v)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *apiHandler) deleteCheck(w http.ResponseWriter, r *http.Request, v int) {
	c, _, ok := h.findCheck(w, r)
	if !ok {
		return
	}
	if _, err := h.db.RenameAndDelete(r.Context(), c.ID); err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *apiHandler) pause(w http.ResponseWriter, r *http.Request, v int) {
	c, _, ok := h.findCheck(w, r)
	if !ok {
		return
	}
	if c.Status == engine.StatusPaused {
		d, err := h.checkDict(r.Context(), c, v)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "服务器错误")
			return
		}
		writeJSON(w, http.StatusOK, d)
		return
	}

	updated, _, err := h.db.Pause(r.Context(), c.ID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	d, err := h.checkDict(r.Context(), updated, v)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (h *apiHandler) resume(w http.ResponseWriter, r *http.Request, v int) {
	c, _, ok := h.findCheck(w, r)
	if !ok {
		return
	}
	if c.Status != engine.StatusPaused {
		jsonError(w, http.StatusConflict, "检查项未暂停")
		return
	}

	updated, _, err := h.db.Resume(r.Context(), c.ID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	d, err := h.checkDict(r.Context(), updated, v)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	writeJSON(w, http.StatusOK, d)
}

type pingDict struct {
	Type       string   `json:"type"`
	Date       string   `json:"date"`
	N          int64    `json:"n"`
	Scheme     string   `json:"scheme"`
	RemoteAddr string   `json:"remote_addr"`
	Method     string   `json:"method"`
	UA         string   `json:"ua"`
	Rid        *string  `json:"rid"`
	BodyURL    *string  `json:"body_url"`
	Duration   *float64 `json:"duration,omitempty"`
}

func (h *apiHandler) pings(w http.ResponseWriter, r *http.Request, v int) {
	c, _, ok := h.findCheck(w, r)
	if !ok {
		return
	}
	limit := h.cfg.PingLogLimit
	if limit > 1000 {
		limit = 1000
	}
	pings, err := h.db.ListPings(r.Context(), c.ID, limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}

	out := make([]pingDict, 0, len(pings))
	for _, p := range pings {
		d := pingDict{
			Type:       p.Kind,
			Date:       format.ISODateTime(p.Created),
			N:          p.N,
			Scheme:     p.Scheme,
			RemoteAddr: p.RemoteAddr,
			Method:     p.Method,
			UA:         p.UA,
			Rid:        p.Rid,
		}
		if d.Type == "" {
			d.Type = "success"
		}
		if len(p.Body) > 0 {
			u := fmt.Sprintf("%s/api/v%d/checks/%s/pings/%d/body", h.cfg.SiteRoot, v, c.Code, p.N)
			d.BodyURL = &u
		}
		if p.Duration != nil {
			secs := math.Round(p.Duration.Seconds()*100) / 100
			d.Duration = &secs
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"pings": out})
}

func (h *apiHandler) pingBody(w http.ResponseWriter, r *http.Request) {
	c, _, ok := h.findCheck(w, r)
	if !ok {
		return
	}
	n, err := strconv.ParseInt(chi.URLParam(r, "n"), 10, 64)
	if err != nil {
		jsonError(w, http.StatusNotFound, "未找到")
		return
	}
	threshold := c.NPings - int64(h.cfg.PingLogLimit)
	if n <= threshold {
		jsonError(w, http.StatusNotFound, "未找到")
		return
	}
	body, err := h.db.PingBody(r.Context(), c.ID, n)
	if err != nil {
		jsonError(w, http.StatusNotFound, "未找到")
		return
	}
	if len(body) == 0 {
		jsonError(w, http.StatusNotFound, "未找到")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(body)
}

func (h *apiHandler) flips(w http.ResponseWriter, r *http.Request) {
	c, _, ok := h.findCheck(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	start, err := parseTimeParam(query.Get("start"))
	if err != nil {
		writeText(w, http.StatusBadRequest, "无效的 start 参数")
		return
	}
	end, err := parseTimeParam(query.Get("end"))
	if err != nil {
		writeText(w, http.StatusBadRequest, "无效的 end 参数")
		return
	}
	if secs := query.Get("seconds"); secs != "" {
		n, err := strconv.Atoi(secs)
		if err != nil || n < 1 {
			writeText(w, http.StatusBadRequest, "无效的 seconds 参数")
			return
		}
		threshold := time.Now().UTC().Add(-time.Duration(n) * time.Second)
		if start == nil || threshold.After(*start) {
			start = &threshold
		}
	}

	events, err := h.db.Flips(r.Context(), c.ID, start, end)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, "服务器错误")
		return
	}
	// Newest first, like the original's order_by("-id").
	out := make([]map[string]any, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		up := 0
		if events[i].Up {
			up = 1
		}
		out = append(out, map[string]any{
			"timestamp": format.ISOString(&events[i].Timestamp),
			"up":        up,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"flips": out})
}

func parseTimeParam(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.Parse(layout, raw); err == nil {
			utc := t.UTC()
			return &utc, nil
		}
	}
	return nil, errors.New("bad time")
}

func (h *apiHandler) channels(w http.ResponseWriter, r *http.Request) {
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
	out := make([]map[string]string, 0, len(channels))
	for _, ch := range channels {
		out = append(out, map[string]string{
			"id":   ch.Code,
			"name": ch.Name,
			"kind": "apprise",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"channels": out})
}
