import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Activity,
  ArrowDownCircle,
  ArrowLeft,
  ArrowUpCircle,
  BellRing,
  Eraser,
  Pause,
  Pencil,
  Play,
  PauseCircle,
  RefreshCw,
  Trash2
} from 'lucide-react';
import { Fragment, useState } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import { StatusGlyph } from '../components/StatusGlyph';
import { Button, CopyButton, EmptyState, cx, useToast } from '../components/ui';
import { api } from '../lib/api';
import { buildRuns, timelineColor, type FlipEvent } from '../lib/timeline';
import { statusMeta } from '../lib/status';
import { formatDateTime, fromNow, humanDuration } from '../lib/time';
import type { Ping, StreamEvent } from '../lib/types';

const localZone = Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';

// 地址后缀就是信号本身，所以直接列出可复制的完整地址，比写 &lt;exitstatus&gt; 这种占位符清楚。
const SIGNALS: Array<{ suffix: string; meaning: string }> = [
  { suffix: '/success', meaning: '成功：任务按时跑完' },
  { suffix: '/start', meaning: '开始运行，配合 ?rid= 可算出运行时长' },
  { suffix: '/log', meaning: '记一条日志，不改变状态' },
  { suffix: '/fail', meaning: '失败' },
  { suffix: '/2', meaning: '失败并记下退出码 2；脚本里写 /$? 接真实退出码，非 0 都算失败' }
];

export default function CheckDetail() {
  const { uuid = '' } = useParams();
  const client = useQueryClient();
  const toast = useToast();
  const navigate = useNavigate();
  const [openBody, setOpenBody] = useState<number | null>(null);

  const check = useQuery({ queryKey: ['check', uuid], queryFn: () => api.check(uuid) });
  const pings = useQuery({ queryKey: ['pings', uuid], queryFn: () => api.pings(uuid) });
  const flips = useQuery({ queryKey: ['flips', uuid], queryFn: () => api.flips(uuid, 24 * 3600) });
  const downtimes = useQuery({
    queryKey: ['downtimes', uuid],
    queryFn: () => api.downtimes(uuid, localZone)
  });
  const events = useQuery({
    queryKey: ['events', uuid],
    queryFn: () => api.events(uuid),
    refetchInterval: 15000
  });

  const retryNotice = useMutation({
    mutationFn: (code: string) => api.retryNotification(code),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ['events', uuid] });
      client.invalidateQueries({ queryKey: ['notifications'] });
      toast('已重新发送');
    },
    onError: (e: Error) => toast(e.message, 'error')
  });

  const refresh = () => {
    client.invalidateQueries({ queryKey: ['check', uuid] });
    client.invalidateQueries({ queryKey: ['status'] });
    client.invalidateQueries({ queryKey: ['checks'] });
  };

  const pause = useMutation({
    mutationFn: () => api.pause(uuid),
    onSuccess: () => {
      refresh();
      toast('已暂停监控');
    },
    onError: (e: Error) => toast(e.message, 'error')
  });
  const resume = useMutation({
    mutationFn: () => api.resume(uuid),
    onSuccess: () => {
      refresh();
      toast('已恢复，等待新的 ping');
    },
    onError: (e: Error) => toast(e.message, 'error')
  });
  const remove = useMutation({
    mutationFn: () => api.deleteCheck(uuid),
    onSuccess: () => {
      toast('检查项已删除');
      navigate('/');
    },
    onError: (e: Error) => toast(e.message, 'error')
  });
  const clear = useMutation({
    mutationFn: () => api.clearEvents(uuid),
    onSuccess: () => {
      refresh();
      client.invalidateQueries({ queryKey: ['pings', uuid] });
      client.invalidateQueries({ queryKey: ['events', uuid] });
      toast('已清空历史，等待新的 ping');
    },
    onError: (e: Error) => toast(e.message, 'error')
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
            <span className={cx('rounded px-1.5 py-0.5 text-xs font-medium', meta.text)}>
              {meta.label}
            </span>
            {c.flapping ? (
              <span
                className="rounded bg-late/15 px-1.5 py-0.5 text-xs font-medium text-late"
                title="状态频繁翻转，逐条告警已合并"
              >
                抖动中
              </span>
            ) : null}
            {c.failure_count ? (
              <span
                className="rounded bg-late/15 px-1.5 py-0.5 text-xs font-medium text-late"
                title={`连续失败 ${c.failure_count} 次${c.failure_tolerance ? `，容忍 ${c.failure_tolerance} 次` : ''}`}
              >
                连续失败 {c.failure_count}
                {c.failure_tolerance ? `/${c.failure_tolerance}` : ''}
              </span>
            ) : null}
            {c.tags
              .split(/\s+/)
              .filter(Boolean)
              .map((t) => (
                <span key={t} className="rounded bg-canvas px-1.5 py-0.5 text-[11px] text-ink-soft">
                  {t}
                </span>
              ))}
          </div>
          {c.desc ? (
            <p className="mt-1 text-sm whitespace-pre-wrap text-ink-soft">{c.desc}</p>
          ) : null}
          <div className="mt-2 flex flex-wrap gap-x-5 gap-y-1 text-xs text-ink-soft">
            <span>
              计划：
              {c.timeout ? `每 ${humanDuration(c.timeout * 1000)}` : `${c.schedule}（${c.tz}）`}
            </span>
            <span>宽限期：{humanDuration(c.grace * 1000)}</span>
            <span>
              下次到期：
              {c.next_ping
                ? `${formatDateTime(c.next_ping)} · ${fromNow(c.next_ping)}到期`
                : '等待 ping'}
            </span>
            <span>最后 ping：{formatDateTime(c.last_ping)}</span>
            {c.last_duration ? (
              <span>上次运行：{humanDuration(c.last_duration * 1000)}</span>
            ) : null}
            <span>累计 ping：{c.n_pings}</span>
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          {c.status === 'paused' ? (
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
            title="删掉全部 ping 记录，状态回到「新建」，宕机统计保留"
            onClick={() => {
              if (window.confirm('清空历史 ping？状态回到「新建」，宕机统计保留。')) clear.mutate();
            }}
          >
            <Eraser size={14} /> 清空历史
          </Button>
          <Button
            size="sm"
            variant="danger"
            onClick={() => {
              if (window.confirm('删除这个检查项？')) remove.mutate();
            }}
          >
            <Trash2 size={14} />
          </Button>
        </div>
      </div>

      <div className="space-y-2 rounded-lg border border-line bg-surface p-4 dark:bg-transparent">
        <h2 className="text-sm font-semibold">Ping 地址</h2>
        <div className="flex items-center gap-2">
          <input
            readOnly
            value={c.ping_url}
            className="min-w-0 flex-1 truncate rounded-md border border-line bg-canvas px-2 py-1 font-mono text-xs"
          />
          <CopyButton value={c.ping_url} label="复制" />
        </div>
        <div className="flex items-center gap-2">
          <input
            readOnly
            value={c.uuid}
            className="min-w-0 flex-1 truncate rounded-md border border-line bg-canvas px-2 py-1 font-mono text-xs"
          />
          <CopyButton value={c.uuid} label="复制 UUID" />
        </div>
        <p className="text-xs text-ink-soft">
          在上面的地址后面接以下后缀，发送不同信号；不接后缀等同于 <code>/success</code>：
        </p>
        <ul className="space-y-1">
          {SIGNALS.map((sig) => (
            <li key={sig.suffix} className="flex items-center gap-2 text-xs">
              <code className="w-20 shrink-0">{sig.suffix || '（不接）'}</code>
              <span className="min-w-0 flex-1 truncate text-ink-soft" title={sig.meaning}>
                {sig.meaning}
              </span>
              <CopyButton value={c.ping_url + sig.suffix} />
            </li>
          ))}
        </ul>
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
                      ? '检查项当时还不存在'
                      : `${d.count} 次宕机，共 ${humanDuration(d.seconds * 1000)} · ${(d.uptime * 100).toFixed(2)}% 可用`
                  }
                  className={cx(
                    'w-20 rounded-md border px-2 py-1.5 text-center text-[11px]',
                    d.no_data ? 'border-dashed border-line text-ink-soft' : uptimeTone(d.uptime)
                  )}
                >
                  <div className="font-medium">{d.boundary.slice(0, 7)}</div>
                  <div>{d.no_data ? '无数据' : `${(d.uptime * 100).toFixed(1)}%`}</div>
                </div>
              ))}
            </div>
          ) : (
            <div className="text-xs text-ink-soft">加载中…</div>
          )}
        </div>
      </div>

      <EventStream
        events={events.data?.events ?? []}
        loading={events.isLoading}
        onRetry={(code) => retryNotice.mutate(code)}
        retrying={retryNotice.isPending}
      />

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
                <th className="py-1 font-medium">退出码</th>
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
                    <td className="py-1">{p.duration ? humanDuration(p.duration * 1000) : '—'}</td>
                    <td
                      className={cx(
                        'py-1',
                        p.exit_status ? 'font-medium text-down' : 'text-ink-soft'
                      )}
                    >
                      {p.exit_status !== undefined && p.exit_status !== null ? p.exit_status : '—'}
                    </td>
                    <td className="py-1">{p.method || 'GET'}</td>
                    <td className="py-1 font-mono">{p.remote_addr}</td>
                    <td className="max-w-40 truncate py-1 text-ink-soft" title={p.ua}>
                      {p.ua}
                    </td>
                    <td className="py-1 text-right">
                      {p.body_url ? (
                        <button
                          className="underline"
                          onClick={() => setOpenBody(openBody === p.n ? null : p.n)}
                        >
                          {openBody === p.n ? '收起' : '请求体'}
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
  const body = useQuery({ queryKey: ['ping-body', uuid, n], queryFn: () => api.pingBody(uuid, n) });
  return (
    <tr>
      <td colSpan={9} className="bg-canvas px-2 py-2">
        {body.isLoading ? (
          <span className="text-xs text-ink-soft">读取中…</span>
        ) : (
          <pre className="max-h-48 overflow-auto text-[11px] whitespace-pre-wrap">
            {body.data || '（空）'}
          </pre>
        )}
      </td>
    </tr>
  );
}

function kindLabel(kind: Ping['type']): string {
  return { success: '成功', start: '开始', log: '日志', fail: '失败', ign: '忽略' }[kind] ?? kind;
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
  return state === 'up' ? '正常' : state === 'down' ? '宕机' : state === 'late' ? '延迟' : '无记录';
}

function uptimeTone(uptime: number): string {
  if (uptime >= 0.999) return 'border-up/40 bg-up/10 text-ink';
  if (uptime >= 0.99) return 'border-late/40 bg-late/10 text-ink';
  return 'border-down/40 bg-down/10 text-ink';
}

// EventStream merges signals, status transitions and alert deliveries into one
// timeline so an outage reads as a story instead of three unrelated tables.
function EventStream({
  events,
  loading,
  onRetry,
  retrying
}: {
  events: StreamEvent[];
  loading: boolean;
  onRetry: (code: string) => void;
  retrying: boolean;
}) {
  return (
    <div className="space-y-2 rounded-lg border border-line bg-surface p-4 dark:bg-transparent">
      <h2 className="text-sm font-semibold">事件流</h2>
      {loading && events.length === 0 ? (
        <div className="text-xs text-ink-soft">加载中…</div>
      ) : events.length === 0 ? (
        <EmptyState>还没有事件。</EmptyState>
      ) : (
        <ul className="max-h-96 space-y-1 overflow-auto pr-1">
          {events.map((e, i) => (
            <li
              key={i}
              className="flex items-start gap-2 border-t border-line py-1 text-xs first:border-t-0"
            >
              <span className="mt-0.5 w-32 shrink-0 text-ink-soft">{formatDateTime(e.date)}</span>
              <EventIcon e={e} />
              <span className="min-w-0 flex-1">
                <EventText e={e} />
              </span>
              {e.type === 'notification' && e.error ? (
                <button
                  className="inline-flex shrink-0 items-center gap-1 rounded border border-line px-1.5 py-0.5 text-ink-soft hover:text-ink disabled:opacity-50"
                  disabled={retrying}
                  title={e.error === 'Sending' ? '正在投递中' : e.error}
                  onClick={() => e.type === 'notification' && onRetry(e.code)}
                >
                  <RefreshCw size={11} /> 重发
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function EventIcon({ e }: { e: StreamEvent }) {
  if (e.type === 'ping') {
    return <Activity size={14} className="mt-0.5 shrink-0 text-ink-soft" />;
  }
  if (e.type === 'notification') {
    const tone = e.error
      ? 'text-down'
      : e.status === 'stable'
        ? 'text-up'
        : e.status === 'flap'
          ? 'text-late'
          : 'text-ink-soft';
    return <BellRing size={14} className={cx('mt-0.5 shrink-0', tone)} />;
  }
  if (e.new_status === 'down')
    return <ArrowDownCircle size={14} className="mt-0.5 shrink-0 text-down" />;
  if (e.new_status === 'up') return <ArrowUpCircle size={14} className="mt-0.5 shrink-0 text-up" />;
  return <PauseCircle size={14} className="mt-0.5 shrink-0 text-ink-soft" />;
}

function EventText({ e }: { e: StreamEvent }) {
  if (e.type === 'ping') {
    const parts = [kindLabel(e.kind)];
    if (e.duration) parts.push(`运行 ${humanDuration(e.duration * 1000)}`);
    if (e.exit_status !== undefined) parts.push(`退出码 ${e.exit_status}`);
    if (e.rid) parts.push(`rid ${e.rid}`);
    return (
      <>
        收到信号：
        {parts.join(' · ')}
      </>
    );
  }
  if (e.type === 'flip') {
    return (
      <span className={cx(e.new_status === 'down' && 'font-medium text-down')}>
        {flipText(e.old_status, e.new_status, e.reason)}
      </span>
    );
  }
  if (e.error === '') {
    const kind =
      e.status === 'down'
        ? '宕机告警'
        : e.status === 'flap'
          ? '抖动告警（已合并逐条通知）'
          : e.status === 'stable'
            ? '抖动结束通知'
            : '恢复通知';
    return (
      <>
        已通过「{e.channel || '已删除渠道'}」发送{kind}
      </>
    );
  }
  if (e.error === 'Sending') {
    return <span className="text-ink-soft">正在向「{e.channel || '已删除渠道'}」投递…</span>;
  }
  return (
    <span className="text-down">
      向「{e.channel || '已删除渠道'}」发送失败：{e.error}
    </span>
  );
}

function flipText(oldStatus: string, newStatus: string, reason?: 'timeout' | 'fail'): string {
  switch (newStatus) {
    case 'down':
      return reason === 'fail'
        ? '状态转为宕机：收到失败信号'
        : '状态转为宕机：成功信号未按时到达，宽限期已过';
    case 'up':
      return '状态恢复正常';
    case 'paused':
      return '监控已暂停';
    case 'new':
      return oldStatus === 'paused' ? '监控已恢复，等待新的 ping' : '等待第一个 ping';
    default:
      return `状态变为 ${newStatus}`;
  }
}
