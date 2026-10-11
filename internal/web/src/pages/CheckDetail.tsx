import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Pause, Pencil, Play, Trash2 } from "lucide-react";
import { Fragment, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { StatusGlyph } from "../components/StatusGlyph";
import { Button, CopyButton, EmptyState, cx, useToast } from "../components/ui";
import { api } from "../lib/api";
import { buildRuns, timelineColor, type FlipEvent } from "../lib/timeline";
import { statusMeta } from "../lib/status";
import { formatDateTime, fromNow, humanDuration } from "../lib/time";
import type { Ping } from "../lib/types";

const localZone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";

export default function CheckDetail() {
  const { uuid = "" } = useParams();
  const client = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [openBody, setOpenBody] = useState<number | null>(null);

  const check = useQuery({ queryKey: ["check", uuid], queryFn: () => api.check(uuid) });
  const pings = useQuery({ queryKey: ["pings", uuid], queryFn: () => api.pings(uuid) });
  const flips = useQuery({ queryKey: ["flips", uuid], queryFn: () => api.flips(uuid, 24 * 3600) });
  const downtimes = useQuery({
    queryKey: ["downtimes", uuid],
    queryFn: () => api.downtimes(uuid, localZone),
  });

  const refresh = () => {
    client.invalidateQueries({ queryKey: ["check", uuid] });
    client.invalidateQueries({ queryKey: ["status"] });
    client.invalidateQueries({ queryKey: ["checks"] });
  };

  const pause = useMutation({
    mutationFn: () => api.pause(uuid),
    onSuccess: () => {
      refresh();
      toast("已暂停监控");
    },
    onError: (e: Error) => toast(e.message, "error"),
  });
  const resume = useMutation({
    mutationFn: () => api.resume(uuid),
    onSuccess: () => {
      refresh();
      toast("已恢复，等待新的 ping");
    },
    onError: (e: Error) => toast(e.message, "error"),
  });
  const remove = useMutation({
    mutationFn: () => api.deleteCheck(uuid),
    onSuccess: () => {
      toast("检查项已删除");
      navigate("/");
    },
    onError: (e: Error) => toast(e.message, "error"),
  });

  if (check.isLoading) return <div className="text-sm text-ink-soft">加载中…</div>;
  if (check.error) return <EmptyState>找不到这个检查项。</EmptyState>;
  const c = check.data!;
  const meta = statusMeta(c.status);

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Link to="/">
          <Button variant="ghost" size="sm">
            <ArrowLeft size={14} /> 仪表板
          </Button>
        </Link>
      </div>

      <div className="flex flex-wrap items-start gap-3 rounded-lg border border-line bg-surface p-4 dark:bg-transparent">
        <span className="mt-0.5">
          <StatusGlyph status={c.status} started={c.started} size={22} />
        </span>
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <h1 className="truncate text-lg font-semibold">{c.name || c.uuid}</h1>
            <span className={cx("rounded px-1.5 py-0.5 text-xs font-medium", meta.text)}>{meta.label}</span>
            {c.tags
              .split(/\s+/)
              .filter(Boolean)
              .map((t) => (
                <span key={t} className="rounded bg-canvas px-1.5 py-0.5 text-[11px] text-ink-soft">
                  {t}
                </span>
              ))}
          </div>
          {c.desc ? <p className="mt-1 whitespace-pre-wrap text-sm text-ink-soft">{c.desc}</p> : null}
          <div className="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-xs text-ink-soft">
            <span>
              计划：{c.timeout ? `每 ${humanDuration(c.timeout * 1000)}` : `${c.schedule}（${c.tz}）`}
            </span>
            <span>宽限期：{humanDuration(c.grace * 1000)}</span>
            <span>下次到期：{c.next_ping ? `${formatDateTime(c.next_ping)} · ${fromNow(c.next_ping)}到期` : "等待 ping"}</span>
            <span>最后 ping：{formatDateTime(c.last_ping)}</span>
            {c.last_duration ? <span>上次运行：{humanDuration(c.last_duration * 1000)}</span> : null}
            <span>累计 ping：{c.n_pings}</span>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {c.status === "paused" ? (
            <Button size="sm" onClick={() => resume.mutate()}>
              <Play size={14} /> 恢复
            </Button>
          ) : (
            <Button size="sm" onClick={() => pause.mutate()}>
              <Pause size={14} /> 暂停
            </Button>
          )}
          <Link to={`/checks/${c.uuid}/edit`}>
            <Button size="sm">
              <Pencil size={14} /> 编辑
            </Button>
          </Link>
          <Button
            size="sm"
            variant="danger"
            onClick={() => {
              if (window.confirm("删除这个检查项？")) remove.mutate();
            }}
          >
            <Trash2 size={14} />
          </Button>
        </div>
      </div>

      <div className="space-y-2 rounded-lg border border-line bg-surface p-4 dark:bg-transparent">
        <h2 className="text-sm font-semibold">Ping 地址</h2>
        <div className="flex items-center gap-2">
          <input readOnly value={c.ping_url} className="min-w-0 flex-1 truncate rounded-md border border-line bg-canvas px-2 py-1 font-mono text-xs" />
          <CopyButton value={c.ping_url} label="复制" />
        </div>
        <div className="flex items-center gap-2">
          <input readOnly value={c.uuid} className="min-w-0 flex-1 truncate rounded-md border border-line bg-canvas px-2 py-1 font-mono text-xs" />
          <CopyButton value={c.uuid} label="复制 UUID" />
        </div>
        <p className="text-xs text-ink-soft">
          信号：<code>/ping/…</code> 成功 · <code>/start</code> 开始 · <code>/log</code> 记录 · <code>/fail</code> 或{" "}
          <code>/&lt;exitstatus&gt;</code> 失败
        </p>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <div className="space-y-2 rounded-lg border border-line bg-surface p-4 dark:bg-transparent">
          <h2 className="text-sm font-semibold">最近 24 小时</h2>
          <Timeline flips={flips.data?.flips ?? []} status={c.status} />
        </div>
        <div className="space-y-2 rounded-lg border border-line bg-surface p-4 dark:bg-transparent">
          <h2 className="text-sm font-semibold">月度可用率（{localZone}）</h2>
          {downtimes.data ? (
            <div className="flex flex-wrap gap-1.5">
              {downtimes.data.downtimes.map((d) => (
                <div
                  key={d.boundary}
                  title={
                    d.no_data
                      ? "检查项当时还不存在"
                      : `${d.count} 次宕机，共 ${humanDuration(d.seconds * 1000)} · ${(d.uptime * 100).toFixed(2)}% 可用`
                  }
                  className={cx(
                    "w-20 rounded-md border px-2 py-1.5 text-center text-[11px]",
                    d.no_data ? "border-dashed border-line text-ink-soft" : uptimeTone(d.uptime),
                  )}
                >
                  <div className="font-medium">{d.boundary.slice(0, 7)}</div>
                  <div>{d.no_data ? "无数据" : `${(d.uptime * 100).toFixed(1)}%`}</div>
                </div>
              ))}
            </div>
          ) : (
            <div className="text-xs text-ink-soft">加载中…</div>
          )}
        </div>
      </div>

      <div className="space-y-2 rounded-lg border border-line bg-surface p-4 dark:bg-transparent">
        <h2 className="text-sm font-semibold">Ping 记录</h2>
        {(pings.data?.pings ?? []).length === 0 ? (
          <EmptyState>从未收到 ping。</EmptyState>
        ) : (
          <table className="w-full text-xs">
            <thead className="text-left text-ink-soft">
              <tr>
                <th className="py-1 font-medium">#</th>
                <th className="py-1 font-medium">类型</th>
                <th className="py-1 font-medium">时间</th>
                <th className="py-1 font-medium">时长</th>
                <th className="py-1 font-medium">方法</th>
                <th className="py-1 font-medium">来源</th>
                <th className="py-1 font-medium">UA</th>
                <th className="py-1" />
              </tr>
            </thead>
            <tbody>
              {(pings.data?.pings ?? []).map((p: Ping) => (
                <Fragment key={p.n}>
                  <tr className="border-t border-line">
                    <td className="py-1 text-ink-soft">{p.n}</td>
                    <td className="py-1">{kindLabel(p.type)}</td>
                    <td className="py-1">{formatDateTime(p.date)}</td>
                    <td className="py-1">{p.duration ? humanDuration(p.duration * 1000) : "—"}</td>
                    <td className="py-1">{p.method || "GET"}</td>
                    <td className="py-1 font-mono">{p.remote_addr}</td>
                    <td className="max-w-40 truncate py-1 text-ink-soft" title={p.ua}>
                      {p.ua}
                    </td>
                    <td className="py-1 text-right">
                      {p.body_url ? (
                        <button className="underline" onClick={() => setOpenBody(openBody === p.n ? null : p.n)}>
                          {openBody === p.n ? "收起" : "请求体"}
                        </button>
                      ) : null}
                    </td>
                  </tr>
                  {openBody === p.n ? <BodyRow uuid={c.uuid} n={p.n} /> : null}
                </Fragment>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function BodyRow({ uuid, n }: { uuid: string; n: number }) {
  const body = useQuery({ queryKey: ["ping-body", uuid, n], queryFn: () => api.pingBody(uuid, n) });
  return (
    <tr>
      <td colSpan={8} className="bg-canvas px-2 py-2">
        {body.isLoading ? (
          <span className="text-xs text-ink-soft">读取中…</span>
        ) : (
          <pre className="max-h-48 overflow-auto whitespace-pre-wrap text-[11px]">{body.data || "（空）"}</pre>
        )}
      </td>
    </tr>
  );
}

function kindLabel(kind: Ping["type"]): string {
  return { success: "成功", start: "开始", log: "日志", fail: "失败", ign: "忽略" }[kind] ?? kind;
}

// Timeline renders the window as continuous spans: one block per state, width
// proportional to how long it lasted. Rasterising it into fixed cells made three
// real transitions look like a hundred events.
function Timeline({ flips, status }: { flips: FlipEvent[]; status: string }) {
  const windowMs = 24 * 3600 * 1000;
  const now = Date.now();
  const runs = buildRuns(flips, status, now, windowMs);

  return (
    <div className="flex h-8 items-stretch overflow-hidden rounded">
      {runs.map((r, i) => (
        <span
          key={i}
          title={`${labelFor(r.state)} · ${humanDuration(r.to - r.from)}`}
          style={{ background: timelineColor(r.state), flexGrow: (r.to - r.from) / windowMs }}
        />
      ))}
    </div>
  );
}

function labelFor(state: string | null): string {
  return state === "up" ? "正常" : state === "down" ? "宕机" : state === "late" ? "延迟" : "无记录";
}

function uptimeTone(uptime: number): string {
  if (uptime >= 0.999) return "border-up/40 bg-up/10 text-ink";
  if (uptime >= 0.99) return "border-late/40 bg-late/10 text-ink";
  return "border-down/40 bg-down/10 text-ink";
}
