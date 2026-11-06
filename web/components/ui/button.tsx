import Link from "next/link";
import type { ComponentProps } from "react";
import { cx } from "@/lib/format";

type Variant = "primary" | "secondary" | "destructive" | "ghost";
type Size = "sm" | "md";

const base =
  "inline-flex items-center justify-center gap-2 rounded-input font-medium whitespace-nowrap transition-colors duration-150 ease-out disabled:cursor-not-allowed disabled:opacity-50";

const variants: Record<Variant, string> = {
  primary: "bg-fg text-bg hover:bg-muted",
  secondary: "border border-border-strong text-fg hover:bg-surface",
  destructive: "border border-error text-error hover:bg-surface",
  ghost: "text-muted hover:bg-surface hover:text-fg",
};

const sizes: Record<Size, string> = {
  sm: "h-8 px-3 text-13",
  md: "h-10 px-4 text-14 max-md:h-11",
};

export function buttonClass(variant: Variant = "secondary", size: Size = "md") {
  return cx(base, variants[variant], sizes[size]);
}

type ButtonProps = ComponentProps<"button"> & { variant?: Variant; size?: Size };

export function Button({ variant, size, className, type = "button", ...props }: ButtonProps) {
  return <button type={type} className={cx(buttonClass(variant, size), className)} {...props} />;
}

type LinkButtonProps = ComponentProps<typeof Link> & { variant?: Variant; size?: Size };

export function LinkButton({ variant, size, className, ...props }: LinkButtonProps) {
  return <Link className={cx(buttonClass(variant, size), className)} {...props} />;
}
