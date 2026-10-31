import { Loader2 } from "lucide-react";
import {
  type StatusMeta,
  statusBadgeClass,
  statusDotClass,
  statusTextClass,
  type Tone,
} from "@/lib/status";
import { cn } from "@/lib/utils";

export function StatusBadge({
  status,
  compact = false,
  plain = false,
  className,
}: {
  status: StatusMeta;
  compact?: boolean;
  plain?: boolean;
  className?: string;
}) {
  const Icon = status.icon;
  const spinning = Icon === Loader2;

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 text-meta font-medium",
        plain
          ? statusTextClass(status.tone)
          : cn(
              "rounded-chip border px-2 py-0.5",
              statusBadgeClass(status.tone),
            ),
        className,
      )}
    >
      <Icon
        aria-hidden="true"
        className={cn("size-3.5 shrink-0", spinning && "animate-spin")}
      />
      {compact ? status.shortLabel : status.label}
    </span>
  );
}

export function StatusDot({
  tone,
  className,
}: {
  tone: Tone;
  className?: string;
}) {
  return (
    <span
      aria-hidden="true"
      className={cn(
        "size-1.5 shrink-0 rounded-full",
        statusDotClass(tone),
        className,
      )}
    />
  );
}
