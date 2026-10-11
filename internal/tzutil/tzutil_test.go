package tzutil

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestNormalize(t *testing.T) {
	if got := Normalize("Europe/Kiev"); got != "Europe/Kyiv" {
		t.Errorf("Normalize legacy = %q, want Europe/Kyiv", got)
	}
	if got := Normalize("US/Pacific"); got != "America/Los_Angeles" {
		t.Errorf("Normalize US/Pacific = %q", got)
	}
	if got := Normalize("Asia/Shanghai"); got != "Asia/Shanghai" {
		t.Errorf("canonical name should pass through, got %q", got)
	}
}

func TestLoad(t *testing.T) {
	loc, err := Load("")
	if err != nil {
		t.Fatalf("Load(\"\"): %v", err)
	}
	if loc != time.UTC {
		t.Errorf("empty zone should mean UTC, got %v", loc)
	}

	// 旧写法要能解析成同一个区域，否则迁移前配好的 cron 表达式会算错下一次时间。
	loc, err = Load("Asia/Calcutta")
	if err != nil {
		t.Fatalf("Load legacy alias: %v", err)
	}
	if loc.String() != "Asia/Kolkata" {
		t.Errorf("legacy alias resolved to %q", loc.String())
	}

	loc, err = Load("Asia/Shanghai")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, offset := time.Date(2026, 6, 1, 0, 0, 0, 0, loc).Zone(); offset != 8*3600 {
		t.Errorf("Asia/Shanghai offset = %d, want 28800", offset)
	}

	_, err = Load("Mars/Olympus")
	if err == nil {
		t.Fatal("unknown zone should fail")
	}
	var uz *UnknownZoneError
	if !errors.As(err, &uz) {
		t.Fatalf("error type = %T, want *UnknownZoneError", err)
	}
	if uz.Name != "Mars/Olympus" {
		t.Errorf("error should carry the original name, got %q", uz.Name)
	}
	if got := uz.Error(); got != "未知的时区名称：Mars/Olympus" {
		t.Errorf("Error() = %q", got)
	}
}

func TestValid(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", true},
		{"UTC", true},
		{"Europe/Berlin", true},
		{"GB", true}, // 旧别名同样算有效
		{"Europe/Kyiv ", false},
		{"Mars/Olympus", false},
		{"../etc", false},
	}
	for _, tt := range tests {
		if got := Valid(tt.in); got != tt.want {
			t.Errorf("Valid(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestValidateName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"empty is allowed and means UTC", "", true},
		{"plain iana name", "Asia/Shanghai", true},
		{"digits and signs", "GMT+8", true},
		{"underscore", "America/Buenos_Aires", true},
		{"space is rejected", "Europe/Kyiv ", false},
		{"non ascii is rejected", "亚洲/上海", false},
		{"dot dot is rejected", "../etc", false},
		{"at the length limit", strings.Repeat("A", 36), true},
		{"over the length limit", strings.Repeat("A", 37), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateName(tt.in); got != tt.want {
				t.Errorf("ValidateName(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}
