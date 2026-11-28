import * as React from "react";
import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/utils";

/**
 * Compact 32px control with crisp elevation in both light and dark modes.
 * Primary actions use the solid brand surface so main CTAs read immediately;
 * neutral and danger actions pair subtle surfaces with tactile border depth.
 */
const buttonVariants = cva(
  cn(
    "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-lg text-[13px] font-medium cursor-pointer",
    "transition-[transform,box-shadow,background-color,border-color,color,filter] duration-120 ease-out",
    "disabled:pointer-events-none disabled:opacity-45",
    "active:translate-y-px",
  ),
  {
    variants: {
      variant: {
        primary: cn(
          "bg-brand-solid text-white border border-brand-solid shadow-xs",
          "hover:brightness-110",
        ),
        neutral: cn(
          "bg-base text-ink border border-line/90 shadow-xs",
          "hover:bg-recessed hover:border-line",
        ),
        danger: cn(
          "bg-danger/10 text-danger border border-danger/25",
          "hover:bg-danger/18 hover:border-danger/40",
        ),
        ghost: cn(
          "bg-transparent text-muted border border-transparent",
          "hover:bg-subtle/70 hover:text-ink",
        ),
      },
      size: {
        default: "h-8 px-3",
        sm: "h-7 px-2.5 text-[12px]",
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
