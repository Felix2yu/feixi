import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Pause, Pencil, Play, Plus, Trash2 } from "lucide-react";
import { useState } from "react";
import { StatusGlyph } from "../components/StatusGlyph";
import { Button, CopyButton, EmptyState, Select, cx, useToast } from "../components/ui";
import { api } from "../lib/api";
import { countdown, fromNow, humanDuration } from "../lib/time";
import type { Check, Display } from "../lib/types";
import { useStatusFeed } from "../lib/useStatus";

function plan(c: Check): string {
  if (c.timeout) return `每 ${humanDuration(c.timeout * 1000)}`;
  if (c.schedule) return `${c.schedule}（${c.tz}）`;
  return "—";
}

// dueCell wording per state: only a live check has something to count down to.
function dueCell(status: Display, started: boolean, next: string | null, now: number): string {
  if (started) return "运行中";
  if (status === "new") return "等待 ping";
  if (status === "paused") return "已暂停";
  if (status === "down") return "已宕机";
  if (!next) return "—";
  return countdown(next, now);
}

export default function Dashboard() {
  const [search, setSearch] = useState("");
  const [tags, setTags] = useState<string[]>([]);
  const [statusFilter, setStatusFilter] = useState("");
  const { feed } = useStatusFeed();
  const checks = useQuery({ queryKey: ["checks"], queryFn: api.checks });
  const client = useQueryClient();
  const toast = useToast();

  const pause = useMutation({
    mutationFn: (c: Check) => api.pause(c.uuid),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["checks"] });
      client.invalidateQueries({ queryKey: ["status"] });
      toast("已暂停监控");
    },
    onError: (e: Error) => toast(e.message, "error"),
  });

  const resume = useMutation({
    mutationFn: (c: Check) => api.resume(c.uuid),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["checks"] });
      toast("已恢复，等待新的 ping");
    },
    onError: (e: Error) => toast(e.message, "error"),
  });

  const remove = useMutation({
    mutationFn: (c: Check) => api.deleteCheck(c.uuid),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ["checks"] });
      toast("检查项已删除");
    },
    onError: (e: Error) => toast(e.message, "error"),
  });

  if (checks.isLoading) return <div className="text-sm text-ink-soft">加载中…</div>;
  const all = checks.data?.checks ?? [];

  const allTags = Array.from(new Set(all.flatMap((c) => c.tags.split(/\s+/).filter(Boolean)))).sort();

  const live = new Map((feed?.details ?? []).map((d) => [d.code, d]));
  const visible = all.filter((c) => {
    const status = live.get(c.uuid)?.status ?? c.status;
    const needle = search.trim().toLowerCase();
    if (needle && !`${c.name} ${c.tags} ${c.slug} ${c.uuid}`.toLowerCase().includes(needle)) return false;
    if (tags.length && !tags.every((t) => c.tags.split(/\s+/).includes(t))) return false;
    if (statusFilter === "started") return !!live.get(c.uuid)?.started;
    if (statusFilter && status !== statusFilter) return false;
    return true;
  });

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <input
          value={search}
          onChange={(e) => setSearch(e.target.value)}
          placeholder="搜索名称、标签或 UUID"
          className="min-w-40 flex-1 rounded-md border border-line bg-surface px-2.5 py-1.5 text-sm outline-none focus:border-ink-soft dark:bg-transparent"
        />
        <Select className="w-36!" value={statusFilter} onChange={(e) => setStatusFilter(e.target.value)} aria-label="按状态筛选">
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
                onClick={() => setTags((prev) => (on ? prev.filter((t) => t !== tag) : [...prev, tag]))}
                className={cx(
                  "rounded-full border px-2 py-0.5 text-xs",
                  on ? "border-ink bg-ink text-white" : "border-line bg-surface text-ink-soft",
                )}
              >
                {status ? <span className={cx("mr-1 inline-block h-1.5 w-1.5 rounded-full", dotClass(status))} /> : null}
                {tag}
              </button>
            );
          })}
        </div>
      ) : null}

      {visible.length === 0 ? (
        <EmptyState>
          {all.length === 0 ? "还没有检查项，点右上角新建一个。" : "没有匹配的检查项。"}
        </EmptyState>
      ) : (
        <div className="overflow-hidden rounded-lg border border-line bg-surface dark:bg-transparent">
          <div className="hidden items-center gap-3 border-b border-line px-3 py-2 text-xs font-medium text-ink-soft sm:flex">
            <span className="w-4" />
            <span className="min-w-0 flex-1">名称 / Ping 地址</span>
            <span className="hidden w-24 shrink-0 sm:block">计划</span>
            <span className="hidden w-24 shrink-0 sm:block">下次到期</span>
            <span className="hidden w-20 shrink-0 sm:block">最后 ping</span>
            <span className="w-32 text-right">操作</span>
          </div>
          {visible.map((c) => {
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
                    <Link to={`/checks/${c.uuid}`} className="truncate font-medium hover:underline">
                      {c.name || c.uuid}
                    </Link>
                    {/* 标签在窄屏让位给名称：详情与筛选都还能用 */}
                    <span className="hidden shrink-0 items-center gap-1 sm:flex">
                      {c.tags
                        .split(/\s+/)
                        .filter(Boolean)
                        .map((t) => (
                          <span key={t} className="rounded bg-canvas px-1.5 py-0.5 text-[11px] text-ink-soft">
                            {t}
                          </span>
                        ))}
                    </span>
                  </div>
                  <div className="truncate font-mono text-[11px] text-ink-soft">
                    {c.slug ? `/ping/${c.slug}` : `/ping/${c.uuid}`}
                  </div>
                </div>
                <span className="hidden w-24 shrink-0 truncate text-xs text-ink-soft sm:block" title={plan(c)}>
                  {plan(c)}
                </span>
                <span className="hidden w-24 shrink-0 truncate text-xs sm:block" title={c.next_ping ?? ""}>
                  {due}
                </span>
                <span className="hidden w-20 shrink-0 truncate text-xs text-ink-soft sm:block" title={fromNow(d?.last_ping ?? c.last_ping)}>
                  {fromNow(d?.last_ping ?? c.last_ping)}
                </span>
                <div className="ml-auto flex shrink-0 items-center justify-end gap-1">
                  <CopyButton value={c.ping_url} />
                  {status === "paused" ? (
                    <Button size="sm" variant="ghost" title="恢复监控" onClick={() => resume.mutate(c)}>
                      <Play size={14} />
                    </Button>
                  ) : (
                    <Button size="sm" variant="ghost" title="暂停监控" onClick={() => pause.mutate(c)}>
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
    up: "bg-up",
    grace: "bg-late",
    down: "bg-down",
    new: "bg-idle",
    paused: "bg-idle",
    started: "bg-idle",
  };
  return map[status] ?? "bg-idle";
}
