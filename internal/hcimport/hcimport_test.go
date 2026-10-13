package hcimport

import (
	"strings"
	"testing"
	"time"

	"github.com/yufei/feixi/internal/engine"
)

// sample 是 GET /api/v3/checks/ 的原文形态：只读字段、null、源实例的渠道 UUID
// 都会一起进来。
const sample = `{"checks":[
 {"name":"备份数据库","uuid":"3d2f8bd8-cb1a-4a6c-b0a2-6b8e0f8b7c11","slug":"db-backup",
  "tags":"db  nightly","desc":"每晚全量","grace":600,"timeout":3600,
  "status":"up","n_pings":12,"last_ping":null,"next_ping":null,
  "badge_url":"https://hc.example.org/badge.svg","ping_url":"https://hc.example.org/ping/3d2f8bd8",
  "update_url":"https://hc.example.org/api/v3/checks/3d2f8bd8/","channels":"11111111-1111-1111-1111-111111111111,22222222-2222-2222-2222-222222222222",
  "methods":"POST","manual_resume":true,"filter_http_body":true,"success_kw":"OK","start_kw":"start"},
 {"name":"每天六点","grace":3600,"schedule":"0 18 * * *","tz":"Asia/Shanghai"},
 {"name":"工作日每4小时","schedule":"Mon-Fri *-*-* 0/4:00","grace":600,"tz":"UTC"},
 {"name":"月末清理","schedule":"*-*-~1 12:00","grace":600},
 {"name":"带年份","schedule":"2026-03-01 12:00","grace":600},
 {"name":"两个时间点","schedule":"*-*-* 08:00\n*-*-* 20:00","grace":600},
 {"name":"亚分钟","schedule":"*-*-* 12:00:15","grace":600},
 {"name":"slug 歪","slug":"Bad Slug","grace":600,"timeout":600},
 {"name":"宽限太短","grace":10,"timeout":3600},
 {"name":"时区歪","schedule":"0 18 * * *","tz":"Mars/Olympus","grace":600},
 {"name":"邮件主题","grace":60,"timeout":60,"subject":"done","subject_fail":"failed","filter_subject":true,"filter_body":true},
 {"name":"暂停中的","timeout":120,"grace":60,"status":"paused"}
]}`

func parseSample(t *testing.T) []Item {
	t.Helper()
	items, err := Parse([]byte(sample), 1)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(items) != 12 {
		t.Fatalf("拿到 %d 条，想要 12 条", len(items))
	}
	return items
}

func item(t *testing.T, items []Item, name string) Item {
	t.Helper()
	for _, it := range items {
		if it.Name == name {
			return it
		}
	}
	t.Fatalf("样本里没有 %q", name)
	return Item{}
}

func TestPlainCheckKeepsSourceUUIDAndShape(t *testing.T) {
	it := item(t, parseSample(t), "备份数据库")
	if !it.Importable() {
		t.Fatalf("不该被拒：%s", it.Problem)
	}
	in := it.New
	if in.Code != "3d2f8bd8-cb1a-4a6c-b0a2-6b8e0f8b7c11" {
		t.Errorf("Code = %q，应该保留源 UUID", in.Code)
	}
	if in.ProjectID != 1 {
		t.Errorf("ProjectID = %d", in.ProjectID)
	}
	if in.Kind != engine.KindSimple || in.Timeout != time.Hour || in.Grace != 10*time.Minute {
		t.Errorf("Kind=%v Timeout=%v Grace=%v", in.Kind, in.Timeout, in.Grace)
	}
	if in.Tags != "db nightly" {
		t.Errorf("Tags = %q，想要把连续空白收成单空格", in.Tags)
	}
	if in.Slug != "db-backup" || in.Desc != "每晚全量" || in.Methods != "POST" {
		t.Errorf("Slug/Desc/Methods = %q/%q/%q", in.Slug, in.Desc, in.Methods)
	}
	if !in.ManualResum || !in.FilterHTTP || in.StartKW != "start" || in.SuccessKW != "OK" {
		t.Errorf("开关没带全：%+v", in)
	}
	if in.TZ != "UTC" {
		t.Errorf("TZ = %q，simple 检查项缺 tz 时应该落 UTC", in.TZ)
	}
	if len(in.ChannelIDs) != 0 {
		t.Errorf("ChannelIDs = %v，源渠道不该被带进来", in.ChannelIDs)
	}
	if !strings.Contains(strings.Join(it.Notes, "|"), "源渠道 2 个未迁移") {
		t.Errorf("Notes = %q，应该提醒手动订阅", it.Notes)
	}
}

func TestCronDialects(t *testing.T) {
	items := parseSample(t)

	six := item(t, items, "每天六点")
	if !six.Importable() || six.New.Schedule != "0 18 * * *" || six.New.Kind != engine.KindCron {
		t.Errorf("五段 cron 应该原样带过，得到 %+v / %s", six.New, six.Problem)
	}
	if six.New.TZ != "Asia/Shanghai" {
		t.Errorf("TZ = %q", six.New.TZ)
	}

	four := item(t, items, "工作日每4小时")
	if !four.Importable() {
		t.Fatalf("OnCalendar 常见形状应该能降段：%s", four.Problem)
	}
	if got := four.New.Schedule; got != "0 0/4 * * Mon-Fri" {
		t.Errorf("Schedule = %q，想要 \"0 0/4 * * Mon-Fri\"", got)
	}
	if !strings.Contains(strings.Join(four.Notes, "|"), "已降段成") {
		t.Errorf("Notes = %q，应该说明降段", four.Notes)
	}
}

func TestRefusedSchedulesComeBackAsProblems(t *testing.T) {
	items := parseSample(t)
	for name, want := range map[string]string{
		"月末清理":   "月末",
		"带年份":    "年份",
		"两个时间点":  "并集",
		"亚分钟":    "秒位",
		"slug 歪": "小写字母",
		"宽限太短":   "grace",
		"时区歪":    "时区",
	} {
		it := item(t, items, name)
		if it.Importable() {
			t.Errorf("%s 应该被拒，却过了", name)
			continue
		}
		if !strings.Contains(it.Problem, want) {
			t.Errorf("%s 的问题 %q 里不含 %q", name, it.Problem, want)
		}
	}
}

func TestSubjectFoldsIntoKeywords(t *testing.T) {
	it := item(t, parseSample(t), "邮件主题")
	if !it.Importable() {
		t.Fatalf("不该被拒：%s", it.Problem)
	}
	if it.New.SuccessKW != "done" || it.New.FailureKW != "failed" {
		t.Errorf("关键字 = %q/%q", it.New.SuccessKW, it.New.FailureKW)
	}
	joined := strings.Join(it.Notes, "|")
	for _, want := range []string{"success_kw", "failure_kw", "filter_subject/filter_body"} {
		if !strings.Contains(joined, want) {
			t.Errorf("Notes 里缺 %q：%q", want, it.Notes)
		}
	}
}

func TestPausedSourceIsNoted(t *testing.T) {
	it := item(t, parseSample(t), "暂停中的")
	if !strings.Contains(strings.Join(it.Notes, "|"), "源是暂停态") {
		t.Errorf("Notes = %q", it.Notes)
	}
}

func TestMissingUUIDGeneratesOneAndSaysSo(t *testing.T) {
	items, err := Parse([]byte(`[{"name":"裸数组","timeout":600,"grace":600}]`), 7)
	if err != nil {
		t.Fatalf("裸数组也该认：%v", err)
	}
	it := items[0]
	if !it.Importable() {
		t.Fatalf("不该被拒：%s", it.Problem)
	}
	if len(it.New.Code) != 0 {
		t.Errorf("没有 uuid 时不该编一个 Code：%q", it.New.Code)
	}
	if !strings.Contains(strings.Join(it.Notes, "|"), "只读 key") {
		t.Errorf("Notes = %q，应该说明地址会换", it.Notes)
	}
}

func TestNotAnExport(t *testing.T) {
	for _, body := range []string{"", "   ", `{"checks":[]}`, `{"nope":1}`} {
		if _, err := Parse([]byte(body), 1); err == nil {
			t.Errorf("Parse(%q) 应该报错", body)
		}
	}
	if _, err := Parse([]byte(`{"checks":[{"name":"歪","uuid":"not-a-uuid","timeout":600} ]}`), 1); err != nil {
		t.Fatalf("坏 uuid 是行级问题，不该整体失败：%v", err)
	}
}

func TestRefusedRowCarriesNoLeftoverNotes(t *testing.T) {
	// 跳过的行不该还挂着「地址会另生成」这种只对进库有意义的话。
	for _, it := range parseSample(t) {
		if !it.Importable() && len(it.Notes) != 0 {
			t.Errorf("%s 被拒了还留着 Notes：%q", it.Name, it.Notes)
		}
	}
}
