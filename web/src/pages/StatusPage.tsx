import { useQuery } from "@tanstack/react-query";
import { useEffect } from "react";
import { Link as LinkIcon } from "lucide-react";
import { useParams } from "react-router-dom";
import { StatusGlyph } from "../components/StatusGlyph";
import { cx } from "../components/ui";
import { ApiError, api } from "../lib/api";
import { statusMeta } from "../lib/status";
import { formatDateTime, fromNow } from "../lib/time";
import type { StatusPageData } from "../lib/types";

const REFRESH_MS = 5000;

const banners = {
  ok: {
    cls: "border-up/30 bg-up/10 text-up",
    title: "所有系统运行正常",
    sub: "全部检查项都在按时上报。",
  },
  grace: {
    cls: "border-late/30 bg-late/10 text-late",
    title: "部分服务信号延迟",
    sub: "有检查项未按时上报，仍在宽限期内。",
  },
  down: {
    cls: "border-down/30 bg-down/10 text-down",
    title: "部分服务异常",
    sub: "有检查项已超过宽限期未恢复。",
  },
} as const;

export default function StatusPage() {
  const { key = "" } = useParams();
  const page = useQuery({
    queryKey: ["status-page", key],
    queryFn: () => api.statusPage(key),
    refetchInterval: REFRESH_MS,
  });

  const data = page.data;
  useEffect(() => {
    if (!data) return;
    const headline =
      data.summary === "down" ? `${data.down} 项宕机` : data.summary === "grace" ? `${data.grace} 项延迟` : "全部正常";
    document.title = `${headline} · ${data.project}`;
  }, [data]);

  return (
    <div className="min-h-full bg-canvas">
      <div className="mx-auto max-w-2xl px-4 py-10 sm:py-14">
        {page.isLoading ? (
          <div className="py-20 text-center text-sm text-ink-soft">加载中…</div>
        ) : page.isError || !data ? (
          <div className="rounded-lg border border-line bg-surface px-6 py-16 text-center dark:bg-transparent">
            <p className="text-sm font-semibold">状态页不可用</p>
            <p className="mt-1 text-xs text-ink-soft">
              {page.error instanceof ApiError && page.error.status === 404
                ? "链接无效或已被更换，请向服务提供方索取最新地址。"
                : "暂时无法获取状态，请稍后刷新重试。"}
            </p>
          </div>
        ) : (
          <Page data={data} />
        )}
      </div>
    </div>
  );
}

function Page({ data }: { data: StatusPageData }) {
  const banner = banners[data.summary];
  return (
    <div className="space-y-5">
      <header className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <h1 className="text-xl font-semibold">{data.project}</h1>
          <p className="mt-0.5 text-xs text-ink-soft">服务运行状态</p>
        </div>
        <span className="text-xs text-ink-soft">更新于 {formatDateTime(data.updated_at)}</span>
      </header>

      <div className={cx("flex items-center gap-3 rounded-xl border px-5 py-4", banner.cls)}>
        <StatusGlyph
          status={data.summary === "ok" ? "up" : data.summary}
          started={false}
          size={28}
        />
        <div>
          <div className="text-base font-semibold">
            {banner.title}
            {data.summary === "down" && data.down > 0 ? ` · ${data.down} 项宕机` : ""}
            {data.summary === "grace" && data.grace > 0 ? ` · ${data.grace} 项延迟` : ""}
          </div>
          <div className="text-xs opacity-80">{banner.sub}</div>
        </div>
      </div>

      {data.checks.length === 0 ? (
        <div className="rounded-lg border border-line bg-surface px-4 py-10 text-center text-sm text-ink-soft dark:bg-transparent">
          暂无可展示的检查项。
        </div>
      ) : (
        <div className="divide-y divide-line overflow-hidden rounded-lg border border-line bg-surface dark:bg-transparent">
          {data.checks.map((c, i) => {
            const meta = statusMeta(c.status);
            const dimmed = c.status === "paused";
            return (
              <div key={i} className={cx("flex items-center gap-3 px-4 py-3", dimmed && "opacity-60")}>
                <span className="shrink-0">
                  <StatusGlyph status={c.status} started={c.started} />
                </span>
                <div className="min-w-0 flex-1">
                  <div className="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                    <span className="truncate text-sm font-medium">{c.name}</span>
                    {c.flapping && (
                      <span className="rounded bg-late/15 px-1.5 py-0.5 text-[11px] text-late">抖动中</span>
                    )}
                    {c.tags.split(/\s+/).filter(Boolean).map((t) => (
                      <span key={t} className="rounded bg-canvas px-1.5 py-0.5 text-[11px] text-ink-soft">
                        {t}
                      </span>
                    ))}
                  </div>
                  <div className="mt-0.5 text-xs text-ink-soft">
                    {c.status === "new"
                      ? "等待首个信号"
                      : c.status === "paused"
                        ? "监控已暂停"
                        : c.started
                          ? "任务运行中"
                          : `最后信号 ${fromNow(c.last_ping)}`}
                  </div>
                </div>
                <span className={cx("shrink-0 text-sm font-medium", meta.text)}>{meta.label}</span>
              </div>
            );
          })}
        </div>
      )}

      <footer className="flex items-center justify-center gap-1.5 pb-4 text-[11px] text-ink-soft">
        <LinkIcon size={12} />
        每 {REFRESH_MS / 1000} 秒自动刷新 · 由飞檄提供
      </footer>
    </div>
  );
}
