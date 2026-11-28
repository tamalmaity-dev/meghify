import * as React from "react";
import { cn } from "@/lib/utils";

/** A compact 32px control, matching every other input in the panel. */
export const Input = React.forwardRef<HTMLInputElement, React.ComponentProps<"input">>(
  ({ className, ...props }, ref) => (
    <input
      ref={ref}
      className={cn(
        "h-8 w-full rounded-lg bg-recessed px-2.5 text-[13px] text-ink",
        "border border-line/85 placeholder:text-faint",
        "transition-[border-color,box-shadow] outline-none focus:border-brand focus:ring-2 focus:ring-brand/25",
        "disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      {...props}
    />
  ),
);
Input.displayName = "Input";

export const Textarea = React.forwardRef<
  HTMLTextAreaElement,
  React.ComponentProps<"textarea">
>(({ className, ...props }, ref) => (
  <textarea
    ref={ref}
    className={cn(
      "w-full rounded-lg bg-recessed px-2.5 py-2 font-mono text-[12px] text-ink",
      "border border-line/85 placeholder:text-faint",
      "transition-[border-color,box-shadow] outline-none focus:border-brand focus:ring-2 focus:ring-brand/25",
      "disabled:cursor-not-allowed disabled:opacity-50",
      className,
    )}
    {...props}
  />
));
Textarea.displayName = "Textarea";

export const Select = React.forwardRef<
  HTMLSelectElement,
  React.ComponentProps<"select">
>(({ className, children, ...props }, ref) => (
  <div className={cn("relative inline-block w-full", className)}>
    <select
      ref={ref}
      className={cn(
        "h-8 w-full appearance-none rounded-lg bg-recessed px-2.5 pr-7 text-[13px] text-ink",
        "border border-line/85 outline-none transition-[border-color,box-shadow]",
        "focus:border-brand focus:ring-2 focus:ring-brand/25",
        "disabled:cursor-not-allowed disabled:opacity-50",
      )}
      {...props}
    >
      {children}
    </select>
    <span
      aria-hidden
      className="select-chevron-icon pointer-events-none absolute top-1/2 right-2 h-3.5 w-3.5 -translate-y-1/2"
    />
  </div>
));
Select.displayName = "Select";

export const Label = React.forwardRef<
  HTMLLabelElement,
  React.ComponentProps<"label">
>(({ className, ...props }, ref) => (
  <label
    ref={ref}
    className={cn("mb-1 block text-[11.5px] font-medium text-muted", className)}
    {...props}
  />
));
Label.displayName = "Label";

export const Switch = React.forwardRef<
  HTMLButtonElement,
  React.ComponentProps<"button"> & { checked: boolean }
>(({ checked, className, onChange, onClick, ...props }, ref) => (
  <button
    ref={ref}
    type="button"
    role="switch"
    aria-checked={checked}
    onClick={(event) => {
      onClick?.(event);
      onChange?.({ target: { value: !checked } } as never);
    }}
    className={cn(
      "relative h-5 w-8.5 shrink-0 cursor-pointer rounded-full border transition-colors",
      checked
        ? "border-brand-solid bg-brand-solid"
        : "border-line bg-subtle",
      "disabled:cursor-not-allowed disabled:opacity-50",
      className,
    )}
    {...props}
  >
    <span
      className={cn(
        "absolute top-0.5 left-0.5 h-3.5 w-3.5 rounded-full bg-white shadow-xs transition-transform",
        checked && "translate-x-3.5",
      )}
    />
  </button>
));
Switch.displayName = "Switch";
