// Package format holds the presentation helpers shared by the API and the
// notification text, so both say the same thing about the same duration.
package format

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// ISOString matches Python's `dt.replace(microsecond=0).isoformat()` on an aware
// UTC datetime: second precision and an explicit +00:00 offset. API clients
// compare these strings, so the shape is part of the contract.
func ISOString(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.UTC().Format("2006-01-02T15:04:05") + "+00:00"
	return &s
}

// ISODateTime matches Python's datetime.isoformat() on an aware datetime: the
// fractional part appears only when microseconds are non-zero. Ping timestamps
// use this form; check timestamps go through ISOString.
func ISODateTime(t time.Time) string {
	utc := t.UTC()
	base := utc.Format("2006-01-02T15:04:05")
	if us := utc.Nanosecond() / 1000; us != 0 {
		base += fmt.Sprintf(".%06d", us)
	}
	return base + "+00:00"
}

// DurationSeconds truncates to whole seconds, like int(td.total_seconds()).
func DurationSeconds(d time.Duration) int { return int(d / time.Second) }

type unit struct {
	name    string
	seconds int64
}

var units = []unit{
	{"周", 7 * 24 * 3600},
	{"天", 24 * 3600},
	{"小时", 3600},
	{"分钟", 60},
}

// Duration renders a duration in words: "1 小时 30 分钟". Weeks are only used
// when they divide exactly, so a gap of eight days reads "8 天" not "1 周 1 天".
func Duration(d time.Duration) string {
	remaining := int64(d / time.Second)
	if remaining <= 0 {
		return "0 秒"
	}

	var parts []string
	for i, u := range units {
		if i == 0 && remaining%u.seconds != 0 {
			continue
		}
		v := remaining / u.seconds
		remaining %= u.seconds
		if v > 0 {
			parts = append(parts, strconv.FormatInt(v, 10)+" "+u.name)
		}
	}
	if remaining > 0 {
		parts = append(parts, strconv.FormatInt(remaining, 10)+" 秒")
	}
	return strings.Join(parts, " ")
}

// HMS is the stopwatch form used for a running check: "1 小时 2 分钟 3 秒".
// Sub-second runs are shown to two decimals instead of collapsing to "0 秒".
func HMS(d time.Duration) string {
	if s := d.Seconds(); s >= 0.01 && s < 1 {
		return strconv.FormatFloat(s, 'f', 2, 64) + " 秒"
	}

	total := int64(d / time.Second)
	hours, mins := total/3600, total%3600/60
	secs := total % 60

	var parts []string
	if hours > 0 {
		parts = append(parts, strconv.FormatInt(hours, 10)+" 小时")
	}
	if hours > 0 || mins > 0 {
		parts = append(parts, strconv.FormatInt(mins, 10)+" 分钟")
	}
	parts = append(parts, strconv.FormatInt(secs, 10)+" 秒")
	return strings.Join(parts, " ")
}
