package badge

import (
	"strings"
	"testing"
)

func TestSVGShapes(t *testing.T) {
	svg := SVG("prod", "up")
	if !strings.HasPrefix(svg, "<svg ") || !strings.HasSuffix(strings.TrimSpace(svg), "</svg>") {
		t.Fatalf("not an svg document: %s", svg)
	}
	// "prod" is 7+4+7+7 = 25 wide, "up" is 14, plus 10 padding on each side.
	if !strings.Contains(svg, `width="59"`) {
		t.Errorf("unexpected width: %s", strings.Split(svg, "\n")[0])
	}
	if !strings.Contains(svg, `fill="#4c1"`) {
		t.Error("up should use the green band")
	}

	late := SVG("*", "late")
	if !strings.Contains(late, "#fe7d37") {
		t.Error("late should use the orange band")
	}
	if !strings.Contains(SVG("x", "down"), "#e05d44") {
		t.Error("down should use the red band")
	}
}

func TestSVGEscapesMarkup(t *testing.T) {
	svg := SVG(`<script>alert(1)</script>`, "up")
	if strings.Contains(svg, "<script>") {
		t.Error("a tag name must never appear as live markup")
	}
	if !strings.Contains(svg, "&lt;script&gt;") {
		t.Errorf("expected escaped content: %s", svg)
	}
}

func TestSignAndCheck(t *testing.T) {
	secret := "s3cr3t"
	sig := Sign(secret, "badge-key", "prod", true)
	if len(sig) != 8 {
		t.Fatalf("sig = %q, want 8 characters", sig)
	}
	if !Check(secret, "badge-key", "prod", sig) {
		t.Error("signature should validate")
	}
	if Check(secret, "badge-key", "other", sig) {
		t.Error("a different tag must not validate")
	}
	if Check("other-secret", "badge-key", "prod", sig) {
		t.Error("a different project key must not validate")
	}

	twoState := Sign(secret, "badge-key", "prod", false)
	if !strings.HasSuffix(twoState, "-2") || len(twoState) != 10 {
		t.Errorf("two-state sig = %q, want the -2 suffix", twoState)
	}
	if !Check(secret, "badge-key", "prod", twoState) {
		t.Error("the -2 suffix must be ignored when validating")
	}
	if WithLate(twoState) {
		t.Error("WithLate should be false for a -2 signature")
	}
	if !WithLate(sig) {
		t.Error("WithLate should be true for a bare signature")
	}
}
