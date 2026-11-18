import { useI18n } from "@/core/i18n/hooks";
import type { TeammateDisplayStatus } from "@/core/swarm/types";
import { cn } from "@/lib/utils";

export interface StatusIndicatorProps {
  status: TeammateDisplayStatus;
  className?: string;
}

export function StatusIndicator({ status, className }: StatusIndicatorProps) {
  const { t } = useI18n();
  const labels = {
    waiting: t.workbenchPanel.waiting,
    active: t.workbenchPanel.ready,
    running: t.subagentWorkspace.statuses.running,
    completed: t.subagentWorkspace.statuses.completed,
    failed: t.subagentWorkspace.statuses.failed,
    timeout: t.subagentWorkspace.statuses.timed_out,
  };
  return (
    <span
      role="img"
      aria-label={labels[status]}
      title={labels[status]}
      className={cn(
        "inline-block size-2 shrink-0 rounded-full",
        status === "waiting" && "animate-pulse bg-blue-500",
        status === "active" && "animate-pulse bg-green-500",
        status === "running" && "animate-swarm-running-pulse bg-green-500",
        status === "completed" && "bg-neutral-400 dark:bg-neutral-500",
        status === "failed" && "animate-swarm-shake bg-red-500",
        status === "timeout" && "bg-orange-500",
        className,
      )}
    />
  );
}
