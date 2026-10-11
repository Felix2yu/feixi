import { useQuery } from "@tanstack/react-query";
import { CopyButton } from "../components/ui";
import { api } from "../lib/api";

export default function Settings() {
  const project = useQuery({ queryKey: ["project"], queryFn: api.project });
  const info = project.data;

  const create = info
    ? `curl ${info.site_root}/api/v3/checks/ \\\n  --header 'Content-Type: application/json' \\\n  --data '{"name": "备份", "tags": "prod", "timeout": 3600, "grace": 60}'`
    : "";
  const ping = info ? `curl ${info.site_root}/ping/<检查项 UUID>` : "";
  const slug = info
    ? `curl ${info.site_root}/ping/${info.ping_key}/nightly-backups?create=1`
    : "";

  return (
    <div className="space-y-4">
      <h1 className="text-base font-semibold">设置</h1>

      <div className="rounded-lg border border-line bg-surface p-4 dark:bg-transparent">
        <h2 className="mb-2 text-sm font-semibold">运行时</h2>
        <p className="mb-3 text-xs text-ink-soft">
          这一版没有登录与用户体系，实例是单项目的，所有限制都来自环境变量；改完重启即可。
        </p>
        <dl className="grid gap-x-6 gap-y-2 text-sm md:grid-cols-2">
          <Row label="项目" value={info?.name} />
          <Row label="检查项数量" value={info ? `${info.check_count} / ${info.max_checks}` : undefined} hint="FEIXI_MAX_CHECKS" />
          <Row label="Ping 体上限" value={info ? `${info.ping_body_limit} 字节` : undefined} hint="FEIXI_PING_BODY_LIMIT" />
          <Row label="Ping 保留条数" value={info ? String(info.ping_log_limit) : undefined} hint="FEIXI_PING_LOG_LIMIT" />
          <Row label="Slug ping 前缀" value={info?.ping_key} />
          <Row label="徽章项目 key" value={info?.badge_key} />
          <Row label="站点地址" value={info?.site_root} hint="FEIXI_SITE_ROOT" />
          <Row label="全量徽章标签" value={info?.master_badge_label} hint="FEIXI_MASTER_BADGE_LABEL" />
        </dl>
      </div>

      <div className="space-y-3 rounded-lg border border-line bg-surface p-4 dark:bg-transparent">
        <h2 className="text-sm font-semibold">接入方式</h2>
        <Snippet title="创建检查项" code={create} />
        <Snippet title="发送成功信号" code={ping} />
        <Snippet title="按名称建 slug 地址（首次 ping 自动建档）" code={slug} />
        <p className="text-xs text-ink-soft">
          失败信号把地址换成 <code>/fail</code>，或带退出码 <code>/&lt;exitstatus&gt;</code>；开始运行用{" "}
          <code>/start</code>，中间日志用 <code>/log</code>。
        </p>
      </div>
    </div>
  );
}

function Row({ label, value, hint }: { label: string; value?: string; hint?: string }) {
  return (
    <div className="flex items-baseline gap-2">
      <dt className="w-28 shrink-0 text-xs text-ink-soft">{label}</dt>
      <dd className="min-w-0 flex-1 truncate font-mono text-xs">{value ?? "…"}</dd>
      {hint ? <span className="shrink-0 text-[10px] text-ink-soft">{hint}</span> : null}
    </div>
  );
}

function Snippet({ title, code }: { title: string; code: string }) {
  return (
    <div>
      <div className="mb-1 text-xs text-ink-soft">{title}</div>
      <div className="flex items-start gap-2">
        <pre className="max-h-32 min-w-0 flex-1 overflow-auto rounded-md border border-line bg-canvas px-2 py-1.5 text-[11px]">{code}</pre>
        <CopyButton value={code} />
      </div>
    </div>
  );
}
