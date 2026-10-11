// Package cronex adapts robfig/cron to a strict cron dialect: five fields only,
// evaluated in a named timezone, with the extensions ordinary cron parsers do
// not share (?, L, W, #, @every, 6- and 7-field forms) refused at the door.
package cronex

import (
	"fmt"
	"strings"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/yufei/feixi/internal/tzutil"
)

var parser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

type Scheduler struct{}

// Next returns the first occurrence after `after` (an instant, interpreted in
// the check's local timezone), as UTC.
func (Scheduler) Next(schedule, tz string, after time.Time) (time.Time, bool, error) {
	loc, err := tzutil.Load(tz)
	if err != nil {
		return time.Time{}, false, err
	}
	sched, err := parser.Parse(schedule)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("无效的 cron 表达式：%s", schedule)
	}
	next := sched.Next(after.In(loc))
	if next.IsZero() {
		return time.Time{}, false, nil
	}
	// Callers add the grace period to this value, so it must sit on the UTC axis:
	// adding a duration to a local-zone time drifts by an hour across DST.
	return next.UTC(), true, nil
}

// Validate reports whether schedule is in the supported cron dialect.
func Validate(schedule string) error {
	s := strings.TrimSpace(schedule)
	if s == "" {
		return fmt.Errorf("schedule 不能为空")
	}

	if strings.HasPrefix(s, "@") {
		if strings.HasPrefix(s, "@every") {
			return fmt.Errorf("不支持 @every 语法，请使用标准 cron 表达式")
		}
		if _, err := parser.Parse(s); err != nil {
			return fmt.Errorf("无效的 cron 表达式：%s", s)
		}
		return nil
	}

	fields := strings.Fields(s)
	if len(fields) != 5 {
		return fmt.Errorf("cron 表达式需要 5 个字段，收到 %d 个：%s", len(fields), s)
	}
	for i, f := range fields {
		if strings.ContainsRune(f, '?') {
			return fmt.Errorf("cron 表达式不支持 ?：%s", s)
		}
		switch i {
		case 2:
			// L and W are day-of-month extensions; no month name uses those letters.
			if strings.ContainsAny(strings.ToUpper(f), "LW") {
				return fmt.Errorf("cron 表达式日期字段不支持 L/W：%s", s)
			}
		case 4:
			// # is the nth-weekday form; no weekday name contains L or #.
			if strings.ContainsAny(strings.ToUpper(f), "L#") {
				return fmt.Errorf("cron 表达式星期字段不支持 L/#：%s", s)
			}
		}
	}
	if _, err := parser.Parse(s); err != nil {
		return fmt.Errorf("无效的 cron 表达式：%s", s)
	}
	return nil
}
