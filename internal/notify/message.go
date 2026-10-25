package notify

import (
	"fmt"
	"strings"
	"time"

	"github.com/Felix2yu/feixi/internal/engine"
	"github.com/Felix2yu/feixi/internal/format"
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

// nagBody is the repeated reminder sent while a check stays down. It leads
// with how long the outage has lasted so it reads differently from the first
// alert.
func nagBody(c *engine.Check, reason engine.Reason, downFor time.Duration, detailURL string) string {
	var lines []string
	lines = append(lines, "仍未恢复：已宕机 "+format.Duration(downFor)+"。")
	if txt := reasonLong(reason); txt != "" {
		lines = append(lines, "原因："+txt+"。")
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

// flapTitle/stableTitle label the two synthetic flap notifications.
func flapTitle(c *engine.Check) string   { return c.NameThenCode() + " 状态抖动" }
func stableTitle(c *engine.Check) string { return c.NameThenCode() + " 已恢复稳定" }

// flapEnterBody explains why individual alerts stop: the check is oscillating.
func flapEnterBody(c *engine.Check, window time.Duration, threshold int, detailURL string) string {
	var lines []string
	lines = append(lines, fmt.Sprintf("状态在 %s 内已至少翻转 %d 次，判定为抖动；暂停逐条宕机/恢复告警，状态持续稳定 %s 后自动恢复通知。",
		format.Duration(window), threshold, format.Duration(window)))
	appendCheckContext(&lines, c, detailURL)
	return strings.Join(lines, "\n")
}

// stableBody closes a flap episode that ended while the check was healthy.
func stableBody(c *engine.Check, window time.Duration, detailURL string) string {
	var lines []string
	lines = append(lines, "抖动已结束：状态已连续 "+format.Duration(window)+" 保持正常。")
	appendCheckContext(&lines, c, detailURL)
	return strings.Join(lines, "\n")
}

func appendCheckContext(lines *[]string, c *engine.Check, detailURL string) {
	if c.Schedule != "" {
		*lines = append(*lines, "计划："+c.Schedule+"（"+c.TZ+"）")
	} else if c.Kind == engine.KindSimple {
		*lines = append(*lines, "超时："+format.Duration(c.Timeout))
	}
	if c.LastPing != nil {
		*lines = append(*lines, "最后 ping："+c.LastPing.UTC().Format("2006-01-02 15:04:05")+" UTC")
	}
	if detailURL != "" {
		*lines = append(*lines, detailURL)
	}
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
