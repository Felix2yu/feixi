package notify

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"net/url"

	"github.com/Felix2yu/feixi/internal/engine"
)

// checkPayload is the $JSON document: the check fields a receiver is most
// likely to want, with the same names as the management API.
func checkPayload(c *engine.Check, pingRoot string) map[string]any {
	out := map[string]any{
		"name":     c.Name,
		"slug":     c.Slug,
		"tags":     c.Tags,
		"desc":     c.Description,
		"grace":    int(c.Grace.Seconds()),
		"n_pings":  c.NPings,
		"status":   string(c.Status),
		"uuid":     c.Code,
		"ping_url": pingRoot + "/ping/" + c.Code,
	}
	if c.LastPing != nil {
		out["last_ping"] = c.LastPing.UTC().Format("2006-01-02T15:04:05") + "+00:00"
	}
	if c.Kind == engine.KindSimple {
		out["timeout"] = int(c.Timeout.Seconds())
	} else {
		out["schedule"] = c.Schedule
		out["tz"] = c.TZ
	}
	return out
}

// vars are substituted into the channel's URL list before apprise-go sees it,
// so a plain `json://host/path?message=$NAME` behaves like the original
// webhook channel's template. Ordered longest-first: $BODY_JSON must win over
// $BODY.
var varOrder = []string{
	"$BODY_JSON", "$EXITSTATUS", "$NAME_JSON", "$TAGS", "$STATUS", "$CODE",
	"$SLUG", "$BODY", "$NAME", "$JSON", "$NOW",
	"$TAG1", "$TAG2", "$TAG3", "$TAG4", "$TAG5",
	"$TAG6", "$TAG7", "$TAG8", "$TAG9",
}

type varValues struct {
	values map[string]string
}

func (v varValues) apply(raw string) string {
	out := raw
	for _, key := range varOrder {
		if value, ok := v.values[key]; ok && strings.Contains(out, key) {
			out = strings.ReplaceAll(out, key, value)
		}
	}
	return out
}

// buildVars collects the substitution table. Expensive entries (the last ping
// body) are only fetched when the URL actually references them, as in the
// original webhook transport.
func (w *Worker) buildVars(ctx context.Context, c *engine.Check, f *engine.Flip, raw string) (varValues, error) {
	out := map[string]string{
		"$CODE":   c.Code,
		"$STATUS": string(f.NewStatus),
		"$NOW":    f.Created.UTC().Format("2006-01-02T15:04:05") + "+00:00",
		"$NAME":   safen(c.Name),
		"$SLUG":   c.Slug,
		"$TAGS":   safen(c.Tags),
	}
	out["$NAME_JSON"] = safen(c.Name)
	bodyJSON, _ := json.Marshal(checkPayload(c, w.pingRoot))
	out["$JSON"] = safen(string(bodyJSON))

	for i, tag := range strings.Fields(c.Tags) {
		if i < 9 {
			out["$TAG"+string(rune('1'+i))] = safen(tag)
		}
	}

	needsBody := strings.Contains(raw, "$BODY") || strings.Contains(raw, "$EXITSTATUS")
	if needsBody {
		body, exitstatus, err := w.DB.LastPingBody(ctx, c.ID, f.Created)
		if err != nil {
			return varValues{}, err
		}
		out["$BODY"] = safen(string(body))
		parsed := json.RawMessage(body)
		if !json.Valid(parsed) {
			encoded, _ := json.Marshal(string(body))
			parsed = encoded
		}
		out["$BODY_JSON"] = safen(string(parsed))
		if exitstatus != nil {
			out["$EXITSTATUS"] = strconv.FormatInt(*exitstatus, 10)
		} else {
			out["$EXITSTATUS"] = "-1"
		}
	}

	return varValues{values: out}, nil
}

// safen percent-encodes a substituted value. Everything here lands inside an
// apprise URL, so an unescaped "&", "#" or space would silently change the
// target; the receiver sees the original text back after URL parsing.
func safen(s string) string {
	return url.QueryEscape(s)
}
