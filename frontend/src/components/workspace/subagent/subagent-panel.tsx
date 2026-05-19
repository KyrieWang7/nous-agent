"use client";

import {
  BotIcon,
  CheckCircle2Icon,
  GripVerticalIcon,
  Loader2Icon,
  XCircleIcon,
  XIcon,
} from "lucide-react";
import { useCallback, useRef, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useSubtaskContext } from "@/core/tasks/context";
import type { Subtask } from "@/core/tasks/types";
import type { TokenUsage } from "@/core/types/thread";
import { cn } from "@/lib/utils";

import { TokenUsageIndicator } from "../token-usage-indicator";

import { SubagentTaskItem } from "./subagent-task-item";

export interface SubagentPanelProps {
  onClose: () => void;
  tokenUsage: TokenUsage | null;
  className?: string;
}

export function SubagentPanel({
  onClose,
  tokenUsage,
  className,
}: SubagentPanelProps) {
  const { tasks } = useSubtaskContext();
  const taskList = Object.values(tasks);

  const inProgress = taskList.filter((t) => t.status === "in_progress");
  const completed = taskList.filter((t) => t.status === "completed");
  const failed = taskList.filter((t) => t.status === "failed");

  return (
    <aside
      className={cn(
        "flex h-full w-full flex-col overflow-hidden border-sidebar-border bg-sidebar text-sidebar-foreground animate-swarm-panel-in",
        className,
      )}
    >
      {/* Header */}
      <header className="flex shrink-0 items-start justify-between gap-2 border-b border-sidebar-border px-3 py-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-sidebar-foreground text-base font-semibold tracking-tight">
              Subagents
            </h2>
            {inProgress.length > 0 && (
              <Badge
                variant="secondary"
                className="border-sidebar-border bg-sidebar-accent text-sidebar-accent-foreground gap-1"
              >
                <Loader2Icon className="size-3 animate-spin" aria-hidden />
                {inProgress.length}
              </Badge>
            )}
            {completed.length > 0 && (
              <Badge
                variant="secondary"
                className="border-sidebar-border bg-sidebar-accent text-green-600 dark:text-green-400 gap-1"
              >
                <CheckCircle2Icon className="size-3" aria-hidden />
                {completed.length}
              </Badge>
            )}
            {failed.length > 0 && (
              <Badge
                variant="secondary"
                className="border-sidebar-border bg-sidebar-accent text-red-500 gap-1"
              >
                <XCircleIcon className="size-3" aria-hidden />
                {failed.length}
              </Badge>
            )}
          </div>
          {/* Token usage summary */}
          {tokenUsage && (
            <div className="mt-1.5">
              <TokenUsageIndicator tokenUsage={tokenUsage} />
            </div>
          )}
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground shrink-0"
          aria-label="Close subagent panel"
          onClick={onClose}
        >
          <XIcon className="size-4" />
        </Button>
      </header>

      {/* Task list */}
      <div className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
        {taskList.length === 0 ? (
          <div className="flex flex-col items-center justify-center gap-2 py-12 text-center">
            <BotIcon className="text-muted-foreground/40 size-8" />
            <p className="text-muted-foreground text-sm">
              No subagent tasks yet.
            </p>
            <p className="text-muted-foreground/60 text-xs">
              Tasks will appear here when the agent delegates work.
            </p>
          </div>
        ) : (
          <div className="flex flex-col gap-2">
            {/* In progress first */}
            {inProgress.length > 0 && (
              <SectionHeader
                label="Running"
                count={inProgress.length}
                color="text-blue-500"
              />
            )}
            {inProgress.map((task) => (
              <SubagentTaskItem key={task.id} task={task} />
            ))}

            {/* Completed */}
            {completed.length > 0 && (
              <SectionHeader
                label="Completed"
                count={completed.length}
                color="text-green-600 dark:text-green-400"
              />
            )}
            {completed.map((task) => (
              <SubagentTaskItem key={task.id} task={task} />
            ))}

            {/* Failed */}
            {failed.length > 0 && (
              <SectionHeader
                label="Failed"
                count={failed.length}
                color="text-red-500"
              />
            )}
            {failed.map((task) => (
              <SubagentTaskItem key={task.id} task={task} />
            ))}
          </div>
        )}
      </div>
    </aside>
  );
}

function SectionHeader({
  label,
  count,
  color,
}: {
  label: string;
  count: number;
  color: string;
}) {
  return (
    <div className="mt-2 mb-1 flex items-center gap-2 px-1">
      <span className={cn("text-xs font-medium", color)}>
        {label} ({count})
      </span>
      <div className="border-border h-px flex-1 border-t" />
    </div>
  );
}

// ---------------------------------------------------------------------------
// Draggable wrapper (same pattern as SwarmDraggablePanel)
// ---------------------------------------------------------------------------

const PANEL_MIN_W = 260;
const PANEL_MAX_W = 560;
const PANEL_DEFAULT_W = 320;

export function SubagentDraggablePanel({
  onClose,
  tokenUsage,
}: {
  onClose: () => void;
  tokenUsage: TokenUsage | null;
}) {
  const [width, setWidth] = useState(PANEL_DEFAULT_W);
  const dragging = useRef(false);
  const startX = useRef(0);
  const startW = useRef(0);

  const onPointerDown = useCallback(
    (e: React.PointerEvent) => {
      e.preventDefault();
      dragging.current = true;
      startX.current = e.clientX;
      startW.current = width;

      const onMove = (ev: PointerEvent) => {
        if (!dragging.current) return;
        const delta = startX.current - ev.clientX;
        const next = Math.min(
          PANEL_MAX_W,
          Math.max(PANEL_MIN_W, startW.current + delta),
        );
        setWidth(next);
      };

      const onUp = () => {
        dragging.current = false;
        document.removeEventListener("pointermove", onMove);
        document.removeEventListener("pointerup", onUp);
      };

      document.addEventListener("pointermove", onMove);
      document.addEventListener("pointerup", onUp);
    },
    [width],
  );

  return (
    <div
      className="relative flex h-full shrink-0 animate-swarm-panel-in"
      style={{ width }}
    >
      <div
        onPointerDown={onPointerDown}
        className="group absolute top-0 left-0 z-50 flex h-full w-2 -translate-x-1/2 cursor-col-resize items-center justify-center hover:bg-primary/10"
      >
        <div className="flex h-8 w-3 items-center justify-center rounded-sm border bg-border opacity-0 transition-opacity group-hover:opacity-100">
          <GripVerticalIcon className="size-2.5" />
        </div>
      </div>
      <div className="h-full w-full overflow-hidden border-l border-sidebar-border">
        <SubagentPanel onClose={onClose} tokenUsage={tokenUsage} />
      </div>
    </div>
  );
}
