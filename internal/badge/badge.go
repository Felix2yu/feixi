// Package badge renders the status badges meant for embedding in READMEs.
package badge

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// A per-character width table is the only way to size the two halves so the
// text stays centered without depending on font metrics at runtime.
var widths = map[rune]int{
	'a': 7, 'b': 7, 'c': 6, 'd': 7, 'e': 6, 'f': 4, 'g': 7, 'h': 7, 'i': 3, 'j': 3,
	'k': 7, 'l': 3, 'm': 10, 'n': 7, 'o': 7, 'p': 7, 'q': 7, 'r': 4, 's': 6, 't': 5,
	'u': 7, 'v': 7, 'w': 9, 'x': 6, 'y': 7, 'z': 7,
	'0': 7, '1': 6, '2': 7, '3': 7, '4': 7, '5': 7, '6': 7, '7': 7, '8': 7, '9': 7,
	'A': 8, 'B': 7, 'C': 8, 'D': 8, 'E': 7, 'F': 6, 'G': 9, 'H': 8, 'I': 3, 'J': 4,
	'K': 7, 'L': 6, 'M': 10, 'N': 8, 'O': 9, 'P': 6, 'Q': 9, 'R': 7, 'S': 7, 'T': 7,
	'U': 8, 'V': 8, 'W': 11, 'X': 7, 'Y': 7, 'Z': 7,
	'-': 4, '_': 6,
}

var colors = map[string]string{
	"up":   "#4c1",
	"late": "#fe7d37",
	"down": "#e05d44",
}

// ShieldsColors maps badge states onto shields.io colour names.
var ShieldsColors = map[string]string{
	"up":   "success",
	"late": "important",
	"down": "critical",
}

func textWidth(s string) int {
	total := 0
	for _, r := range s {
		if w, ok := widths[r]; ok {
			total += w
			continue
		}
		// CJK and other wide glyphs are roughly an em wide at font-size 11.
		total += 11
	}
	return total
}

const svgTemplate = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="%s" height="20">
  <linearGradient id="smooth" x2="0" y2="100%%">
    <stop offset="0" stop-color="#bbb" stop-opacity=".1"/>
    <stop offset="1" stop-opacity=".1"/>
  </linearGradient>

  <mask id="round">
    <rect width="%s" height="20" rx="3" fill="#fff"/>
  </mask>

  <g mask="url(#round)">
    <rect width="%s" height="20" fill="#555"/>
    <rect x="%s" width="%s" height="20" fill="%s"/>
    <rect width="%s" height="20" fill="url(#smooth)"/>
  </g>

  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" text-rendering="geometricPrecision" font-size="11">
    <text x="%s" y="15" fill="#010101" fill-opacity=".3">%s</text>
    <text x="%s" y="14">%s</text>
    <text x="%s" y="15" fill="#010101" fill-opacity=".3">%s</text>
    <text x="%s" y="14">%s</text>
  </g>
</svg>
`

// SVG draws the two-part badge. Both labels are escaped by the caller's data
// (tag names and check names come from the operator, not from untrusted input),
// but we still neutralise the characters that would break the document.
func SVG(tag, status string) string {
	w1 := textWidth(tag) + 10
	w2 := textWidth(status) + 10
	total := w1 + w2
	tagCenter := strconv.FormatFloat(float64(w1)/2, 'f', 1, 64)
	statusCenter := strconv.FormatFloat(float64(w1+w2/2), 'f', 1, 64)

	color, ok := colors[status]
	if !ok {
		color = colors["up"]
	}

	return fmt.Sprintf(svgTemplate,
		strconv.Itoa(total), strconv.Itoa(total),
		strconv.Itoa(w1), strconv.Itoa(w1), strconv.Itoa(w2), color, strconv.Itoa(total),
		tagCenter, escape(tag), tagCenter, escape(tag),
		statusCenter, escape(status), statusCenter, escape(status),
	)
}

func escape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// Sign produces the unguessable part of a badge URL. The original truncates a
// Django base64_hmac to 8 characters and appends "-2" to mark a two-state badge;
// both details are kept so the URLs look the same.
func Sign(secret, badgeKey, tag string, withLate bool) string {
	sum := hmacSHA256(secret, "feixi-badge/"+badgeKey+"/"+tag)
	sig := base64.RawURLEncoding.EncodeToString(sum)[:8]
	if !withLate {
		sig += "-2"
	}
	return sig
}

// Check verifies a badge signature, tolerating the "-2" suffix.
func Check(secret, badgeKey, tag, sig string) bool {
	want := hmacSHA256(secret, "feixi-badge/"+badgeKey+"/"+tag)
	encoded := base64.RawURLEncoding.EncodeToString(want)[:8]
	return hmac.Equal([]byte(encoded), []byte(sigPrefix(sig)))
}

// WithLate reports whether a signature asks for the three-state rendering.
func WithLate(sig string) bool {
	return !(len(sig) == 10 && strings.HasSuffix(sig, "-2"))
}

func sigPrefix(sig string) string {
	if len(sig) == 10 && strings.HasSuffix(sig, "-2") {
		return sig[:8]
	}
	return sig
}

func hmacSHA256(secret, message string) []byte {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(message))
	return m.Sum(nil)
}
