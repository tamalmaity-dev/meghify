import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

/**
 * The panel's single button treatment: a compact 32px control with a 2px bottom
 * depth. Hover lifts the face by 1px, press pushes it into the edge. That depth
 * cue is what makes a dense operator UI feel tactile instead of flat.
 */
const buttonVariants = cva(
  cn(
    "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md text-[13px] font-medium",
    "h-8 px-3 border-b-2 transition-[transform,box-shadow,background-color,color] duration-100 ease-out",
    "disabled:pointer-events-none disabled:opacity-50",
    "active:translate-y-[2px] active:border-b-0",
  ),
  {
    variants: {
      variant: {
        primary: cn(
          "bg-brand/10 text-brand border-b-brand/40 hover:bg-brand/15 hover:-translate-y-px hover:border-b-[3px]",
          "dark:text-brand",
        ),
        neutral: cn(
          "bg-white/[0.04] text-current border-b-white/10 hover:bg-white/[0.08] hover:-translate-y-px hover:border-b-[3px]",
          "dark:bg-white/[0.04]",
        ),
        danger: cn(
          "bg-danger/10 text-danger border-b-danger/40 hover:bg-danger/15 hover:-translate-y-px hover:border-b-[3px]",
        ),
        ghost: "h-8 px-2 border-b-0 hover:bg-white/5 text-current",
      },
      size: {
        default: "h-8 px-3",
        sm: "h-7 px-2 text-[12px]",
        icon: "h-8 w-8 p-0",
      },
    },
    defaultVariants: { variant: "neutral", size: "default" },
  },
);

export interface ButtonProps
  extends React.ButtonHTMLAttributes<HTMLButtonElement>,
    VariantProps<typeof buttonVariants> {}

export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant, size, ...props }, ref) => (
    <button
      ref={ref}
      className={cn(buttonVariants({ variant, size }), className)}
      {...props}
    />
  ),
);
Button.displayName = "Button";

export { buttonVariants };
