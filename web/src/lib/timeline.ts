// The "最近 24 小时" strip: state changes are events, so the timeline is a set of
// continuous spans whose width is the time spent in that state — not one cell per
// pixel, which reads as "there were 120 pings".

export type TimelineState = "up" | "down" | "late" | null;

export interface TimelineRun {
  state: TimelineState;
  from: number;
  to: number;
}

export interface FlipEvent {
  timestamp: string;
  up: 0 | 1;
}

const palette: Record<string, string> = {
  up: "#4caf50",
  late: "#fe7d37",
  down: "#e05d44",
  none: "#e4e7eb",
};

export function timelineColor(state: TimelineState): string {
  return state ? (palette[state] ?? palette.none) : palette.none;
}

// buildRuns turns flip events into spans. A flip reports the status it moved TO,
// so the span that follows it carries that status; whatever precedes the oldest
// event in the window is unknown and stays grey.
export function buildRuns(
  flips: FlipEvent[],
  status: string,
  now: number,
  windowMs: number,
): TimelineRun[] {
  const start = now - windowMs;
  const events = flips
    .map((f) => ({ at: Date.parse(f.timestamp), up: f.up === 1 }))
    .filter((f) => !Number.isNaN(f.at))
    .sort((a, b) => a.at - b.at);

  if (events.length === 0) {
    const state: TimelineState =
      status === "down" ? "down" : status === "grace" ? "late" : status === "up" ? "up" : null;
    return [{ state, from: start, to: now }];
  }

  const runs: TimelineRun[] = [];
  if (events[0].at > start) {
    runs.push({ state: null, from: start, to: events[0].at });
  }
  for (let i = 0; i < events.length; i++) {
    const from = Math.max(events[i].at, start);
    const to = i + 1 < events.length ? events[i + 1].at : now;
    if (to > from) runs.push({ state: events[i].up ? "up" : "down", from, to });
  }
  return runs;
}
