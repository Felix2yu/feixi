import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Link } from 'react-router-dom';
import { ArrowDown, ArrowUp, Pause, Pencil, Play, Plus, Trash2 } from 'lucide-react';
import { useState } from 'react';
import { StatusGlyph } from '../components/StatusGlyph';
import { Button, CopyButton, EmptyState, Select, cx, useToast } from '../components/ui';
import { api } from '../lib/api';
import { defaultSort, nextSort, sortChecks, type SortKey, type SortState } from '../lib/sort';
import { countdown, fromNow, humanDuration } from '../lib/time';
import type { Check, Display } from '../lib/types';
import { useStatusFeed } from '../lib/useStatus';

const SORT_KEY = 'feixi-sort';

// 排序选择留在本地：仪表板每次刷新都重画，不该被服务端参数牵着走。
function initialSort(): SortState {
  const raw = localStorage.getItem(SORT_KEY);
  if (!raw) return defaultSort;
  try {
    const parsed = JSON.parse(raw) as SortState;
    if (typeof parsed?.key === 'string' && typeof parsed?.desc === 'boolean') return parsed;
  } catch {
    // 存坏了就回到默认
  }
  return defaultSort;
}

const stamp = (iso: string | null | undefined): number | null => {
  if (!iso) return null;
  const ms = Date.parse(iso);
  return Number.isNaN(ms) ? null : ms;
};

function plan(c: Check): string {
  if (c.timeout) return `每 ${humanDuration(c.timeout * 1000)}`;
  if (c.schedule) return `${c.schedule}（${c.tz}）`;
  return '—';
}

// dueCell wording per state: only a live check has something to count down to.
function dueCell(status: Display, started: boolean, next: string | null, now: number): string {
  if (started) return '运行中';
  if (status === 'new') return '等待 ping';
  if (status === 'paused') return '已暂停';
  if (status === 'down') return '已宕机';
  if (!next) return '—';
  return countdown(next, now);
}

export default function Dashboard() {
  const [search, setSearch] = useState('');
  const [tags, setTags] = useState<string[]>([]);
  const [statusFilter, setStatusFilter] = useState('');
  const [sort, setSort] = useState<SortState>(initialSort);
  const [selected, setSelected] = useState<string[]>([]);
  const { feed } = useStatusFeed();
  const checks = useQuery({ queryKey: ['checks'], queryFn: api.checks });
  const client = useQueryClient();
  const toast = useToast();

  const changeSort = (key: SortKey) => {
    const next = nextSort(sort, key);
    setSort(next);
    localStorage.setItem(SORT_KEY, JSON.stringify(next));
  };

  const pause = useMutation({
    mutationFn: (c: Check) => api.pause(c.uuid),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ['checks'] });
      client.invalidateQueries({ queryKey: ['status'] });
      toast('已暂停监控');
    },
    onError: (e: Error) => toast(e.message, 'error')
  });

  const resume = useMutation({
    mutationFn: (c: Check) => api.resume(c.uuid),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ['checks'] });
      toast('已恢复，等待新的 ping');
    },
    onError: (e: Error) => toast(e.message, 'error')
  });

  const remove = useMutation({
    mutationFn: (c: Check) => api.deleteCheck(c.uuid),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ['checks'] });
      toast('检查项已删除');
    },
    onError: (e: Error) => toast(e.message, 'error')
  });

  const bulkActions = {
    pause: (c: Check) => api.pause(c.uuid),
    resume: (c: Check) => api.resume(c.uuid),
    remove: (c: Check) => api.deleteCheck(c.uuid),
    clear: (c: Check) => api.clearEvents(c.uuid)
  };
  const bulkLabels: Record<keyof typeof bulkActions, string> = {
    pause: '已暂停',
    resume: '已恢复',
    remove: '已删除',
    clear: '已清空历史'
  };

  const bulk = useMutation({
    mutationFn: async (action: keyof typeof bulkActions) => {
      const targets = selected
        .map((uuid) => all.find((c) => c.uuid === uuid))
        .filter((c): c is Check => !!c);
      // 只对合用的下手：恢复只发给暂停中的，暂停只发给还活着的。
      const picked =
        action === 'pause'
          ? targets.filter((c) => c.status !== 'paused')
          : action === 'resume'
            ? targets.filter((c) => c.status === 'paused')
            : targets;
      const run: (c: Check) => Promise<unknown> = bulkActions[action];
      const results = await Promise.allSettled(picked.map((c) => run(c)));
      const ok = results.filter((r) => r.status === 'fulfilled').length;
      return { ok, failed: results.length - ok };
    },
    onSuccess: ({ ok, failed }, action) => {
      const done = bulkLabels[action];
      toast(
        failed > 0 ? `${done} ${ok} 项，失败 ${failed} 项` : `${done} ${ok} 项`,
        failed > 0 ? 'error' : 'ok'
      );
      setSelected([]);
      client.invalidateQueries({ queryKey: ['checks'] });
      client.invalidateQueries({ queryKey: ['status'] });
    },
    onError: (e: Error) => toast(e.message, 'error')
  });

  if (checks.isLoading) return <div className="text-sm text-ink-soft">加载中…</div>;
  const all = checks.data?.checks ?? [];

  const allTags = Array.from(
    new Set(all.flatMap((c) => c.tags.split(/\s+/).filter(Boolean)))
  ).sort();

  const live = new Map((feed?.details ?? []).map((d) => [d.code, d]));
  const visible = all.filter((c) => {
    const status = live.get(c.uuid)?.status ?? c.status;
    const needle = search.trim().toLowerCase();
    if (needle && !`${c.name} ${c.tags} ${c.slug} ${c.uuid}`.toLowerCase().includes(needle))
      return false;
    if (tags.length && !tags.every((t) => c.tags.split(/\s+/).includes(t))) return false;
    if (statusFilter === 'started') return !!live.get(c.uuid)?.started;
    if (statusFilter && status !== statusFilter) return false;
    return true;
  });

  const shown = sortChecks(visible, sort, (c) => {
    const d = live.get(c.uuid);
    const started = d?.started ?? c.started;
    return {
      name: c.name || c.uuid,
      status: started ? 'started' : (d?.status ?? c.status),
      due: stamp(d?.next_ping ?? c.next_ping),
      lastPing: stamp(d?.last_ping ?? c.last_ping)
    };
  });
  const selectedVisible = shown.filter((c) => selected.includes(c.uuid));
  const allSelected = shown.length > 0 && selectedVisible.length === shown.length;

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="搜索名称、标签或 UUID"
          className="min-w-40 flex-1 rounded-md border border-line bg-surface px-2.5 py-1.5 text-sm outline-none focus:border-ink-soft dark:bg-transparent"
        />
        <Select
          className="w-36!"
          value={statusFilter}
          onChange={(e) => setStatusFilter(e.target.value)}
          aria-label="按状态筛选"
        >
          <option value="">全部状态</option>
          <option value="new">新建</option>
          <option value="paused">已暂停</option>
          <option value="started">运行中</option>
          <option value="up">正常</option>
          <option value="grace">延迟</option>
          <option value="down">宕机</option>
        </Select>
        <Link to="/checks/new">
          <Button variant="primary" size="md">
            <Plus size={16} /> 新建检查项
          </Button>
        </Link>
      </div>

      {allTags.length > 0 ? (
        <div className="flex flex-wrap items-center gap-1.5">
          {allTags.map((tag) => {
            const status = feed?.tags[tag];
            const on = tags.includes(tag);
            return (
              <button
                key={tag}
                onClick={() =>
                  setTags((prev) => (on ? prev.filter((t) => t !== tag) : [...prev, tag]))
                }
                className={cx(
                  'rounded-full border px-2 py-0.5 text-xs',
                  on
                    ? 'border-ink bg-accent text-accent-ink'
                    : 'border-line bg-surface text-ink-soft'
                )}
              >
                {status ? (
                  <span
                    className={cx('mr-1 inline-block h-1.5 w-1.5 rounded-full', dotClass(status))}
                  />
                ) : null}
                {tag}
              </button>
            );
          })}
        </div>
      ) : null}

      {selected.length > 0 ? (
        <div className="flex items-center gap-2 rounded-lg border border-line bg-surface px-3 py-1.5 text-xs dark:bg-transparent">
          <span className="shrink-0 font-medium">已选 {selected.length} 项</span>
          <Button size="sm" onClick={() => bulk.mutate('pause')} disabled={bulk.isPending}>
            <Pause size={13} /> 暂停
          </Button>
          <Button size="sm" onClick={() => bulk.mutate('resume')} disabled={bulk.isPending}>
            <Play size={13} /> 恢复
          </Button>
          <Button
            size="sm"
            title={`删除选中的 ${selected.length} 项检查项？`}
            variant="danger"
            disabled={bulk.isPending}
            onClick={() => {
              if (window.confirm(`删除选中的 ${selected.length} 项检查项？`)) bulk.mutate('remove');
            }}
          >
            <Trash2 size={13} />
          </Button>
          <Button size="sm" variant="ghost" className="ml-auto" onClick={() => setSelected([])}>
            取消
          </Button>
        </div>
      ) : null}

      {visible.length === 0 ? (
        <EmptyState>
          {all.length === 0 ? '还没有检查项，点右上角新建一个。' : '没有匹配的检查项。'}
        </EmptyState>
      ) : (
        <div className="overflow-hidden rounded-lg border border-line bg-surface dark:bg-transparent">
          <div className="flex items-center gap-3 border-b border-line px-3 py-2 text-xs font-medium text-ink-soft">
            <SortHeader
              label=""
              hint="按状态排序"
              sortKey="status"
              current={sort}
              onChange={changeSort}
              className="flex w-4 shrink-0 items-center justify-center"
            />
            <div className="flex min-w-0 flex-1 items-center gap-2">
              <input
                type="checkbox"
                aria-label="全选"
                checked={allSelected}
                onChange={() => setSelected(allSelected ? [] : shown.map((c) => c.uuid))}
                className="size-3.5 shrink-0"
              />
              <SortHeader
                label="名称"
                hint="按名称排序"
                sortKey="name"
                current={sort}
                onChange={changeSort}
                className="flex items-center gap-1"
              />
              <span className="hidden truncate font-normal sm:inline">/ Ping 地址</span>
            </div>
            <span className="hidden w-24 shrink-0 sm:block">计划</span>
            <SortHeader
              label="下次到期"
              hint="按下次到期排序"
              sortKey="due"
              current={sort}
              onChange={changeSort}
              className="hidden w-24 shrink-0 items-center gap-1 sm:flex"
            />
            <SortHeader
              label="最后 ping"
              hint="按最后 ping 排序"
              sortKey="last_ping"
              current={sort}
              onChange={changeSort}
              className="hidden w-20 shrink-0 items-center gap-1 sm:flex"
            />
            <span className="w-32 text-right">操作</span>
          </div>
          {shown.map((c) => {
            const d = live.get(c.uuid);
            const status = d?.status ?? c.status;
            const started = d?.started ?? c.started;
            const due = dueCell(status, started, d?.next_ping ?? c.next_ping, Date.now());
            return (
              <div
                key={c.uuid}
                className="flex items-center gap-3 border-b border-line px-3 py-2 last:border-b-0"
              >
                <span className="flex w-4 shrink-0 justify-center">
                  <StatusGlyph status={status} started={started} />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex min-w-0 items-center gap-2">
                    <input
                      type="checkbox"
                      aria-label={`选中 ${c.name || c.uuid}`}
                      checked={selected.includes(c.uuid)}
                      onChange={(e) =>
                        setSelected((prev) =>
                          e.target.checked ? [...prev, c.uuid] : prev.filter((u) => u !== c.uuid)
                        )
                      }
                      className="size-3.5 shrink-0"
                    />
                    <Link to={`/checks/${c.uuid}`} className="truncate font-medium hover:underline">
                      {c.name || c.uuid}
                    </Link>
                    {/* 标签在窄屏让位给名称：详情与筛选都还能用 */}
                    <span className="hidden shrink-0 items-center gap-1 sm:flex">
                      {c.tags
                        .split(/\s+/)
                        .filter(Boolean)
                        .map((t) => (
                          <span
                            key={t}
                            className="rounded bg-canvas px-1.5 py-0.5 text-[11px] text-ink-soft"
                          >
                            {t}
                          </span>
                        ))}
                    </span>
                  </div>
                  <div className="truncate font-mono text-[11px] text-ink-soft">
                    {c.slug ? `/ping/${c.slug}` : `/ping/${c.uuid}`}
                  </div>
                </div>
                <span
                  className="hidden w-24 shrink-0 truncate text-xs text-ink-soft sm:block"
                  title={plan(c)}
                >
                  {plan(c)}
                </span>
                <span
                  className="hidden w-24 shrink-0 truncate text-xs sm:block"
                  title={c.next_ping ?? ''}
                >
                  {due}
                </span>
                <span
                  className="hidden w-20 shrink-0 truncate text-xs text-ink-soft sm:block"
                  title={fromNow(d?.last_ping ?? c.last_ping)}
                >
                  {fromNow(d?.last_ping ?? c.last_ping)}
                </span>
                <div className="ml-auto flex shrink-0 items-center justify-end gap-1">
                  <CopyButton value={c.ping_url} />
                  {status === 'paused' ? (
                    <Button
                      size="sm"
                      variant="ghost"
                      title="恢复监控"
                      onClick={() => resume.mutate(c)}
                    >
                      <Play size={14} />
                    </Button>
                  ) : (
                    <Button
                      size="sm"
                      variant="ghost"
                      title="暂停监控"
                      onClick={() => pause.mutate(c)}
                    >
                      <Pause size={14} />
                    </Button>
                  )}
                  <Link to={`/checks/${c.uuid}/edit`}>
                    <Button size="sm" variant="ghost" title="编辑">
                      <Pencil size={14} />
                    </Button>
                  </Link>
                  <Button
                    size="sm"
                    variant="ghost"
                    title="删除"
                    onClick={() => {
                      if (window.confirm(`删除检查项“${c.name || c.uuid}”？`)) remove.mutate(c);
                    }}
                  >
                    <Trash2 size={14} className="text-down" />
                  </Button>
                </div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function dotClass(status: Display): string {
  const map: Record<string, string> = {
    up: 'bg-up',
    grace: 'bg-late',
    down: 'bg-down',
    new: 'bg-idle',
    paused: 'bg-idle',
    started: 'bg-idle'
  };
  return map[status] ?? 'bg-idle';
}

// SortHeader 只画文字与箭头，显示方式（含窄屏隐藏）由调用方的 className 决定。
function SortHeader({
  label,
  hint,
  sortKey,
  current,
  onChange,
  className
}: {
  label: string;
  hint: string;
  sortKey: SortKey;
  current: SortState;
  onChange: (key: SortKey) => void;
  className: string;
}) {
  const on = current.key === sortKey;
  return (
    <button
      type="button"
      title={hint}
      aria-label={hint}
      onClick={() => onChange(sortKey)}
      className={cx('hover:text-ink', on && 'text-ink', className)}
    >
      {label ? <span>{label}</span> : null}
      {on ? (
        current.desc ? (
          <ArrowDown size={11} />
        ) : (
          <ArrowUp size={11} />
        )
      ) : (
        <ArrowDown size={11} className="opacity-30" />
      )}
    </button>
  );
}
