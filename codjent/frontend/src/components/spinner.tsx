import { Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";

export function Spinner({
  className,
  label = "Loading",
}: {
  className?: string;
  label?: string;
}) {
  return (
    <output
      aria-live="polite"
      className={cn(
        "inline-flex size-3.5 shrink-0 items-center justify-center",
        className,
      )}
    >
      <Loader2 aria-hidden="true" className="size-full animate-spin" />
      <span className="sr-only">{label}</span>
    </output>
  );
}
