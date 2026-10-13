package hcimport

import (
	"strings"
	"testing"
)

func TestToCron(t *testing.T) {
	tests := []struct {
		expr string
		want string
	}{
		{"*-*-* 03:00", "0 3 * * *"},
		{"*-*-* 12:00:00", "0 12 * * *"},
		{"12:00", "0 12 * * *"},
		{"0/15:00", "0 0/15 * * *"},
		{"Mon-Fri *-*-* 0/4:00", "0 0/4 * * Mon-Fri"},
		{"Mon..Fri *-*-* 12:00", "0 12 * * Mon-Fri"},
		{"Monday 08:00", "0 8 * * Mon"},
		{"Mon-Fri", "0 0 * * Mon-Fri"},
		{"*-*-*", "0 0 * * *"},
		{"*-*-15", "0 0 15 * *"},
		{"Sat,Sun *-*-* 06:30", "30 6 * * Sat,Sun"},
		{"*-*-1 12:00", "0 12 1 * *"},
		{"*-01-01 00:00", "0 0 1 1 *"},
		{"*-*-01..05 03:04", "4 3 1-5 * *"},
		{"*-*-* 00:00,12:00", "0,12 0 * * *"},
		{"*-*-1,15 06:00", "0 6 1,15 * *"},
		{"*-*-* *:00:00", "0 * * * *"},
		{"*-jul-15 09:00", "0 9 15 Jul *"},
	}
	for _, tt := range tests {
		got, _, err := toCron(tt.expr)
		if err != nil {
			t.Errorf("toCron(%q) 意外失败：%v", tt.expr, err)
			continue
		}
		if got != tt.want {
			t.Errorf("toCron(%q) = %q，想要 %q", tt.expr, got, tt.want)
		}
	}
}

func TestToCronRefuses(t *testing.T) {
	tests := []struct {
		expr   string
		reason string
	}{
		{"*-*-~1 12:00", "月末"},
		{"2026-03-01 12:00", "年份"},
		{"*-*-* 12:00:15", "秒位"},
		{"*-*-* *:*:*", "秒位"},
		{"*-*-* 08:00\n*-*-* 20:00", "并集"},
		{"*-*-** 12:00", "**"},
		{"Mon-Fri *-*-01..05 12:00", "并集"},
		{"*-* 12:00", "年-月-日"},
		{"*-*-* 12:00 Mars/Olympus", "不是时区名"},
		{"*-*-* 12", "12"},
		{"", "空的"},
	}
	for _, tt := range tests {
		_, _, err := toCron(tt.expr)
		if err == nil {
			t.Errorf("toCron(%q) 应该被拒，却成功了", tt.expr)
			continue
		}
		if !strings.Contains(err.Error(), tt.reason) {
			t.Errorf("toCron(%q) 的错误 %q 里不含 %q", tt.expr, err, tt.reason)
		}
	}
}

func TestToCronCarriesZoneSuffix(t *testing.T) {
	// hc 用的 oncalendar 库认这种写法，并在该时区里算下一次触发。
	cron, zone, err := toCron("*-*-* 09:00:00 Europe/Berlin")
	if err != nil {
		t.Fatalf("不该被拒：%v", err)
	}
	if cron != "0 9 * * *" || zone != "Europe/Berlin" {
		t.Errorf("得到 %q / %q", cron, zone)
	}
}

func TestToCronHandsUnparsableFieldsToCronex(t *testing.T) {
	// systemd 里 0 号星期是合法的，cron 的日字段没有 0 号。
	if _, _, err := toCron("*-*-0 12:00"); err == nil {
		t.Error("日=0 应该由 cronex 拦下")
	} else if !strings.Contains(err.Error(), "收得下的 cron") {
		t.Errorf("错误应该指回 cronex，收到 %q", err)
	}
}
