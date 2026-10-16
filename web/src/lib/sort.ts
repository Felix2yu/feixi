import type { Display } from "./types";

export type SortKey = "status" | "name" | "due" | "last_ping";

export interface SortState {
  key: SortKey;
  desc: boolean;
}

// 排序要的值由调用方算好：派生态和到期时间在后端字典里不是最终答案。
export interface SortRow {
  name: string;
  status: Display;
  due: number | null;
  lastPing: number | null;
}

// 默认按状态倒序打开：先看到有问题的。
export const defaultSort: SortState = { key: "status", desc: true };

const rank: Record<Display, number> = {
  paused: 1,
  new: 2,
  up: 3,
  started: 4,
  grace: 5,
  down: 6,
};

const byName = (a: SortRow, b: SortRow) => a.name.localeCompare(b.name, "zh-Hans-CN");

export function sortChecks<T>(items: T[], state: SortState, rowOf: (item: T) => SortRow): T[] {
  const dir = state.desc ? -1 : 1;
  return [...items].sort((x, y) => {
    const a = rowOf(x);
    const b = rowOf(y);

    if (state.key === "due" || state.key === "last_ping") {
      const va = state.key === "due" ? a.due : a.lastPing;
      const vb = state.key === "due" ? b.due : b.lastPing;
      // 没有值的一律垫底：暂停与新建不会因为倒序被搬到最前面。
      if (va === null || vb === null) return va === vb ? byName(a, b) : va === null ? 1 : -1;
      return dir * (va - vb) || byName(a, b);
    }
    if (state.key === "name") return dir * byName(a, b);
    return dir * (rank[a.status] - rank[b.status]) || byName(a, b);
  });
}

// 同一列再点一次改成倒序，换列从正序开始。
export function nextSort(current: SortState, key: SortKey): SortState {
  return current.key === key ? { key, desc: !current.desc } : { key, desc: false };
}
