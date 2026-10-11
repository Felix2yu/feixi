import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowLeft, Save } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { Button, Card, Field, Input, Select, Textarea, Toggle, cx, useToast } from "../components/ui";
import { api } from "../lib/api";
import type { CheckSpec } from "../lib/types";

const TIMEOUT_PRESETS: Array<{ label: string; seconds: number }> = [
  { label: "每小时", seconds: 3600 },
  { label: "每 6 小时", seconds: 6 * 3600 },
  { label: "每天", seconds: 24 * 3600 },
  { label: "每 3 天", seconds: 3 * 24 * 3600 },
  { label: "每周", seconds: 7 * 24 * 3600 },
];

const GRACE_PRESETS = [60, 300, 600, 1800, 3600];

const CRON_FIELDS: Array<{ key: number; label: string; placeholder: string }> = [
  { key: 0, label: "分钟", placeholder: "*" },
  { key: 1, label: "小时", placeholder: "*" },
  { key: 2, label: "日", placeholder: "*" },
  { key: 3, label: "月", placeholder: "*" },
  { key: 4, label: "星期", placeholder: "*" },
];

function localTimeZones(): string[] {
  const supported = (Intl as unknown as { supportedValuesOf?: (k: string) => string[] }).supportedValuesOf;
  const list = supported ? supported("timeZone") : [];
  return list.length ? list : ["UTC", "Asia/Shanghai"];
}

export default function CheckForm() {
  const { uuid } = useParams();
  const navigate = useNavigate();
  const client = useQueryClient();
  const toast = useToast();
  const editing = !!uuid;

  const existing = useQuery({
    queryKey: ["check", uuid],
    queryFn: () => api.check(uuid!),
    enabled: editing,
  });
  const channels = useQuery({ queryKey: ["channels"], queryFn: api.channels });

  const [form, setForm] = useState({
    name: "",
    tags: "",
    desc: "",
    kind: "simple" as "simple" | "cron",
    timeout: 24 * 3600,
    grace: 3600,
    schedule: "0 2 * * *",
    tz: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
    methods: "" as "" | "POST",
    manual_resume: false,
    filter_http_body: false,
    filter_default_fail: false,
    start_kw: "",
    success_kw: "",
    failure_kw: "",
    channelCodes: [] as string[],
  });
  const [mode, setMode] = useState<"text" | "grid">("text");
  const [preview, setPreview] = useState<string[]>([]);

  // Fill the form from the server once, not on every render.
  useEffect(() => {
    if (!existing.data) return;
    const c = existing.data;
    setForm({
      name: c.name,
      tags: c.tags,
      desc: c.desc,
      kind: c.schedule ? "cron" : "simple",
      timeout: c.timeout ?? 24 * 3600,
      grace: c.grace,
      schedule: c.schedule ?? "0 2 * * *",
      tz: c.tz ?? (Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC"),
      methods: c.methods === "POST" ? "POST" : "",
      manual_resume: c.manual_resume,
      filter_http_body: c.filter_http_body,
      filter_default_fail: c.filter_default_fail,
      start_kw: c.start_kw,
      success_kw: c.success_kw,
      failure_kw: c.failure_kw,
      channelCodes: channels.data?.channels.map((ch) => ch.code).filter((code) => (c.channels || "").split(",").includes(code)) ?? [],
    });
  }, [existing.data, channels.data]);

  useEffect(() => {
    if (form.kind !== "cron") return;
    const timer = setTimeout(() => {
      api
        .previewSchedule(form.schedule, form.tz)
        .then((r) => setPreview(r.next))
        .catch((e: Error) => setPreview([`✗ ${e.message}`]));
    }, 350);
    return () => clearTimeout(timer);
  }, [form.kind, form.schedule, form.tz]);

  const save = useMutation({
    mutationFn: async () => {
      const spec: CheckSpec = {
        name: form.name,
        tags: form.tags,
        desc: form.desc,
        grace: form.grace,
        methods: form.methods,
        manual_resume: form.manual_resume,
        filter_http_body: form.filter_http_body,
        filter_default_fail: form.filter_default_fail,
        start_kw: form.start_kw,
        success_kw: form.success_kw,
        failure_kw: form.failure_kw,
        channels: form.channelCodes.join(","),
      };
      if (form.kind === "simple") {
        spec.timeout = form.timeout;
      } else {
        spec.schedule = form.schedule;
        spec.tz = form.tz;
      }
      if (editing) return api.updateCheck(uuid!, spec);
      // A brand new check gets every existing channel by default.
      if (form.channelCodes.length === 0) spec.channels = "*";
      return api.createCheck(spec);
    },
    onSuccess: (c) => {
      client.invalidateQueries({ queryKey: ["checks"] });
      client.invalidateQueries({ queryKey: ["status"] });
      toast(editing ? "已保存" : "检查项已创建");
      navigate(c ? `/checks/${c.uuid}` : "/");
    },
    onError: (e: Error) => toast(e.message, "error"),
  });

  const scheduleFields = form.schedule.trim().split(/\s+/);

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Link to={editing ? `/checks/${uuid}` : "/"}>
          <Button variant="ghost" size="sm">
            <ArrowLeft size={14} /> 返回
          </Button>
        </Link>
        <h1 className="text-base font-semibold">{editing ? "编辑检查项" : "新建检查项"}</h1>
      </div>

      <Card className="space-y-3">
        <h2 className="text-sm font-semibold">基本信息</h2>
        <div className="grid gap-3 md:grid-cols-2">
          <Field label="名称">
            <Input value={form.name} onChange={(e) => setForm({ ...form, name: e.target.value })} placeholder="夜间备份" />
          </Field>
          <Field label="标签" hint="空格分隔，用于筛选和徽章">
            <Input value={form.tags} onChange={(e) => setForm({ ...form, tags: e.target.value })} placeholder="prod www" />
          </Field>
        </div>
        <Field label="描述">
          <Textarea rows={2} value={form.desc} onChange={(e) => setForm({ ...form, desc: e.target.value })} />
        </Field>
      </Card>

      <Card className="space-y-3">
        <h2 className="text-sm font-semibold">周期</h2>
        <div className="flex gap-2">
          {(["simple", "cron"] as const).map((k) => (
            <button
              key={k}
              onClick={() => setForm({ ...form, kind: k })}
              className={cx(
                "rounded-md border px-3 py-1 text-sm",
                form.kind === k ? "border-ink bg-ink text-white" : "border-line text-ink-soft",
              )}
            >
              {k === "simple" ? "固定间隔" : "Cron 表达式"}
            </button>
          ))}
        </div>

        {form.kind === "simple" ? (
          <div className="space-y-3">
            <Field label="期望间隔（超时时间）">
              <div className="flex flex-wrap items-center gap-2">
                {TIMEOUT_PRESETS.map((p) => (
                  <button
                    key={p.seconds}
                    onClick={() => setForm({ ...form, timeout: p.seconds })}
                    className={cx(
                      "rounded-md border px-2 py-1 text-xs",
                      form.timeout === p.seconds ? "border-ink bg-ink text-white" : "border-line text-ink-soft",
                    )}
                  >
                    {p.label}
                  </button>
                ))}
                <Input
                  type="number"
                  className="w-28!"
                  value={form.timeout}
                  onChange={(e) => setForm({ ...form, timeout: Number(e.target.value) || 0 })}
                />
                <span className="text-xs text-ink-soft">秒</span>
              </div>
            </Field>
          </div>
        ) : (
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <span className="text-xs font-medium text-ink-soft">Cron 表达式</span>
              <button
                onClick={() => setMode(mode === "text" ? "grid" : "text")}
                className="text-xs text-ink-soft underline"
              >
                {mode === "text" ? "改用五段填写" : "改用文本输入"}
              </button>
            </div>
            {mode === "text" ? (
              <Input
                value={form.schedule}
                onChange={(e) => setForm({ ...form, schedule: e.target.value })}
                className="font-mono"
                placeholder="15 5 * * *"
              />
            ) : (
              <div className="grid grid-cols-5 gap-2">
                {CRON_FIELDS.map((f) => (
                  <Field key={f.key} label={f.label}>
                    <Input
                      value={scheduleFields[f.key] ?? "*"}
                      onChange={(e) => {
                        const next = [...Array(5)].map((_, i) => scheduleFields[i] ?? "*");
                        next[f.key] = e.target.value || "*";
                        setForm({ ...form, schedule: next.join(" ") });
                      }}
                      className="font-mono"
                      placeholder={f.placeholder}
                    />
                  </Field>
                ))}
              </div>
            )}
            <div className="grid gap-3 md:grid-cols-2">
              <Field label="时区">
                <Select value={form.tz} onChange={(e) => setForm({ ...form, tz: e.target.value })}>
                  {localTimeZones().map((z) => (
                    <option key={z} value={z}>
                      {z}
                    </option>
                  ))}
                </Select>
              </Field>
            </div>
            <div className="rounded-md border border-line bg-canvas px-3 py-2 text-xs">
              <div className="mb-1 font-medium text-ink-soft">接下来会运行在</div>
              {preview.length === 0 ? <div className="text-ink-soft">…</div> : preview.map((p) => <div key={p}>{p}</div>)}
            </div>
          </div>
        )}

        <Field label="宽限期" hint="超过期望时间后，再宽限多久才判定宕机">
          <div className="flex flex-wrap items-center gap-2">
            {GRACE_PRESETS.map((g) => (
              <button
                key={g}
                onClick={() => setForm({ ...form, grace: g })}
                className={cx(
                  "rounded-md border px-2 py-1 text-xs",
                  form.grace === g ? "border-ink bg-ink text-white" : "border-line text-ink-soft",
                )}
              >
                {g < 3600 ? `${g / 60} 分钟` : `${g / 3600} 小时`}
              </button>
            ))}
            <Input
              type="number"
              className="w-28!"
              value={form.grace}
              onChange={(e) => setForm({ ...form, grace: Number(e.target.value) || 0 })}
            />
            <span className="text-xs text-ink-soft">秒</span>
          </div>
        </Field>
      </Card>

      <Card className="space-y-3">
        <h2 className="text-sm font-semibold">通知渠道</h2>
        {channels.isLoading ? <div className="text-xs text-ink-soft">加载中…</div> : null}
        {channels.data && channels.data.channels.length === 0 ? (
          <div className="text-xs text-ink-soft">
            还没有渠道，先去 <Link className="underline" to="/channels">通知渠道</Link> 建一个。
          </div>
        ) : null}
        <div className="flex flex-wrap gap-2">
          {(channels.data?.channels ?? []).map((ch) => {
            const on = form.channelCodes.includes(ch.code);
            return (
              <button
                key={ch.code}
                onClick={() =>
                  setForm({
                    ...form,
                    channelCodes: on ? form.channelCodes.filter((c) => c !== ch.code) : [...form.channelCodes, ch.code],
                  })
                }
                className={cx(
                  "rounded-md border px-2.5 py-1 text-xs",
                  on ? "border-up bg-up/10 text-ink" : "border-line text-ink-soft",
                )}
              >
                {on ? "✓ " : ""}
                {ch.name}
              </button>
            );
          })}
        </div>
      </Card>

      <Card className="space-y-3">
        <h2 className="text-sm font-semibold">高级</h2>
        <div className="flex flex-col items-start gap-3">
        <Toggle
          checked={form.methods === "POST"}
          onChange={(v) => setForm({ ...form, methods: v ? "POST" : "" })}
          label="只接受 POST 请求（GET 会被记为忽略）"
        />
        <Toggle
          checked={form.manual_resume}
          onChange={(v) => setForm({ ...form, manual_resume: v })}
          label="粘性暂停：暂停期间忽略 ping，需手动恢复"
        />
        <Toggle
          checked={form.filter_http_body}
          onChange={(v) => setForm({ ...form, filter_http_body: v })}
          label="按请求体关键字判定状态"
        />
        </div>
        {form.filter_http_body ? (
          <div className="grid gap-3 md:grid-cols-3">
            <Field label="失败关键字">
              <Input value={form.failure_kw} onChange={(e) => setForm({ ...form, failure_kw: e.target.value })} />
            </Field>
            <Field label="成功关键字">
              <Input value={form.success_kw} onChange={(e) => setForm({ ...form, success_kw: e.target.value })} />
            </Field>
            <Field label="开始关键字">
              <Input value={form.start_kw} onChange={(e) => setForm({ ...form, start_kw: e.target.value })} />
            </Field>
            <div className="md:col-span-3">
              <Toggle
                checked={form.filter_default_fail}
                onChange={(v) => setForm({ ...form, filter_default_fail: v })}
                label="都不匹配时判定为失败"
              />
            </div>
          </div>
        ) : null}
      </Card>

      <div className="flex items-center gap-2">
        <Button variant="primary" onClick={() => save.mutate()} disabled={save.isPending}>
          <Save size={14} /> {save.isPending ? "保存中…" : editing ? "保存" : "创建"}
        </Button>
        {save.error ? <span className="text-xs text-down">{(save.error as Error).message}</span> : null}
      </div>
    </div>
  );
}
