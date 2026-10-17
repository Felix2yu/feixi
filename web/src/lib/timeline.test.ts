import { describe, expect, it } from 'vitest';
import { buildRuns, timelineColor } from './timeline';

const DAY = 24 * 3600 * 1000;
const now = Date.UTC(2026, 9, 11, 12, 0, 0);
const at = (hoursAgo: number) => new Date(now - hoursAgo * 3600_000).toISOString();

describe('buildRuns', () => {
  it('没有事件时整段就是一种状态', () => {
    const runs = buildRuns([], 'up', now, DAY);
    expect(runs).toHaveLength(1);
    expect(runs[0].state).toBe('up');
    expect(runs[0].to - runs[0].from).toBe(DAY);
  });

  it('新建与暂停没有历史可画，留灰', () => {
    expect(buildRuns([], 'new', now, DAY)[0].state).toBeNull();
    expect(buildRuns([], 'paused', now, DAY)[0].state).toBeNull();
  });

  it('三段：事件之前无记录，之后按事件指示的状态铺满', () => {
    const runs = buildRuns(
      [
        { timestamp: at(6), up: 0 },
        { timestamp: at(3), up: 1 }
      ],
      'up',
      now,
      DAY
    );
    expect(runs.map((r) => r.state)).toEqual([null, 'down', 'up']);
    // 事件前 18 小时无记录，宕机 3 小时，恢复 3 小时
    expect(runs[0].to - runs[0].from).toBe(18 * 3600_000);
    expect(runs[1].to - runs[1].from).toBe(3 * 3600_000);
    expect(runs[2].to - runs[2].from).toBe(3 * 3600_000);
  });

  it('事件顺序乱序也要按时间排', () => {
    const runs = buildRuns(
      [
        { timestamp: at(3), up: 1 },
        { timestamp: at(6), up: 0 }
      ],
      'up',
      now,
      DAY
    );
    expect(runs.map((r) => r.state)).toEqual([null, 'down', 'up']);
  });

  it('窗口之外的事件把起点夹到窗口左边界，不留灰头', () => {
    const runs = buildRuns([{ timestamp: at(30), up: 0 }], 'down', now, DAY);
    expect(runs).toHaveLength(1);
    expect(runs[0].state).toBe('down');
    expect(runs[0].to - runs[0].from).toBe(DAY);
  });

  it('时间戳解析不了的直接丢掉，不产生零宽段', () => {
    const runs = buildRuns(
      [
        { timestamp: 'not-a-date', up: 1 },
        { timestamp: at(2), up: 0 }
      ],
      'down',
      now,
      DAY
    );
    expect(runs.map((r) => r.state)).toEqual([null, 'down']);
    expect(runs.every((r) => r.to > r.from)).toBe(true);
  });

  it('颜色表覆盖四种情况', () => {
    expect(timelineColor('up')).toBe('#4caf50');
    expect(timelineColor('down')).toBe('#e05d44');
    expect(timelineColor('late')).toBe('#fe7d37');
    expect(timelineColor(null)).toBe('#e4e7eb');
  });
});
