import dayjs from "dayjs";
import relativeTime from "dayjs/plugin/relativeTime";
import "dayjs/locale/zh-cn";

dayjs.extend(relativeTime);
dayjs.locale("zh-cn");

export function fromNow(value: string | null | undefined): string {
  if (!value) return "从未";
  return dayjs(value).from(dayjs());
}

export function formatDateTime(value: string | null | undefined): string {
  if (!value) return "—";
  return dayjs(value).format("YYYY-MM-DD HH:mm:ss");
}

// countdown renders the distance to a future instant as "1 小时 5 分钟后" or,
// once it has passed, how long ago that was.
export function countdown(value: string | null | undefined, now: number): string {
  if (!value) return "等待 ping";
  const target = dayjs(value).valueOf();
  const delta = target - now;
  // Use the caller's clock here too, so the whole cell can't disagree with itself.
  if (delta <= 0) return `${humanDuration(-delta)}前到期`;
  return `${humanDuration(delta)}后到期`;
}

// humanDuration speaks weeks, days, hours and minutes only — a leftover of
// seconds is dropped rather than shown as "8 天 1 秒".
export function humanDuration(ms: number): string {
  const total = Math.floor(ms / 1000);
  if (total < 60) return `${total} 秒`;

  const units: Array<[number, string]> = [
    [604800, "周"],
    [86400, "天"],
    [3600, "小时"],
    [60, "分钟"],
  ];
  const parts: string[] = [];
  let rest = total;
  for (const [size, name] of units) {
    // A leftover day is phrased as days, never "1 周 1 天".
    if (size === 604800 && rest % size !== 0) continue;
    const value = Math.floor(rest / size);
    if (value > 0) parts.push(`${value} ${name}`);
    rest -= value * size;
    // Two units are enough to read a countdown.
    if (parts.length === 2) break;
  }
  return parts.join(" ") || "0 秒";
}
