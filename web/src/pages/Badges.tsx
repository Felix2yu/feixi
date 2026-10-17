import { useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { CopyButton, EmptyState, cx } from '../components/ui';
import { api } from '../lib/api';

type Variant = 'svg' | 'json' | 'shields' | 'svg3' | 'json3' | 'shields3';

const variants: Array<{ key: Variant; label: string; note: string }> = [
  { key: 'svg', label: 'SVG（两态）', note: '延迟算正常' },
  { key: 'svg3', label: 'SVG（三态）', note: '延迟显示为 late' },
  { key: 'shields', label: 'shields.io', note: '给 shields 用的 JSON' },
  { key: 'json', label: 'JSON', note: '含 total/grace/down' }
];

export default function Badges() {
  const badges = useQuery({ queryKey: ['badges'], queryFn: api.badges });
  const checks = useQuery({ queryKey: ['checks'], queryFn: api.checks });
  const project = useQuery({ queryKey: ['project'], queryFn: api.project });
  const [variant, setVariant] = useState<Variant>('svg3');

  if (badges.isLoading) return <div className="text-sm text-ink-soft">加载中…</div>;
  const entries = Object.entries(badges.data?.badges ?? {});
  const statusPageURL = project.data
    ? `${project.data.site_root.replace(/\/+$/, '')}/status/${project.data.badge_key}`
    : '';

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-base font-semibold">状态徽章</h1>
        <p className="mt-1 text-xs text-ink-soft">
          徽章地址带签名，公开也没法猜；把它贴进 README
          或内部门户即可。改地址需要重新生成，签名与项目密钥绑定。
        </p>
      </div>

      {statusPageURL && (
        <div className="space-y-2 rounded-lg border border-line bg-surface p-4 dark:bg-transparent">
          <h2 className="text-sm font-semibold">对外状态页</h2>
          <p className="text-xs text-ink-soft">
            只读的公开状态页，不带任何管理入口；地址含项目密钥，无法被猜到，可直接发给外部团队或挂到内部门户。
          </p>
          <div className="flex flex-wrap items-center gap-2">
            <input
              readOnly
              value={statusPageURL}
              className="max-w-xl min-w-0 flex-1 truncate rounded border border-line bg-canvas px-2 py-1 font-mono text-[11px]"
            />
            <CopyButton value={statusPageURL} />
            <a
              href={statusPageURL}
              target="_blank"
              rel="noreferrer"
              className="rounded-md border border-line px-2.5 py-1 text-xs text-ink-soft hover:text-ink"
            >
              打开
            </a>
          </div>
        </div>
      )}

      <div className="flex flex-wrap gap-2">
        {variants.map((v) => (
          <button
            key={v.key}
            onClick={() => setVariant(v.key)}
            className={cx(
              'rounded-md border px-2.5 py-1 text-xs',
              variant === v.key
                ? 'border-ink bg-accent text-accent-ink'
                : 'border-line text-ink-soft'
            )}
            title={v.note}
          >
            {v.label}
          </button>
        ))}
      </div>

      <div className="space-y-2 rounded-lg border border-line bg-surface p-4 dark:bg-transparent">
        <h2 className="text-sm font-semibold">项目与标签</h2>
        {entries.length === 0 ? (
          <EmptyState>还没有检查项。</EmptyState>
        ) : (
          entries.map(([tag, urls]) => {
            const url = urls[variant];
            const isSvg = variant === 'svg' || variant === 'svg3';
            return (
              <div
                key={tag}
                className="flex flex-wrap items-center gap-2 border-b border-line py-2 last:border-b-0"
              >
                <span className="w-28 shrink-0 text-xs font-medium">
                  {tag === '*' ? '全部' : tag}
                </span>
                {isSvg ? (
                  <img src={url} alt={tag} className="h-5" />
                ) : (
                  <code className="text-[11px] text-ink-soft">GET {url}</code>
                )}
                <input
                  readOnly
                  value={url}
                  className="max-w-md min-w-0 flex-1 truncate rounded border border-line bg-canvas px-2 py-1 font-mono text-[11px]"
                />
                <CopyButton value={isSvg ? `![飞檄](${url})` : url} />
              </div>
            );
          })
        )}
      </div>

      <div className="space-y-2 rounded-lg border border-line bg-surface p-4 dark:bg-transparent">
        <h2 className="text-sm font-semibold">单个检查项</h2>
        {(checks.data?.checks ?? []).map((c) => {
          const url = c.badge_url.replace('/b/2/', '/b/3/');
          return (
            <div
              key={c.uuid}
              className="flex flex-wrap items-center gap-2 border-b border-line py-2 last:border-b-0"
            >
              <span className="w-40 shrink-0 truncate text-xs">{c.name || c.uuid}</span>
              <img src={url} alt={c.name} className="h-5" />
              <input
                readOnly
                value={url}
                className="max-w-md min-w-0 flex-1 truncate rounded border border-line bg-canvas px-2 py-1 font-mono text-[11px]"
              />
              <CopyButton value={`![飞檄](${url})`} />
            </div>
          );
        })}
      </div>
    </div>
  );
}
