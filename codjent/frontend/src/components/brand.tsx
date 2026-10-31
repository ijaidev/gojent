import { cn } from "@/lib/utils";

export function CodjentMark({ className }: { className?: string }) {
  return (
    <svg
      aria-hidden="true"
      className={cn("size-7", className)}
      viewBox="0 0 32 32"
      fill="none"
    >
      <rect width="32" height="32" rx="9" fill="var(--brand-mark-bg)" />
      <path
        d="M12.25 10.5 7.75 16l4.5 5.5"
        stroke="var(--brand-mark-fg)"
        strokeWidth="2.1"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path
        d="M17.5 21.5h6.75"
        stroke="var(--brand-mark-fg)"
        strokeWidth="2.1"
        strokeLinecap="round"
      />
      <circle cx="20.5" cy="13.5" r="1.75" fill="var(--accent)" />
    </svg>
  );
}

export function CodjentWordmark({ className }: { className?: string }) {
  return (
    <span
      className={cn(
        "text-title font-semibold tracking-[-0.035em] text-ink",
        className,
      )}
    >
      codjent
    </span>
  );
}
