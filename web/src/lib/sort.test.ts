import { describe, expect, it } from 'vitest';
import { defaultSort, nextSort, sortChecks, type SortRow, type SortState } from './sort';

const row = (
  name: string,
  status: SortRow['status'],
  due: number | null = null,
  lastPing: number | null = null
) => ({
  name,
  status,
  due,
  lastPing
});

// 四种状态、有值与空值的时间各来一个。
const items = [
  row('backups', 'up', 3000, 9000),
  row('cleanup', 'down', 1000, 8000),
  row('scraper', 'paused', null, null),
  row('sync', 'grace', 2000, 7000)
];

const names = (list: SortRow[]) => list.map((i) => i.name).join(',');
const identity = (i: SortRow) => i;

describe('sortChecks', () => {
  it('名称列按字母序，倒序整个反过来', () => {
    expect(names(sortChecks(items, { key: 'name', desc: false }, identity))).toBe(
      'backups,cleanup,scraper,sync'
    );
    expect(names(sortChecks(items, { key: 'name', desc: true }, identity))).toBe(
      'sync,scraper,cleanup,backups'
    );
  });

  it('状态列倒序把宕机顶到最前，正序把暂停垫在最前', () => {
    expect(names(sortChecks(items, { key: 'status', desc: true }, identity))).toBe(
      'cleanup,sync,backups,scraper'
    );
    expect(names(sortChecks(items, { key: 'status', desc: false }, identity))).toBe(
      'scraper,backups,sync,cleanup'
    );
  });

  it('没有到期时间或没有 ping 的永远垫底，倒序也不搬', () => {
    expect(names(sortChecks(items, { key: 'due', desc: false }, identity))).toBe(
      'cleanup,sync,backups,scraper'
    );
    expect(names(sortChecks(items, { key: 'due', desc: true }, identity))).toBe(
      'backups,sync,cleanup,scraper'
    );
    expect(names(sortChecks(items, { key: 'last_ping', desc: true }, identity))).toBe(
      'backups,cleanup,sync,scraper'
    );
  });

  it('同一时间戳时按名称定序，避免每次刷新都在换位置', () => {
    const tied = [row('second', 'up', null, 1000), row('first', 'up', null, 1000)];
    expect(names(sortChecks(tied, { key: 'last_ping', desc: true }, identity))).toBe(
      'first,second'
    );
  });
});

describe('nextSort', () => {
  it('同一列再点一次翻方向，换列回到正序', () => {
    const first = nextSort(defaultSort, 'name');
    expect(first).toEqual<SortState>({ key: 'name', desc: false });
    expect(nextSort(first, 'name')).toEqual<SortState>({ key: 'name', desc: true });
  });
});
