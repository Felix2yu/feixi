package format

import (
	"testing"
	"time"
)

func TestISOString(t *testing.T) {
	if got := ISOString(nil); got != nil {
		t.Errorf("ISOString(nil) = %v, want nil", got)
	}

	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 3, 5, 9, 8, 7, 123456, shanghai)
	got := ISOString(&ts)
	if got == nil {
		t.Fatal("expected a value")
	}
	if *got != "2026-03-05T01:08:07+00:00" {
		t.Errorf("ISOString = %q, want UTC second precision with +00:00", *got)
	}
}

func TestISODateTime(t *testing.T) {
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{
			name: "whole seconds omit the fraction",
			in:   time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC),
			want: "2026-01-02T03:04:05+00:00",
		},
		{
			name: "microseconds are padded to six digits",
			in:   time.Date(2026, 1, 2, 3, 4, 5, 7000, time.UTC),
			want: "2026-01-02T03:04:05.000007+00:00",
		},
		{
			name: "nanoseconds below a microsecond are dropped",
			in:   time.Date(2026, 1, 2, 3, 4, 5, 999, time.UTC),
			want: "2026-01-02T03:04:05+00:00",
		},
		{
			name: "input is converted to UTC",
			in:   time.Date(2026, 1, 2, 23, 4, 5, 0, time.FixedZone("UTC+8", 8*3600)),
			want: "2026-01-02T15:04:05+00:00",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ISODateTime(tt.in); got != tt.want {
				t.Errorf("ISODateTime = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDurationSeconds(t *testing.T) {
	if got := DurationSeconds(90*time.Second + 999*time.Millisecond); got != 90 {
		t.Errorf("DurationSeconds = %d, want truncation to 90", got)
	}
}

func TestDuration(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{0, "0 秒"},
		{-time.Minute, "0 秒"},
		{45 * time.Second, "45 秒"},
		{90 * time.Minute, "1 小时 30 分钟"},
		// 周只在整除时出现，8 天该读成「8 天」。
		{8 * 24 * time.Hour, "8 天"},
		{7 * 24 * time.Hour, "1 周"},
		{7*24*time.Hour + time.Hour, "7 天 1 小时"},
		{time.Hour + time.Second, "1 小时 1 秒"},
		{365 * 24 * time.Hour, "365 天"},
	}
	for _, tt := range tests {
		if got := Duration(tt.d); got != tt.want {
			t.Errorf("Duration(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestHMS(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"sub-second runs keep two decimals", 500 * time.Millisecond, "0.50 秒"},
		{"one second", time.Second, "1 秒"},
		{"minutes without hours", 2*time.Minute + 3*time.Second, "2 分钟 3 秒"},
		{"hours pad the middle unit", time.Hour + 2*time.Second, "1 小时 0 分钟 2 秒"},
		{"full stopwatch form", time.Hour + 2*time.Minute + 3*time.Second, "1 小时 2 分钟 3 秒"},
		{"0.005s is too small for decimals", 5 * time.Millisecond, "0 秒"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HMS(tt.d); got != tt.want {
				t.Errorf("HMS(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}
