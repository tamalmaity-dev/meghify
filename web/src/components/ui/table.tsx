import * as React from "react";
import { cn } from "@/lib/utils";

/**
 * Dense collection table. Clean header strip, comfortable row padding, subtle
 * theme-aware row hover, and clean divider lines.
 */
export function Table({
  head,
  children,
  className,
}: {
  head: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("overflow-x-auto", className)}>
      <table className="w-full border-collapse text-[13px]">
        <thead>
          <tr className="h-9 border-b border-line/60 bg-recessed/35 text-left text-[11px] font-medium uppercase tracking-wider text-muted">
            {head}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}

export function Th({
  children,
  className,
  align = "left",
}: {
  children?: React.ReactNode;
  className?: string;
  align?: "left" | "right" | "center";
}) {
  return (
    <th
      className={cn(
        "px-3.5 py-2 font-medium",
        align === "right" && "text-right",
        align === "center" && "text-center",
        className,
      )}
    >
      {children}
    </th>
  );
}

export function Td({
  children,
  className,
  align = "left",
}: {
  children?: React.ReactNode;
  className?: string;
  align?: "left" | "right" | "center";
}) {
  return (
    <td
      className={cn(
        "h-12 border-t border-line/45 px-3.5 align-middle",
        align === "right" && "text-right",
        align === "center" && "text-center",
        className,
      )}
    >
      {children}
    </td>
  );
}

export function Tr({
  children,
  className,
  onClick,
}: {
  children: React.ReactNode;
  className?: string;
  onClick?: () => void;
}) {
  return (
    <tr
      onClick={onClick}
      className={cn(
        "border-t border-line/45 transition-colors hover:bg-recessed/55",
        onClick && "cursor-pointer",
        className,
      )}
    >
      {children}
    </tr>
  );
}

export function TableToolbar({
  left,
  right,
}: {
  left?: React.ReactNode;
  right?: React.ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-line/50 px-3.5 py-2.5">
      <div className="min-w-0 flex-1">{left}</div>
      <div className="flex shrink-0 items-center gap-2">{right}</div>
    </div>
  );
}

export function TableFooter({ children }: { children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between border-t border-line/50 px-3.5 py-2 text-[11px] text-muted">
      {children}
    </div>
  );
}
