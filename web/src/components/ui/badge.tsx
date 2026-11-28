import * as React from "react";
import { cn } from "@/lib/utils";
import { toneFor } from "@/lib/types";

type Tone = "ok" | "warn" | "danger" | "neutral";

const toneClasses: Record<Tone, string> = {
  ok: "bg-ok/12 text-ok border-ok/25",
  warn: "bg-warn/12 text-warn border-warn/25",
  danger: "bg-danger/12 text-danger border-danger/25",
  neutral: "bg-subtle/75 text-muted border-line/70",
};

const dotClasses: Record<Tone, string> = {
  ok: "bg-ok",
  warn: "bg-warn",
  danger: "bg-danger",
  neutral: "bg-faint",
};

/**
 * Resource status pill with a semantic dot and balanced contrast across light
 * and dark themes.
 */
export function StatusBadge({
  status,
  className,
}: {
  status: string;
  className?: string;
}) {
  const tone = toneFor(status);
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[11px] font-medium whitespace-nowrap",
        toneClasses[tone],
        className,
      )}
    >
      <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", dotClasses[tone])} />
      {status}
    </span>
  );
}

export function Badge({
  children,
  className,
}: {
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full border border-line/60 bg-subtle/70 px-2 py-0.5 text-[11px] font-medium text-muted",
        className,
      )}
    >
      {children}
    </span>
  );
}

export function EmptyState({
  title,
  description,
  icon,
  action,
}: {
  title: string;
  description?: string;
  icon?: React.ReactNode;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex flex-col items-center justify-center gap-2 px-6 py-12 text-center">
      {icon && (
        <div className="flex h-10 w-10 items-center justify-center rounded-xl border border-line/60 bg-recessed text-muted">
          {icon}
        </div>
      )}
      <div className="text-[13.5px] font-semibold text-ink">{title}</div>
      {description && (
        <p className="max-w-sm text-[12px] leading-relaxed text-muted">{description}</p>
      )}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}
