import type { TeammateDisplayStatus } from "@/core/swarm/types";
import { cn } from "@/lib/utils";

export interface StatusIndicatorProps {
  status: TeammateDisplayStatus;
  className?: string;
}

export function StatusIndicator({ status, className }: StatusIndicatorProps) {
  return (
    <span
      role="presentation"
      className={cn(
        "inline-block size-2 shrink-0 rounded-full",
        status === "waiting" && "bg-blue-500 animate-pulse",
        status === "active" && "bg-green-500 animate-pulse",
        status === "running" && "bg-green-500 animate-swarm-running-pulse",
        status === "completed" && "bg-neutral-400 dark:bg-neutral-500",
        status === "failed" && "bg-red-500 animate-swarm-shake",
        status === "timeout" && "bg-orange-500",
        className,
      )}
    />
  );
}
