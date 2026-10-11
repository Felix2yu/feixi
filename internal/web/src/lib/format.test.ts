import { describe, expect, it } from "vitest";
import { badgeStatus, statusMeta } from "./status";
import { countdown, humanDuration } from "./time";
import type { Display } from "./types";

describe("状态文案", () => {
  // These five labels are the product's core vocabulary; renaming one by
  // accident would make the UI disagree with the docs.
  const labels: Array<[Display, string]> = [
    ["new", "新建"],
    ["up", "正常"],
    ["grace", "延迟"],
    ["down", "宕机"],
    ["paused", "暂停"],
    ["started", "运行中"],
  ];
  it.each(labels)("%s 显示为 %s", (status, label) => {
    expect(statusMeta(status).label).toBe(label);
  });

  it("grace 在徽章上叫 late，两态徽章把它算作 up", () => {
    expect(badgeStatus("grace")).toBe("late");
    expect(badgeStatus("down")).toBe("down");
    expect(badgeStatus("up")).toBe("up");
    expect(badgeStatus("new")).toBe("up");
  });

  it("未知状态退回新建的样式而不是崩掉", () => {
    expect(statusMeta("nonsense" as Display).label).toBe("新建");
  });
});

describe("时长文案", () => {
  const cases: Array<[number, string]> = [
    [500, "0 秒"],
    [45_000, "45 秒"],
    [90 * 60_000, "1 小时 30 分钟"],
    [8 * 24 * 3600_000, "8 天"],
    // 一周一天的余量要说成 8 天，不能说成 1 周 1 天
    [8 * 24 * 3600_000 + 1000, "8 天"],
    [2 * 7 * 24 * 3600_000, "2 周"],
    [23 * 3600_000 + 58 * 60_000 + 41_000, "23 小时 58 分钟"],
  ];
  it.each(cases)("%i ms 显示为 %s", (ms, want) => {
    expect(humanDuration(ms)).toBe(want);
  });
});

describe("倒计时", () => {
  const now = Date.UTC(2026, 9, 10, 12, 0, 0);

  it("没有到期时间就说在等 ping", () => {
    expect(countdown(null, now)).toBe("等待 ping");
  });

  it("未来的到期时间显示为“…后到期”", () => {
    expect(countdown("2026-10-10T13:00:00+00:00", now)).toBe("1 小时后到期");
  });

  it("已过期的时间显示为“…前到期”", () => {
    expect(countdown("2026-10-10T11:00:00+00:00", now)).toBe("1 小时前到期");
  });
});
