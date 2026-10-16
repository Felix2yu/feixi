import { Check, Copy } from "lucide-react";
import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";

export function cx(...parts: Array<string | false | null | undefined>): string {
  return parts.filter(Boolean).join(" ");
}

type ButtonProps = {
  children: ReactNode;
  onClick?: () => void;
  variant?: "primary" | "ghost" | "danger" | "outline";
  size?: "sm" | "md";
  disabled?: boolean;
  title?: string;
  type?: "button" | "submit";
  className?: string;
};

export function Button({
  children,
  onClick,
  variant = "outline",
  size = "md",
  disabled,
  title,
  type = "button",
  className,
}: ButtonProps) {
  const base =
    "inline-flex items-center gap-1.5 rounded-md border font-medium transition disabled:opacity-50 disabled:cursor-not-allowed whitespace-nowrap";
  const sizes = size === "sm" ? "px-2 py-1 text-xs" : "px-3 py-1.5 text-sm";
  const variants = {
    primary: "bg-accent text-accent-ink border-accent hover:opacity-90",
    outline: "bg-surface border-line text-ink hover:bg-canvas dark:bg-transparent",
    ghost: "border-transparent bg-transparent text-ink-soft hover:text-ink",
    danger: "border-down/40 text-down hover:bg-down/10",
  }[variant];
  return (
    <button type={type} title={title} disabled={disabled} onClick={onClick} className={cx(base, sizes, variants, className)}>
      {children}
    </button>
  );
}

export function Field({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <label className="block">
      <span className="mb-1 block text-xs font-medium text-ink-soft">{label}</span>
      {children}
      {hint ? <span className="mt-1 block text-xs text-ink-soft">{hint}</span> : null}
    </label>
  );
}

const inputClass =
  "w-full rounded-md border border-line bg-surface px-2.5 py-1.5 text-sm outline-none focus:border-ink-soft dark:bg-transparent";

export function Input(props: React.InputHTMLAttributes<HTMLInputElement>) {
  return <input {...props} className={cx(inputClass, props.className)} />;
}

export function Textarea(props: React.TextareaHTMLAttributes<HTMLTextAreaElement>) {
  return <textarea {...props} className={cx(inputClass, "font-mono text-xs", props.className)} />;
}

export function Select(props: React.SelectHTMLAttributes<HTMLSelectElement>) {
  return <select {...props} className={cx(inputClass, "w-auto", props.className)} />;
}

export function Toggle({
  checked,
  onChange,
  label,
}: {
  checked: boolean;
  onChange: (next: boolean) => void;
  label: string;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onChange(!checked)}
      className="flex w-fit items-center gap-2 text-sm"
    >
      <span
        className={cx(
          "relative h-5 w-9 rounded-full border transition",
          checked ? "border-up/60 bg-up/25" : "border-line bg-canvas",
        )}
      >
        <span
          className={cx(
            "absolute top-0.5 h-4 w-4 rounded-full bg-surface shadow transition-all",
            checked ? "left-4.5" : "left-0.5",
          )}
        />
      </span>
      <span className="text-ink-soft">{label}</span>
    </button>
  );
}

export function Modal({
  open,
  title,
  onClose,
  children,
  wide,
}: {
  open: boolean;
  title: string;
  onClose: () => void;
  children: ReactNode;
  wide?: boolean;
}) {
  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto bg-black/40 p-4 pt-16">
      <div className={cx("w-full rounded-lg border border-line bg-surface shadow-lg dark:bg-canvas", wide ? "max-w-3xl" : "max-w-lg")}>
        <div className="flex items-center justify-between border-b border-line px-4 py-3">
          <h2 className="text-sm font-semibold">{title}</h2>
          <button onClick={onClose} className="text-ink-soft hover:text-ink" aria-label="关闭">
            ✕
          </button>
        </div>
        <div className="px-4 py-3">{children}</div>
      </div>
    </div>
  );
}

interface Toast {
  id: number;
  text: string;
  kind: "ok" | "error";
}

const ToastContext = createContext<(text: string, kind?: Toast["kind"]) => void>(() => {});

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Toast[]>([]);

  const push = useCallback((text: string, kind: Toast["kind"] = "ok") => {
    const id = Date.now() + Math.random();
    setItems((prev) => [...prev, { id, text, kind }]);
    setTimeout(() => setItems((prev) => prev.filter((t) => t.id !== id)), 4000);
  }, []);

  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="fixed bottom-4 right-4 z-[60] flex flex-col gap-2">
        {items.map((t) => (
          <div
            key={t.id}
            className={cx(
              "max-w-sm rounded-md border px-3 py-2 text-sm shadow",
              t.kind === "ok" ? "border-up/40 bg-surface" : "border-down/40 bg-surface text-down",
            )}
          >
            {t.text}
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast() {
  return useContext(ToastContext);
}

export function CopyButton({ value, label }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  const toast = useToast();

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
    } catch {
      // Clipboard needs a secure context; fall back to selecting the text.
      toast("无法访问剪贴板，已选中文字", "error");
      const el = document.getElementById(`copy-${label ?? value}`) as HTMLInputElement | null;
      el?.select();
      return;
    }
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  };

  return (
    <Button size="sm" variant="ghost" onClick={copy} title="复制到剪贴板">
      {copied ? <Check size={14} className="text-up" /> : <Copy size={14} />}
      {label ? <span>{label}</span> : null}
    </Button>
  );
}

export function Card({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <div className={cx("rounded-lg border border-line bg-surface p-4 dark:bg-transparent", className)}>{children}</div>
  );
}

export function EmptyState({ children }: { children: ReactNode }) {
  return <div className="rounded-lg border border-dashed border-line px-4 py-10 text-center text-sm text-ink-soft">{children}</div>;
}
