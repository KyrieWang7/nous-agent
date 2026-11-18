import {
  CircleCheckIcon,
  CircleDashedIcon,
  CirclePauseIcon,
  CircleXIcon,
  Loader2Icon,
} from "lucide-react";

import { useI18n } from "@/core/i18n/hooks";
import type { ChildStatus } from "@/core/subagents/types";
import { cn } from "@/lib/utils";

export function SessionStatus({
  status,
  compact = false,
}: {
  status: ChildStatus;
  compact?: boolean;
}) {
  const { t } = useI18n();
  const active = status === "pending" || status === "running";
  const Icon = active
    ? Loader2Icon
    : status === "completed"
      ? CircleCheckIcon
      : status === "cancelled" || status === "interrupted"
        ? CirclePauseIcon
        : status === "failed" || status === "timed_out"
          ? CircleXIcon
          : CircleDashedIcon;
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1.5 text-xs",
        active
          ? "text-blue-600 dark:text-blue-400"
          : status === "completed"
            ? "text-emerald-700 dark:text-emerald-400"
            : status === "failed" || status === "timed_out"
              ? "text-destructive"
              : "text-muted-foreground",
      )}
    >
      <Icon
        className={cn(
          "size-3.5",
          active && "animate-spin motion-reduce:animate-none",
        )}
        aria-hidden
      />
      <span className={compact ? "sr-only" : undefined}>
        {t.subagentWorkspace.statuses[status]}
      </span>
    </span>
  );
}
