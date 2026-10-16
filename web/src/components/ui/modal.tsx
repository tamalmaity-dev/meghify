import * as React from "react";
import { Button } from "./button";
import { cn } from "@/lib/utils";

/**
 * A modal built on the panel's layer-card anatomy: an elevated header strip, a
 * nested body, and right-aligned footer actions below a divider. Content-width
 * on desktop, full width on mobile.
 */
export function Modal({
  open,
  onClose,
  title,
  description,
  children,
  footer,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  description?: string;
  children: React.ReactNode;
  footer?: React.ReactNode;
}) {
  React.useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-start justify-center overflow-y-auto p-4 pt-[8vh]">
      <div
        className="fixed inset-0 bg-black/50 backdrop-blur-[2px]"
        onClick={onClose}
        aria-hidden
      />
      <div
        role="dialog"
        aria-modal="true"
        aria-label={title}
        className={cn(
          "relative z-10 w-full max-w-lg rounded-[8px] card-lift bg-base shadow-[var(--shadow-modal)]",
          "border border-line/70",
        )}
      >
        <div className="flex items-start justify-between gap-4 rounded-t-[8px] bg-white/[0.03] px-4 py-3">
          <div>
            <h2 className="text-[15px] font-semibold">{title}</h2>
            {description && (
              <p className="mt-0.5 text-[11px] text-black/50 dark:text-white/45">
                {description}
              </p>
            )}
          </div>
          <Button variant="ghost" size="icon" onClick={onClose} aria-label="Close">
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M18 6 6 18M6 6l12 12" />
            </svg>
          </Button>
        </div>

        <div className="p-4">{children}</div>

        {footer && (
          <div className="flex items-center justify-end gap-2 border-t border-line/50 px-4 py-3">
            {footer}
          </div>
        )}
      </div>
    </div>
  );
}
