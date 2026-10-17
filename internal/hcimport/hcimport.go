// Package hcimport turns a healthchecks export into 飞檄 check definitions. The
// input is whatever GET /api/v3/checks/ returns on the source instance, read-only
// fields and nulls included, so nothing here shares decodeSpec's strict rules.
package hcimport

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yufei/feixi/internal/cronex"
	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/store"
	"github.com/yufei/feixi/internal/tzutil"
)

// 导入直接写库，绕开了 API 层的 validate()，所以同一套界限在这里也得拦一次。
const (
	minSeconds = 60
	maxSeconds = 31536000
	maxName    = 100
	maxSlug    = 100
	maxKeyword = 200
	maxDesc    = 20000
)

var slugPattern = regexp.MustCompile(`^[a-z0-9/_-]*$`)

// Item is one exported check plus what the conversion did to it.
type Item struct {
	New     *store.NewCheck
	Problem string
	Notes   []string
	Name    string
}

// Importable reports whether the item can be written.
func (i Item) Importable() bool { return i.Problem == "" && i.New != nil }

type rawCheck struct {
	UUID              *string `json:"uuid"`
	Name              *string `json:"name"`
	Slug              *string `json:"slug"`
	Tags              *string `json:"tags"`
	Desc              *string `json:"desc"`
	Timeout           *int    `json:"timeout"`
	Grace             *int    `json:"grace"`
	Schedule          *string `json:"schedule"`
	TZ                *string `json:"tz"`
	Methods           *string `json:"methods"`
	ManualResume      *bool   `json:"manual_resume"`
	StartKW           *string `json:"start_kw"`
	SuccessKW         *string `json:"success_kw"`
	FailureKW         *string `json:"failure_kw"`
	Subject           *string `json:"subject"`
	SubjectFail       *string `json:"subject_fail"`
	FilterHTTPBody    *bool   `json:"filter_http_body"`
	FilterDefaultFail *bool   `json:"filter_default_fail"`
	FilterSubject     *bool   `json:"filter_subject"`
	FilterBody        *bool   `json:"filter_body"`
	Channels          *string `json:"channels"`
	Status            *string `json:"status"`
}

// Parse decodes the export and converts every check. It only returns an error
// for a file that is not an export at all; a bad *row* comes back as
// Item.Problem so the caller can report the whole batch instead of aborting.
func Parse(data []byte, projectID int64) ([]Item, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("导入文件是空的")
	}

	var raws []rawCheck
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &raws); err != nil {
			return nil, fmt.Errorf("解析检查项数组失败：%s", trimJSONError(err))
		}
	} else {
		var env struct {
			Checks []rawCheck `json:"checks"`
		}
		if err := json.Unmarshal(trimmed, &env); err != nil {
			return nil, fmt.Errorf(`不是 healthchecks 导出的 JSON（应为 {"checks":[...]} 或裸数组）：%s`,
				trimJSONError(err))
		}
		raws = env.Checks
	}
	if len(raws) == 0 {
		return nil, errors.New(`文件里没有检查项（"checks" 是空的）`)
	}

	out := make([]Item, 0, len(raws))
	for i := range raws {
		out = append(out, convert(&raws[i], projectID))
	}
	return out, nil
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func boolean(p *bool) bool {
	return p != nil && *p
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func trimJSONError(err error) string {
	msg := strings.TrimPrefix(err.Error(), "json: ")
	if len(msg) > 120 {
		msg = msg[:120]
	}
	return msg
}

func convert(r *rawCheck, projectID int64) Item {
	it := Item{Name: str(r.Name), New: &store.NewCheck{ProjectID: projectID}}
	// 跳过的条目不该再挂着「地址会另生成」这类提示，它根本不会进库。
	fail := func(format string, args ...any) Item {
		it.New, it.Problem, it.Notes = nil, fmt.Sprintf(format, args...), nil
		return it
	}

	in := it.New
	name := strings.TrimSpace(str(r.Name))
	if name == "" {
		name = "（未命名）"
	}
	it.Name = name
	if len([]rune(str(r.Name))) > maxName {
		return fail("name 超过 %d 字符", maxName)
	}
	slug := strings.TrimSpace(str(r.Slug))
	if len(slug) > maxSlug || !slugPattern.MatchString(slug) {
		return fail(`字段 "slug" 只能包含小写字母、数字、/、-、_`)
	}
	in.Name, in.Slug = strings.TrimSpace(str(r.Name)), slug

	if desc := str(r.Desc); len(desc) > maxDesc {
		return fail("desc 超过 %d 字符", maxDesc)
	}
	in.Desc = str(r.Desc)
	in.Tags = strings.Join(strings.Fields(str(r.Tags)), " ")

	// 保留源 UUID 是这次迁移的全部意义：脚本只需要改域名，地址里的 UUID 不动。
	if r.UUID == nil {
		it.Notes = append(it.Notes, "源 JSON 没有 uuid（多半是用只读 key 导出的），已另生成 ping 地址")
	} else if u, err := uuid.Parse(strings.TrimSpace(*r.UUID)); err != nil {
		return fail("uuid %q 不是合法 UUID", *r.UUID)
	} else {
		in.Code = u.String()
	}

	schedule := strings.TrimSpace(str(r.Schedule))
	exprZone := ""
	if schedule != "" {
		in.Kind = engine.KindCron
		if isFiveField(schedule) {
			if err := cronex.Validate(schedule); err != nil {
				return fail("%s", err)
			}
			in.Schedule = strings.Join(strings.Fields(schedule), " ")
		} else {
			cron, zone, err := toCron(schedule)
			if err != nil {
				return fail("OnCalendar %q 降不成 5 段 cron：%s", compact(schedule), err)
			}
			in.Schedule = cron
			exprZone = zone
			it.Notes = append(it.Notes, fmt.Sprintf("OnCalendar %q 已降段成 %q", compact(schedule), cron))
		}
	} else {
		in.Kind = engine.KindSimple
	}

	for _, pair := range []struct {
		field string
		val   *int
		dst   *time.Duration
		def   time.Duration
	}{
		{"timeout", r.Timeout, &in.Timeout, engine.DefaultTimeout},
		{"grace", r.Grace, &in.Grace, engine.DefaultGrace},
	} {
		if pair.val == nil {
			*pair.dst = pair.def
			continue
		}
		if *pair.val < minSeconds || *pair.val > maxSeconds {
			return fail("字段 %q 需要在 %d 到 %d 秒之间，收到 %d", pair.field, minSeconds, maxSeconds, *pair.val)
		}
		*pair.dst = time.Duration(*pair.val) * time.Second
	}

	// 表达式自带的时区优先，跟源实例算 next_ping 的口径一致。
	tz := "UTC"
	if r.TZ != nil && *r.TZ != "" {
		if !tzutil.Valid(*r.TZ) {
			return fail("时区 %q 认不出来", *r.TZ)
		}
		tz = *r.TZ
	}
	if exprZone != "" && exprZone != tz {
		if str(r.TZ) != "" {
			it.Notes = append(it.Notes, fmt.Sprintf("时区取表达式自带的 %s，不是 tz 字段的 %s", exprZone, *r.TZ))
		} else {
			it.Notes = append(it.Notes, fmt.Sprintf("时区取表达式自带的 %s", exprZone))
		}
		tz = exprZone
	}
	in.TZ = tz

	methods := str(r.Methods)
	if methods != "" && methods != "POST" {
		return fail(`字段 "methods" 只能是 "" 或 "POST"，收到 %q`, methods)
	}
	in.Methods = methods
	in.ManualResum = boolean(r.ManualResume)
	in.FilterHTTP = boolean(r.FilterHTTPBody)
	in.FilterFail = boolean(r.FilterDefaultFail)

	for _, kw := range []*string{r.StartKW, r.SuccessKW, r.FailureKW, r.Subject, r.SubjectFail} {
		if len(str(kw)) > maxKeyword {
			return fail("关键字字段最长 %d 字符", maxKeyword)
		}
	}
	in.StartKW = str(r.StartKW)
	in.SuccessKW = firstNonEmpty(str(r.SuccessKW), str(r.Subject))
	in.FailureKW = firstNonEmpty(str(r.FailureKW), str(r.SubjectFail))
	if str(r.Subject) != "" {
		it.Notes = append(it.Notes, "subject 已并入 success_kw（飞檄只按请求体关键字匹配）")
	}
	if str(r.SubjectFail) != "" {
		it.Notes = append(it.Notes, "subject_fail 已并入 failure_kw（飞檄只按请求体关键字匹配）")
	}
	if boolean(r.FilterSubject) || boolean(r.FilterBody) {
		it.Notes = append(it.Notes, "filter_subject/filter_body 未带过来，它们只对邮件通道有意义")
	}

	if n := countChannels(str(r.Channels)); n > 0 {
		it.Notes = append(it.Notes, fmt.Sprintf("源渠道 %d 个未迁移（UUID 不通用），请在检查项详情里手动订阅", n))
	}
	if str(r.Status) == "paused" {
		it.Notes = append(it.Notes, "源是暂停态，导入后回到「新建」，收到第一次 ping 才算正常")
	}
	return it
}

func countChannels(raw string) int {
	if raw == "" {
		return 0
	}
	return len(strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' }))
}

func isFiveField(schedule string) bool {
	return !strings.Contains(schedule, "\n") && len(strings.Fields(schedule)) == 5
}

func compact(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
