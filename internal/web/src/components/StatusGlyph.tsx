import {
  AlertCircle,
  CheckCircle2,
  CircleDashed,
  Clock,
  PauseCircle,
} from "lucide-react";
import { statusMeta } from "../lib/status";
import type { Display } from "../lib/types";

export function StatusGlyph({ status, started, size = 16 }: { status: Display; started: boolean; size?: number }) {
  const meta = statusMeta(status);
  if (started && status !== "down") {
    return (
      <span className="spinner" style={{ color: meta.color }} title={meta.tip} aria-label="运行中">
        <span />
        <span />
        <span />
      </span>
    );
  }
  const props = { size, style: { color: meta.color }, title: meta.tip, "aria-label": meta.label };
  switch (status) {
    case "up":
      return <CheckCircle2 {...props} />;
    case "grace":
      return <Clock {...props} />;
    case "down":
      return <AlertCircle {...props} />;
    case "paused":
      return <PauseCircle {...props} />;
    case "started":
      return (
        <span className="spinner" style={{ color: meta.color }} title={meta.tip} aria-label="运行中">
          <span />
          <span />
          <span />
        </span>
      );
    default:
      return <CircleDashed {...props} />;
  }
}

export function StatusText({ status }: { status: Display }) {
  const meta = statusMeta(status);
  return <span className={cxText(status)}>{meta.label}</span>;
}

function cxText(status: Display): string {
  return statusMeta(status).text;
}
