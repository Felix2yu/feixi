package notify

import (
	"strings"
	"time"

	"github.com/yufei/feixi/internal/engine"
	"github.com/yufei/feixi/internal/format"
)

// title follows the original apprise channel: "<name or code> UP|DOWN".
func title(c *engine.Check, status engine.Status) string {
	return c.NameThenCode() + " " + strings.ToUpper(string(status))
}

// body carries the reason for a failure and the length of the outage on
// recovery, the two facts the original apprise template prints.
func body(c *engine.Check, f *engine.Flip, downDur *time.Duration, detailURL string) string {
	var lines []string

	switch f.NewStatus {
	case engine.StatusDown:
		if reason := reasonLong(f.Reason); reason != "" {
			lines = append(lines, "原因："+reason+"。")
		}
	case engine.StatusUp:
		if downDur != nil && *downDur > 0 {
			lines = append(lines, "宕机持续了 "+format.Duration(*downDur)+"。")
		}
	}

	if c.Schedule != "" {
		lines = append(lines, "计划："+c.Schedule+"（"+c.TZ+"）")
	} else if c.Kind == engine.KindSimple {
		lines = append(lines, "超时："+format.Duration(c.Timeout))
	}
	if c.LastPing != nil {
		lines = append(lines, "最后 ping："+c.LastPing.UTC().Format("2006-01-02 15:04:05")+" UTC")
	}
	if detailURL != "" {
		lines = append(lines, detailURL)
	}
	return strings.Join(lines, "\n")
}

// reasonLong mirrors Flip.reason_long(), in the wording the Chinese templates use.
func reasonLong(r engine.Reason) string {
	switch r {
	case engine.ReasonTimeout:
		return "成功信号未按时到达，宽限期已过"
	case engine.ReasonFail:
		return "收到了失败信号"
	}
	return ""
}
