import type { LucideIcon } from "lucide-react";
import type { ComponentProps } from "react";
import { cn } from "@/lib/utils";

const sizes = {
  sm: "size-8",
  md: "size-9",
} as const;

export interface IconButtonProps
  extends Omit<ComponentProps<"button">, "aria-label" | "title"> {
  label: string;
  icon: LucideIcon;
  size?: keyof typeof sizes;
  tone?: "neutral" | "danger";
}

export function IconButton({
  label,
  icon: Icon,
  size = "md",
  tone = "neutral",
  className,
  type = "button",
  ...props
}: IconButtonProps) {
  return (
    <button
      aria-label={label}
      className={cn(
        "grid shrink-0 place-items-center rounded-field text-muted transition-colors duration-fast hover:bg-surface-muted hover:text-ink focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/40 disabled:cursor-not-allowed disabled:opacity-40",
        tone === "danger" && "hover:bg-danger-soft hover:text-danger",
        sizes[size],
        className,
      )}
      title={label}
      type={type}
      {...props}
    >
      <Icon aria-hidden="true" className="size-4" />
    </button>
  );
}
