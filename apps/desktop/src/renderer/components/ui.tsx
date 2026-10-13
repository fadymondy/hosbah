import { useEffect, useRef, type ButtonHTMLAttributes, type ReactNode } from "react";
import clsx from "clsx";
import { Loader2, X } from "lucide-react";
import type { Health, Overall } from "@hosbah/client";
import { useApp } from "../lib/app";
import type { BridgeError } from "../lib/bridge";

export const healthTone: Record<Health, string> = { ok: "text-ok-text bg-ok/15", warn: "text-warn-text bg-warn/20", down: "text-danger-text bg-danger/15" };
export const healthDot: Record<Health, string> = { ok: "bg-ok", warn: "bg-warn", down: "bg-danger" };
export const overallDot: Record<Overall, string> = { green: "bg-ok", amber: "bg-warn", red: "bg-danger" };

export function Dot({ health, className }: { health: Health; className?: string }) {
  return <span aria-hidden className={clsx("inline-block size-2.5 shrink-0 rounded-full", healthDot[health], className)} />;
}

export function Badge({ health, children }: { health: Health; children: ReactNode }) {
  return (
    <span className={clsx("inline-flex items-center gap-1.5 rounded-full px-2.5 py-0.5 text-xs font-medium whitespace-nowrap", healthTone[health])}>
      <Dot health={health} className="size-1.5" />
      {children}
    </span>
  );
}

export function Card({ className, children, ...rest }: { className?: string; children: ReactNode } & React.HTMLAttributes<HTMLDivElement>) {
  return (
    <div className={clsx("rounded-xl border border-line bg-card", className)} {...rest}>
      {children}
    </div>
  );
}

type BtnProps = ButtonHTMLAttributes<HTMLButtonElement> & { variant?: "primary" | "ghost" | "danger" | "soft"; busy?: boolean };
export function Button({ variant = "soft", busy, className, children, disabled, ...rest }: BtnProps) {
  const v = {
    primary: "bg-action text-on-action hover:opacity-90",
    soft: "bg-soft text-fg hover:bg-line/60 border border-line",
    ghost: "text-body hover:bg-soft",
    danger: "bg-danger text-white hover:opacity-90",
  }[variant];
  return (
    <button
      className={clsx("inline-flex items-center justify-center gap-2 rounded-lg px-3.5 py-2 text-sm font-medium transition disabled:cursor-not-allowed disabled:opacity-50", v, className)}
      disabled={disabled || busy}
      {...rest}
    >
      {busy && <Loader2 className="size-4 animate-[spin_1s_linear_infinite]" />}
      {children}
    </button>
  );
}

export function Meter({ label, value, sub }: { label: string; value: number | null; sub?: string }) {
  const v = value === null ? 0 : Math.min(100, Math.max(0, value));
  const tone = v >= 90 ? "bg-danger" : v >= 75 ? "bg-warn" : "bg-ok";
  return (
    <div className="min-w-0">
      <div className="mb-1 flex items-baseline justify-between gap-2 text-xs">
        <span className="text-muted">{label}</span>
        <span className="ltr font-medium text-fg">{value === null ? "-" : `${Math.round(v)}%`}</span>
      </div>
      <div className="h-1.5 overflow-hidden rounded-full bg-soft">
        <div className={clsx("h-full rounded-full", tone)} style={{ width: `${v}%` }} />
      </div>
      {sub && <div className="ltr mt-1 text-[11px] text-muted">{sub}</div>}
    </div>
  );
}

export function PageHeader({ title, sub, actions, back }: { title: ReactNode; sub?: ReactNode; actions?: ReactNode; back?: ReactNode }) {
  return (
    <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div className="min-w-0">
        {back}
        <h1 className="truncate text-xl font-semibold text-fg">{title}</h1>
        {sub && <div className="mt-0.5 text-sm text-muted">{sub}</div>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}

export function Empty({ children }: { children: ReactNode }) {
  return <div className="rounded-xl border border-dashed border-line px-6 py-10 text-center text-sm text-muted">{children}</div>;
}

export function Spinner() {
  return (
    <div className="flex justify-center py-10 text-muted">
      <Loader2 className="size-6 animate-[spin_1s_linear_infinite]" />
    </div>
  );
}

export function ErrorNote({ error, onRetry }: { error: BridgeError; onRetry?: () => void }) {
  const { t } = useApp();
  return (
    <div className="flex items-center justify-between gap-3 rounded-xl border border-danger/40 bg-danger/10 px-4 py-3 text-sm text-danger-text">
      <span>{error.offline ? t("error.network") : error.forbidden ? t("error.forbidden") : error.message || t("error.generic")}</span>
      {onRetry && (
        <Button variant="ghost" onClick={onRetry}>
          {t("common.retry")}
        </Button>
      )}
    </div>
  );
}

export function Dialog({ title, onClose, children, footer }: { title: string; onClose: () => void; children: ReactNode; footer: ReactNode }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    ref.current?.querySelector<HTMLElement>("input,button")?.focus();
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return (
    <div className="fixed inset-0 z-50 grid place-items-center bg-black/50 p-6" role="dialog" aria-modal="true" aria-label={title}>
      <div ref={ref} className="w-full max-w-md rounded-2xl border border-line bg-elevated p-6 shadow-2xl">
        <div className="mb-3 flex items-center justify-between gap-3">
          <h2 className="text-lg font-semibold text-fg">{title}</h2>
          <button onClick={onClose} aria-label="close" className="rounded-md p-1 text-muted hover:bg-soft">
            <X className="size-4" />
          </button>
        </div>
        {children}
        <div className="mt-5 flex justify-end gap-2">{footer}</div>
      </div>
    </div>
  );
}

export function Segmented<T extends string>({ value, options, onChange, label }: { value: T; options: { value: T; label: string }[]; onChange: (v: T) => void; label: string }) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex rounded-lg border border-line bg-soft p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          role="radio"
          aria-checked={o.value === value}
          onClick={() => onChange(o.value)}
          className={clsx("rounded-md px-3 py-1 text-sm transition", o.value === value ? "bg-card font-medium text-fg shadow-sm" : "text-muted hover:text-fg")}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function Toggle({ checked, onChange, label, desc }: { checked: boolean; onChange: (v: boolean) => void; label: string; desc?: string }) {
  return (
    <label className="flex cursor-pointer items-center justify-between gap-4 py-2.5">
      <span className="min-w-0">
        <span className="block text-sm font-medium text-fg">{label}</span>
        {desc && <span className="block text-xs text-muted">{desc}</span>}
      </span>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={clsx("relative h-6 w-11 shrink-0 rounded-full transition", checked ? "bg-action" : "bg-line")}
      >
        <span className={clsx("absolute top-0.5 size-5 rounded-full bg-white shadow transition-all", checked ? "start-[22px]" : "start-0.5")} />
      </button>
    </label>
  );
}
