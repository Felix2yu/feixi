package hcimport

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Felix2yu/feixi/internal/cronex"
	"github.com/Felix2yu/feixi/internal/tzutil"
)

// systemd 的日历表达式比 5 段 cron 多出四类东西：年份、月末（~）、亚分钟秒位、
// 以及多条表达式的并集。这四类飞檄都装不下，只能带原因退回人工处理。
// 剩下的常见形状（每天几点、工作日每 N 小时、每月几号）逐项映射成 cron 字段。

var (
	fieldChars = regexp.MustCompile(`^[-,/*0-9A-Za-z]+$`)
	letterRun  = regexp.MustCompile(`[A-Za-z]+`)
	digitRun   = regexp.MustCompile(`[0-9]+`)
)

// systemd 接受整名（Monday、January），cron 只认三字母，这里统一收口。
// 星期名和月份名分开两张表：混在一张里，"jul" 会被当成合法的星期字段跑出去。
var (
	weekdayNames = map[string]string{
		"sun": "Sun", "mon": "Mon", "tue": "Tue", "wed": "Wed", "thu": "Thu", "fri": "Fri", "sat": "Sat",
		"sunday": "Sun", "monday": "Mon", "tuesday": "Tue", "wednesday": "Wed",
		"thursday": "Thu", "friday": "Fri", "saturday": "Sat",
	}
	monthNames = map[string]string{
		"jan": "Jan", "feb": "Feb", "mar": "Mar", "apr": "Apr", "may": "May", "jun": "Jun",
		"jul": "Jul", "aug": "Aug", "sep": "Sep", "oct": "Oct", "nov": "Nov", "dec": "Dec",
		"january": "Jan", "february": "Feb", "march": "Mar", "april": "Apr", "june": "Jun",
		"july": "Jul", "august": "Aug", "september": "Sep", "october": "Oct",
		"november": "Nov", "december": "Dec",
	}
)

type slot int

const (
	empty slot = iota
	clockSlot
	dateSlot
	dowSlot
	zoneSlot
)

var slotLabels = map[slot]string{
	clockSlot: "时间", dateSlot: "日期", dowSlot: "星期", zoneSlot: "时区",
}

// toCron rewrites one systemd OnCalendar expression as a five-field cron
// schedule. An error always names the construct that cannot be carried over.
// The second result is a zone the expression spelled out itself, if any.
func toCron(expr string) (string, string, error) {
	lines := exprLines(expr)
	switch len(lines) {
	case 0:
		return "", "", errors.New("表达式是空的")
	case 1:
	default:
		return "", "", fmt.Errorf("写了 %d 条表达式（并集），飞檄一个检查项只挂一个 schedule", len(lines))
	}

	filled := map[slot]string{}
	for _, tok := range strings.Fields(lines[0]) {
		k := classify(tok)
		if _, dup := filled[k]; dup {
			return "", "", fmt.Errorf("%s部分出现了两次（%q）", slotLabels[k], tok)
		}
		filled[k] = tok
	}

	minute, hour, err := splitClock(filled[clockSlot])
	if err != nil {
		return "", "", err
	}
	day, month, err := splitDate(filled[dateSlot])
	if err != nil {
		return "", "", err
	}
	weekday, err := splitDow(filled[dowSlot])
	if err != nil {
		return "", "", err
	}
	// Vixie cron 在两个字段都限定时取并集，systemd 取交集，这里不能糊过去。
	if day != "*" && weekday != "*" {
		return "", "", fmt.Errorf(
			"同时限定了日子(%q)和星期(%q)：cron 里两者取并集、systemd 取交集，降段会把触发日放宽", day, weekday)
	}
	zone := ""
	if z := filled[zoneSlot]; z != "" {
		if !tzutil.Valid(z) {
			return "", "", fmt.Errorf("表达式里写的 %q 不是时区名", z)
		}
		zone = z
	}

	cron := strings.Join([]string{minute, hour, day, month, weekday}, " ")
	if err := cronex.Validate(cron); err != nil {
		return "", "", fmt.Errorf("降成 %q 后仍不是飞檄收得下的 cron：%s", cron, err)
	}
	return cron, zone, nil
}

func exprLines(expr string) []string {
	var out []string
	for _, chunk := range strings.FieldsFunc(expr, func(r rune) bool { return r == '\n' || r == ';' }) {
		if strings.TrimSpace(chunk) != "" {
			out = append(out, chunk)
		}
	}
	return out
}

func classify(tok string) slot {
	switch {
	case strings.Contains(tok, ":"):
		return clockSlot
	case strings.Contains(tok, "/"):
		// 日期段用 - 连接，带 / 的只能是表达式尾上的时区（Europe/Berlin）。
		return zoneSlot
	case strings.Count(tok, "-") == 2:
		return dateSlot
	case strings.Contains(tok, "*"):
		// 带 * 又不是三段，多半是日期写歪了，交给 splitDate 报准确的错。
		return dateSlot
	default:
		return dowSlot
	}
}

// splitClock 缺时间部分时按 systemd 的口径落午夜零点。
func splitClock(tok string) (minute, hour string, err error) {
	if tok == "" {
		return "0", "0", nil
	}
	parts := strings.Split(tok, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return "", "", fmt.Errorf("时间部分 %q 要写成 HH:MM 或 HH:MM:SS", tok)
	}
	if len(parts) == 3 && parts[2] != "0" && parts[2] != "00" {
		return "", "", fmt.Errorf("秒位是 %q：降段等于把触发点挪回整分，飞檄不做亚分钟计划", parts[2])
	}
	if hour, err = normalizeField("小时", parts[0], nil); err != nil {
		return "", "", err
	}
	if minute, err = normalizeField("分钟", parts[1], nil); err != nil {
		return "", "", err
	}
	return minute, hour, nil
}

func splitDate(tok string) (day, month string, err error) {
	if tok == "" {
		return "*", "*", nil
	}
	parts := strings.Split(tok, "-")
	if len(parts) != 3 {
		return "", "", fmt.Errorf("日期部分 %q 要写成 年-月-日", tok)
	}
	if parts[0] != "*" {
		return "", "", fmt.Errorf("写了年份 %q：cron 没有年份字段", parts[0])
	}
	if month, err = normalizeField("月份", parts[1], monthNames); err != nil {
		return "", "", err
	}
	if day, err = normalizeField("日", parts[2], nil); err != nil {
		return "", "", err
	}
	return day, month, nil
}

func splitDow(tok string) (string, error) {
	if tok == "" || tok == "*" {
		return "*", nil
	}
	return normalizeField("星期", tok, weekdayNames)
}

// normalizeField keeps cron's alphabet, folds systemd's ".." ranges into cron's
// "-", and rewrites full weekday/month names to the three letters cron accepts.
// names 为空表示这个槽只接受数字。
func normalizeField(label, part string, names map[string]string) (string, error) {
	if part == "" {
		return "", fmt.Errorf("%s字段是空的", label)
	}
	p := strings.ReplaceAll(part, "..", "-")
	switch {
	case strings.Contains(p, "~"):
		return "", fmt.Errorf("%s字段 %q 用了 ~（月末/年末），cron 表达不了，建议改成月初", label, part)
	case strings.Contains(p, "**"):
		return "", fmt.Errorf("%s字段 %q 用了 ** 数字通配，cron 表达不了", label, part)
	case !fieldChars.MatchString(p):
		return "", fmt.Errorf("%s字段 %q 里有用不上的字符", label, part)
	}
	var unknown string
	out := letterRun.ReplaceAllStringFunc(p, func(run string) string {
		name, ok := names[strings.ToLower(run)]
		if !ok {
			unknown = run
			return run
		}
		return name
	})
	if unknown != "" {
		if names == nil {
			return "", fmt.Errorf("%s字段 %q 里的 %q 不是数字", label, part, unknown)
		}
		return "", fmt.Errorf("%s字段 %q 里的 %q 不是%s名", label, part, unknown, label)
	}
	// systemd 允许 07 这种补零写法，cron 那边显示成 7 更好比对。
	return digitRun.ReplaceAllStringFunc(out, func(run string) string {
		n, err := strconv.Atoi(run)
		if err != nil {
			return run
		}
		return strconv.Itoa(n)
	}), nil
}
