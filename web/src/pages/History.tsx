import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { RefreshCw } from 'lucide-react';
import { EmptyState, cx, useToast } from '../components/ui';
import { api } from '../lib/api';
import { formatDateTime } from '../lib/time';

export default function History() {
  const client = useQueryClient();
  const toast = useToast();
  const rows = useQuery({
    queryKey: ['notifications'],
    queryFn: api.notifications,
    refetchInterval: 10_000
  });

  const retry = useMutation({
    mutationFn: (code: string) => api.retryNotification(code),
    onSuccess: () => {
      client.invalidateQueries({ queryKey: ['notifications'] });
      toast('已重新发送');
    },
    onError: (e: Error) => toast(e.message, 'error')
  });

  if (rows.isLoading) return <div className="text-sm text-ink-soft">加载中…</div>;
  const list = rows.data?.notifications ?? [];

  return (
    <div className="space-y-3">
      <div>
        <h1 className="text-base font-semibold">通知历史</h1>
        <p className="mt-1 text-xs text-ink-soft">
          最近 {list.length} 条投递记录；空的“结果”表示发送成功。
        </p>
      </div>

      {list.length === 0 ? (
        <EmptyState>还没有发出过通知。</EmptyState>
      ) : (
        <div className="overflow-hidden rounded-lg border border-line bg-surface dark:bg-transparent">
          <table className="w-full text-xs">
            <thead className="border-b border-line text-left text-ink-soft">
              <tr>
                <th className="px-3 py-2 font-medium">时间</th>
                <th className="px-3 py-2 font-medium">检查项</th>
                <th className="px-3 py-2 font-medium">渠道</th>
                <th className="px-3 py-2 font-medium">状态</th>
                <th className="px-3 py-2 font-medium">结果</th>
                <th className="px-3 py-2 font-medium" />
              </tr>
            </thead>
            <tbody>
              {list.map((n) => {
                const pending = n.error === 'Sending';
                const failed = n.error && n.error !== 'Sending';
                return (
                  <tr key={n.code} className="border-b border-line last:border-b-0">
                    <td className="px-3 py-1.5 whitespace-nowrap">{formatDateTime(n.created)}</td>
                    <td className="px-3 py-1.5">{n.check_name || '测试发送'}</td>
                    <td className="px-3 py-1.5">{n.channel_name}</td>
                    <td
                      className={cx(
                        'px-3 py-1.5',
                        n.status === 'down'
                          ? 'text-down'
                          : n.status === 'flap'
                            ? 'text-late'
                            : 'text-up'
                      )}
                    >
                      {statusLabel(n.status)}
                    </td>
                    <td className="max-w-64 truncate px-3 py-1.5 text-ink-soft" title={n.error}>
                      {pending ? <span className="text-late">投递中…</span> : n.error || '已发送'}
                    </td>
                    <td className="px-3 py-1.5 text-right">
                      {failed ? (
                        <button
                          className="inline-flex items-center gap-1 rounded border border-line px-1.5 py-0.5 text-ink-soft hover:text-ink disabled:opacity-50"
                          disabled={retry.isPending}
                          onClick={() => retry.mutate(n.code)}
                        >
                          <RefreshCw size={11} /> 重发
                        </button>
                      ) : null}
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}

function statusLabel(status: string): string {
  switch (status) {
    case 'down':
      return '宕机';
    case 'flap':
      return '抖动';
    case 'stable':
      return '已稳定';
    default:
      return '正常';
  }
}
