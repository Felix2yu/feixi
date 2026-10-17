import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Mail, Plus, Send, Trash2 } from 'lucide-react';
import { useState } from 'react';
import {
  Button,
  Card,
  EmptyState,
  Field,
  Input,
  Modal,
  Textarea,
  Toggle,
  cx,
  useToast
} from '../components/ui';
import { api } from '../lib/api';
import { formatDateTime, humanDuration } from '../lib/time';
import type { Channel } from '../lib/types';

export default function Channels() {
  const client = useQueryClient();
  const toast = useToast();
  const list = useQuery({ queryKey: ['channels'], queryFn: api.channels });
  const meta = useQuery({ queryKey: ['meta'], queryFn: api.meta });
  const [editing, setEditing] = useState<Channel | null>(null);
  const [creating, setCreating] = useState(false);

  const reload = () => {
    client.invalidateQueries({ queryKey: ['channels'] });
    client.invalidateQueries({ queryKey: ['checks'] });
  };

  const test = useMutation({
    mutationFn: (code: string) => api.testChannel(code),
    onSuccess: () => toast('测试消息已发出'),
    onError: (e: Error) => toast(`发送失败：${e.message}`, 'error')
  });

  const toggle = useMutation({
    mutationFn: (ch: Channel) => api.updateChannel(ch.code, { disabled: !ch.disabled }),
    onSuccess: () => reload()
  });

  const remove = useMutation({
    mutationFn: (code: string) => api.deleteChannel(code),
    onSuccess: () => {
      reload();
      toast('渠道已删除');
    },
    onError: (e: Error) => toast(e.message, 'error')
  });

  const disabled = (list.data?.channels ?? []).filter((c) => c.disabled);
  const nagPeriod = meta.data?.nag_period_seconds ?? 0;

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <h1 className="text-base font-semibold">通知渠道</h1>
        <Button variant="primary" onClick={() => setCreating(true)}>
          <Plus size={16} /> 新建渠道
        </Button>
      </div>

      {disabled.length > 0 ? (
        <div className="rounded-md border border-late/40 bg-late/10 px-3 py-2 text-xs">
          {disabled.length} 个渠道因连续失败被停用，修好 URL 后在卡片上重新启用。
        </div>
      ) : null}

      {(list.data?.channels ?? []).length === 0 ? (
        <EmptyState>
          还没有渠道。apprise 支持 200+
          服务：Telegram、Slack、Discord、企业微信、ntfy、邮件（mailto）等都写在一个 URL 里。
        </EmptyState>
      ) : (
        <div className="grid gap-3 md:grid-cols-2">
          {(list.data?.channels ?? []).map((ch) => (
            <Card key={ch.code} className={cx('space-y-2', ch.disabled && 'opacity-70')}>
              <div className="flex items-center gap-2">
                <Mail size={15} className="text-ink-soft" />
                <span className="min-w-0 flex-1 truncate font-medium">{ch.name}</span>
                {ch.disabled ? (
                  <span className="rounded bg-down/10 px-1.5 py-0.5 text-[11px] text-down">
                    已停用
                  </span>
                ) : null}
              </div>
              <ul className="space-y-0.5">
                {ch.urls.map((u) => (
                  <li key={u} className="truncate font-mono text-[11px] text-ink-soft" title={u}>
                    {mask(u)}
                  </li>
                ))}
              </ul>
              <div className="flex flex-wrap items-center gap-2 text-[11px] text-ink-soft">
                <span>{ch.up ? '宕机+恢复' : '仅宕机'}</span>
                {ch.nag && nagPeriod > 0 ? (
                  <span className="text-late">每 {humanDuration(nagPeriod * 1000)} 催促</span>
                ) : null}
                {ch.last_notify ? <span>最后发送 {formatDateTime(ch.last_notify)}</span> : null}
                {ch.last_notify_duration ? (
                  <span>耗时 {humanDuration(ch.last_notify_duration * 1000)}</span>
                ) : null}
              </div>
              {ch.last_error ? (
                <div className="truncate text-[11px] text-down" title={ch.last_error}>
                  {ch.last_error}
                </div>
              ) : null}
              <div className="flex items-center gap-1">
                <Button size="sm" onClick={() => test.mutate(ch.code)} disabled={test.isPending}>
                  <Send size={13} /> 发送测试
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setEditing(ch)}>
                  编辑
                </Button>
                <Button size="sm" variant="ghost" onClick={() => toggle.mutate(ch)}>
                  {ch.disabled ? '启用' : '停用'}
                </Button>
                <Button
                  size="sm"
                  variant="ghost"
                  onClick={() => {
                    if (window.confirm(`删除渠道“${ch.name}”？`)) remove.mutate(ch.code);
                  }}
                >
                  <Trash2 size={13} className="text-down" />
                </Button>
              </div>
            </Card>
          ))}
        </div>
      )}

      <ChannelForm
        open={creating || !!editing}
        channel={editing}
        schemas={meta.data?.apprise_schemas ?? []}
        nagPeriod={nagPeriod}
        onClose={() => {
          setCreating(false);
          setEditing(null);
        }}
        onSaved={() => {
          reload();
          setCreating(false);
          setEditing(null);
        }}
      />
    </div>
  );
}

function ChannelForm({
  open,
  channel,
  schemas,
  nagPeriod,
  onClose,
  onSaved
}: {
  open: boolean;
  channel: Channel | null;
  schemas: string[];
  nagPeriod: number;
  onClose: () => void;
  onSaved: () => void;
}) {
  const toast = useToast();
  const [name, setName] = useState(channel?.name ?? '');
  const [urls, setUrls] = useState((channel?.urls ?? []).join('\n'));
  const [up, setUp] = useState(channel?.up ?? false);
  const [down, setDown] = useState(channel?.down ?? true);
  const [nag, setNag] = useState(channel?.nag ?? false);
  const [error, setError] = useState('');
  const [seed, setSeed] = useState<string | null>(null);

  // Re-seed the fields whenever a different channel opens.
  if (open && seed !== (channel?.code ?? 'new')) {
    setSeed(channel?.code ?? 'new');
    setName(channel?.name ?? '');
    setUrls((channel?.urls ?? []).join('\n'));
    setUp(channel?.up ?? false);
    setDown(channel?.down ?? true);
    setNag(channel?.nag ?? false);
    setError('');
  }

  const save = useMutation({
    mutationFn: async () => {
      const body = {
        name,
        urls: urls
          .split('\n')
          .map((s) => s.trim())
          .filter(Boolean),
        up,
        down,
        nag: nagPeriod > 0 ? nag : false
      };
      if (channel) return api.updateChannel(channel.code, body);
      return api.createChannel(body);
    },
    onSuccess: () => {
      toast(channel ? '渠道已更新' : '渠道已创建');
      onSaved();
    },
    onError: (e: Error) => setError(e.message)
  });

  return (
    <Modal open={open} title={channel ? '编辑渠道' : '新建渠道'} onClose={onClose} wide>
      <div className="space-y-3">
        <Field label="名称">
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Telegram 群组"
          />
        </Field>
        <Field
          label="Apprise URL（每行一个）"
          hint="同一渠道内的所有 URL 都会收到通知。?:字段=$NAME 之类的写法可以把变量塞进请求体，?-字段 进查询串，?+字段 进 header。"
        >
          <Textarea
            rows={5}
            value={urls}
            onChange={(e) => setUrls(e.target.value)}
            placeholder={
              'tgram://bottoken/chatid\nmailto://user:pass@gmail.com\njson://hooks.example.com/feixi?:check=$NAME&:status=$STATUS'
            }
          />
        </Field>
        <div className="max-h-28 overflow-y-auto rounded-md border border-line bg-canvas p-2">
          <div className="mb-1 text-[11px] text-ink-soft">
            本进程支持的 {schemas.length} 个 scheme（点击填入）
          </div>
          <div className="flex flex-wrap gap-1">
            {schemas.map((s) => (
              <button
                key={s}
                onClick={() =>
                  setUrls(`${urls}${urls.endsWith('\n') || urls === '' ? '' : '\n'}${s}://`)
                }
                className="rounded bg-surface px-1.5 py-0.5 font-mono text-[11px] hover:bg-line"
              >
                {s}
              </button>
            ))}
          </div>
        </div>
        <div className="flex flex-wrap gap-4">
          <Toggle checked={down} onChange={setDown} label="宕机时通知" />
          <Toggle checked={up} onChange={setUp} label="恢复时通知" />
          {nagPeriod > 0 ? (
            <Toggle
              checked={nag}
              onChange={setNag}
              label={`宕机未恢复时每 ${humanDuration(nagPeriod * 1000)} 催促`}
            />
          ) : (
            <span className="self-center text-[11px] text-ink-soft">
              重复催促未启用：设置环境变量 FEIXI_NAG_PERIOD（如 30m、1h）后可按渠道开启
            </span>
          )}
        </div>
        {error ? <div className="text-xs text-down">{error}</div> : null}
        <div className="flex justify-end gap-2">
          <Button onClick={onClose}>取消</Button>
          <Button variant="primary" onClick={() => save.mutate()} disabled={save.isPending}>
            保存
          </Button>
        </div>
      </div>
    </Modal>
  );
}

// mask hides credentials in the URL list.
function mask(u: string): string {
  const i = u.indexOf('://');
  if (i < 0) return u;
  const rest = u.slice(i + 3);
  const at = rest.indexOf('@');
  if (at < 0) return u;
  const host = rest.slice(at + 1);
  const scheme = u.slice(0, i);
  const user = rest.slice(0, at).split(':')[0];
  return `${scheme}://${user}:***@${host}`;
}
