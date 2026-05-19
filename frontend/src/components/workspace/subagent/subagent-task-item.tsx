"use client";

import {
  CheckCircle2Icon,
  ChevronDownIcon,
  Loader2Icon,
  XCircleIcon,
} from "lucide-react";
import { useMemo, useState } from "react";

import { Shimmer } from "@/components/ai-elements/shimmer";
import { hasToolCalls } from "@/core/messages/utils";
import type { Subtask } from "@/core/tasks/types";
import { explainLastToolCall } from "@/core/tools/utils";
import { useI18n } from "@/core/i18n/hooks";
import { cn } from "@/lib/utils";

export interface SubagentTaskItemProps {
  task: Subtask;
  className?: string;
}

export function SubagentTaskItem({ task, className }: SubagentTaskItemProps) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);

  const statusIcon = useMemo(() => {
    switch (task.status) {
      case "in_progress":
        return <Loader2Icon className="size-4 shrink-0 animate-spin text-blue-500" />;
      case "completed":
        return <CheckCircle2Icon className="size-4 shrink-0 text-green-600 dark:text-green-400" />;
      case "failed":
        return <XCircleIcon className="size-4 shrink-0 text-red-500" />;
    }
  }, [task.status]);

  const currentAction = useMemo(() => {
    if (task.status !== "in_progress") return null;
    if (task.latestMessage && hasToolCalls(task.latestMessage)) {
      return explainLastToolCall(task.latestMessage, t);
    }
    return "Working…";
  }, [task, t]);

  return (
    <div
      className={cn(
        "rounded-lg border bg-background/80 transition-colors",
        task.status === "in_progress" && "border-blue-500/30 shadow-sm",
        task.status === "completed" && "border-border/60 opacity-80",
        task.status === "failed" && "border-red-500/30",
        className,
      )}
    >
      {/* Header row */}
      <button
        type="button"
        className="flex w-full items-center gap-2 px-3 py-2 text-left"
        onClick={() => setExpanded(!expanded)}
      >
        {statusIcon}
        <div className="min-w-0 flex-1">
          <p className="truncate text-sm font-medium text-foreground">
            {task.status === "in_progress" ? (
              <Shimmer duration={3} spread={3}>
                {task.description}
              </Shimmer>
            ) : (
              task.description
            )}
          </p>
          {currentAction && (
            <p className="mt-0.5 truncate text-xs text-muted-foreground">
              {currentAction}
            </p>
          )}
        </div>
        <ChevronDownIcon
          className={cn(
            "size-4 shrink-0 text-muted-foreground transition-transform",
            expanded && "rotate-180",
          )}
        />
      </button>

      {/* Expanded detail */}
      {expanded && (
        <div className="border-t border-border/50 px-3 py-2">
          {/* Type badge */}
          <div className="mb-1.5 flex items-center gap-2">
            <span className="rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
              {task.subagent_type}
            </span>
          </div>

          {/* Prompt */}
          {task.prompt && (
            <div className="mb-2">
              <p className="text-[11px] font-medium text-muted-foreground mb-0.5">
                Prompt
              </p>
              <p className="text-xs text-foreground/80 line-clamp-4 whitespace-pre-wrap">
                {task.prompt}
              </p>
            </div>
          )}

          {/* Result */}
          {task.status === "completed" && task.result && (
            <div className="mb-1">
              <p className="text-[11px] font-medium text-green-600 dark:text-green-400 mb-0.5">
                Result
              </p>
              <p className="text-xs text-foreground/80 line-clamp-6 whitespace-pre-wrap">
                {task.result}
              </p>
            </div>
          )}

          {/* Error */}
          {task.status === "failed" && task.error && (
            <div className="mb-1">
              <p className="text-[11px] font-medium text-red-500 mb-0.5">
                Error
              </p>
              <p className="text-xs text-red-500/80 line-clamp-4 whitespace-pre-wrap">
                {task.error}
              </p>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
