import { Loader2, type LucideIcon } from "lucide-react";
import type { ComponentProps, ReactNode } from "react";
import { cn } from "@/lib/utils";

type Variant = "primary" | "secondary" | "ghost" | "danger";
type Size = "sm" | "md";

const variants: Record<Variant, string> = {
  primary:
    "border border-accent bg-accent text-app hover:bg-accent/90 disabled:hover:bg-accent",
  secondary:
    "border border-line-strong bg-surface text-ink hover:bg-surface-muted",
  ghost:
    "border border-transparent text-muted hover:bg-surface-muted hover:text-ink",
  danger: "border border-danger bg-danger text-white hover:opacity-90",
};

const sizes: Record<Size, string> = {
  sm: "h-8 px-2.5 text-meta",
  md: "h-9 px-3 text-body-sm",
};

export interface ButtonProps extends ComponentProps<"button"> {
  variant?: Variant;
  size?: Size;
  loading?: boolean;
  icon?: LucideIcon;
  children?: ReactNode;
}

export function Button({
  variant = "secondary",
  size = "md",
  loading = false,
  icon: Icon,
  className,
  children,
  disabled,
  type = "button",
  ...props
}: ButtonProps) {
  return (
    <button
      aria-busy={loading || undefined}
      className={cn(
        "inline-flex shrink-0 items-center justify-center gap-1.5 rounded-field font-medium transition-colors duration-fast focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-50",
        variants[variant],
        sizes[size],
        className,
      )}
      disabled={disabled || loading}
      type={type}
      {...props}
    >
      {loading ? (
        <Loader2 aria-hidden="true" className="size-3.5 animate-spin" />
      ) : Icon ? (
        <Icon aria-hidden="true" className="size-3.5" />
      ) : null}
      {children}
    </button>
  );
}
