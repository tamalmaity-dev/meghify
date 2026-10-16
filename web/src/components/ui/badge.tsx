import * as React from "react";
import { cn } from "@/lib/utils";
import { toneFor } from "@/lib/types";

type Tone = "ok" | "warn" | "danger" | "neutral";

const toneClasses: Record<Tone, string> = {
  ok: "text-ok",
  warn: "text-warn",
  danger: "text-danger",
  neutral: "text-black/60 dark:text-white/50",
};

const dotClasses: Record<Tone, string> = {
  ok: "bg-ok",
  warn: "bg-warn",
  danger: "bg-danger",
  neutral: "bg-black/30 dark:bg-white/30",
};

/**
 * Resource status is shown as a small neutral pill with a semantic dot, never a
 * full coloured rectangle. That keeps dozens of rows readable at once.
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
        "inline-flex items-center gap-1.5 rounded-full bg-white/[0.04] px-2 py-0.5 text-[11px] font-medium whitespace-nowrap",
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
        "inline-flex items-center rounded-full bg-white/[0.05] px-2 py-0.5 text-[11px] font-medium",
        "text-black/65 dark:text-white/60",
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
      {icon && <div className="text-black/30 dark:text-white/25">{icon}</div>}
      <div className="text-[13px] font-medium">{title}</div>
      {description && (
        <p className="max-w-sm text-[12px] text-black/50 dark:text-white/45">{description}</p>
      )}
      {action && <div className="mt-2">{action}</div>}
    </div>
  );
}
