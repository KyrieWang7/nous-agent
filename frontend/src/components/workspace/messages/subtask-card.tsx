"use client";

import { BotIcon, ChevronDownIcon, ChevronRightIcon } from "lucide-react";
import { useState } from "react";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/core/i18n/hooks";
import { hasToolCalls } from "@/core/messages/utils";
import { useChildSessions } from "@/core/subagents/context";
import { isChildActive } from "@/core/subagents/types";
import type { Subtask } from "@/core/tasks/types";
import { explainLastToolCall } from "@/core/tools/utils";
import { cn } from "@/lib/utils";

import { SessionStatus } from "../subagent/session-status";

import { MarkdownContent } from "./markdown-content";

export function SubtaskCard({
  task,
  isLoading,
  className,
}: {
  task: Subtask;
  isLoading: boolean;
  className?: string;
}) {
  const { t } = useI18n();
  const [expanded, setExpanded] = useState(false);
  const children = useChildSessions();
  const session = children?.sessions.find((s) =>
    task.session_id ? s.id === task.session_id : s.result.task_id === task.id,
  );
  const currentTask = session?.result.task_id === task.id;
  const status =
    (currentTask ? session?.result.status : undefined) ??
    (task.final_status === "polling_timed_out"
      ? "interrupted"
      : task.final_status) ??
    (task.status === "in_progress" ? "running" : task.status);
  const output =
    (currentTask ? session?.result.output : undefined) ?? task.result;
  const error = (currentTask ? session?.result.error : undefined) ?? task.error;
  const unresolved =
    !isLoading &&
    task.status === "in_progress" &&
    !task.final_status &&
    !currentTask;
  const active = !unresolved && isChildActive(status);
  return (
    <section
      className={cn("bg-card overflow-hidden rounded-lg border", className)}
      aria-label={task.description || task.subagent_type}
    >
      <div className="flex items-center gap-1 pr-2">
        <button
          type="button"
          className="flex min-w-0 flex-1 items-center gap-2.5 px-3 py-3 text-left"
          aria-expanded={expanded}
          onClick={() => setExpanded(!expanded)}
        >
          <BotIcon className="text-muted-foreground size-4 shrink-0" />
          <span className="min-w-0 flex-1 truncate text-sm">
            {task.description || task.subagent_type}
          </span>
          {unresolved ? (
            <span className="text-muted-foreground text-xs">
              {t.subagentWorkspace.noTerminal}
            </span>
          ) : task.final_status === "polling_timed_out" && !currentTask ? (
            <span className="text-muted-foreground text-xs">
              {t.subagentWorkspace.waitEnded}
            </span>
          ) : (
            <SessionStatus status={status} />
          )}
          <ChevronDownIcon
            className={cn(
              "text-muted-foreground size-3.5 shrink-0 transition-transform",
              expanded && "rotate-180",
            )}
          />
        </button>
        {session && (
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={t.subagentWorkspace.open}
            onClick={() => children?.openSession(session.id)}
          >
            <ChevronRightIcon className="size-4" />
          </Button>
        )}
      </div>
      {expanded && (
        <div className="space-y-3 border-t px-4 py-3 text-sm">
          <div className="text-muted-foreground flex items-center gap-2 text-xs">
            <span>{task.subagent_type}</span>
            {session && <span>· {session.result.provider}</span>}
          </div>
          {task.prompt && (
            <details>
              <summary className="text-muted-foreground cursor-pointer text-xs">
                {t.subagentWorkspace.user}
              </summary>
              <div className="mt-2">
                <MarkdownContent
                  content={task.prompt}
                  isLoading={false}
                  rehypePlugins={[]}
                />
              </div>
            </details>
          )}
          {active &&
            isLoading &&
            task.latestMessage &&
            hasToolCalls(task.latestMessage) && (
              <p className="text-muted-foreground text-xs">
                {explainLastToolCall(task.latestMessage, t)}
              </p>
            )}
          {output && (
            <div>
              <p className="text-muted-foreground mb-1 text-xs font-medium">
                {status === "completed"
                  ? t.subagentWorkspace.assistant
                  : t.subagentWorkspace.partial}
              </p>
              <MarkdownContent
                content={output}
                isLoading={false}
                rehypePlugins={[]}
              />
            </div>
          )}
          {error && <p className="text-destructive text-xs">{error}</p>}
          {currentTask && session?.result.coordination_error && (
            <p className="text-destructive text-xs">
              {session.result.coordination_error}
            </p>
          )}
          {session && (
            <Button
              variant="outline"
              size="sm"
              className="h-7 text-xs"
              onClick={() => children?.openSession(session.id)}
            >
              {t.subagentWorkspace.open}
              <ChevronRightIcon className="size-3" />
            </Button>
          )}
        </div>
      )}
    </section>
  );
}
