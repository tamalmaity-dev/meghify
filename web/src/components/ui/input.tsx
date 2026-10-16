import * as React from "react";
import { cn } from "@/lib/utils";

/** A compact 32px control, matching every other input in the panel. */
export const Input = React.forwardRef<HTMLInputElement, React.ComponentProps<"input">>(
  ({ className, ...props }, ref) => (
    <input
      ref={ref}
      className={cn(
        "h-8 w-full rounded-md bg-recessed px-2.5 text-[13px] text-current",
        "ring-1 ring-line/70 placeholder:text-black/35 dark:placeholder:text-white/30",
        "transition-shadow outline-none focus:ring-2 focus:ring-brand",
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
      "w-full rounded-md bg-recessed px-2.5 py-2 font-mono text-[12px] text-current",
      "ring-1 ring-line/70 placeholder:text-black/35 dark:placeholder:text-white/30",
      "transition-shadow outline-none focus:ring-2 focus:ring-brand",
      className,
    )}
    {...props}
  />
));
Textarea.displayName = "Textarea";

export const Select = React.forwardRef<
  HTMLSelectElement,
  React.ComponentProps<"select">
>(({ className, ...props }, ref) => (
  <select
    ref={ref}
    className={cn(
      "h-8 w-full appearance-none rounded-md bg-recessed px-2.5 pr-7 text-[13px] text-current",
      "ring-1 ring-line/70 outline-none focus:ring-2 focus:ring-brand",
      "bg-[url('data:image/svg+xml;utf8,<svg xmlns=%22http://www.w3.org/2000/svg%22 viewBox=%220 0 24 24%22 fill=%22none%22 stroke=%22currentColor%22 stroke-width=%222%22><path d=%22m6 9 6 6 6-6%22/></svg>')] bg-no-repeat bg-[position:right_0.4rem_center] bg-[size:14px]",
      className,
    )}
    {...props}
  />
));
Select.displayName = "Select";

export const Label = React.forwardRef<
  HTMLLabelElement,
  React.ComponentProps<"label">
>(({ className, ...props }, ref) => (
  <label
    ref={ref}
    className={cn("mb-1 block text-[11px] font-medium text-black/60 dark:text-white/55", className)}
    {...props}
  />
));
Label.displayName = "Label";

export const Switch = React.forwardRef<
  HTMLButtonElement,
  React.ComponentProps<"button"> & { checked: boolean }
>(({ checked, className, ...props }, ref) => (
  <button
    ref={ref}
    type="button"
    role="switch"
    aria-checked={checked}
    onClick={() => props.onChange?.({ target: { value: !checked } } as never)}
    className={cn(
      "relative h-4 w-7 shrink-0 rounded-full transition-colors",
      checked ? "bg-brand" : "bg-white/15",
      className,
    )}
    {...props}
  >
    <span
      className={cn(
        "absolute top-0.5 left-0.5 h-3 w-3 rounded-full bg-white transition-transform",
        checked && "translate-x-3",
      )}
    />
  </button>
));
Switch.displayName = "Switch";
