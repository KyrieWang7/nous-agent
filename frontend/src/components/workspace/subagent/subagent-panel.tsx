"use client";

import {
  BotIcon,
  ChevronRightIcon,
  ClipboardListIcon,
  ListTreeIcon,
  RefreshCwIcon,
  XIcon,
} from "lucide-react";
import { useMemo, useRef, useState, type CSSProperties } from "react";

import { Button } from "@/components/ui/button";
import { WorkspaceSideSheet } from "@/components/workspace/workspace-side-sheet";
import { useI18n } from "@/core/i18n/hooks";
import { childTree, type ChildSession } from "@/core/subagents/types";
import type {
  AgentThreadState,
  PendingUserQuestion,
} from "@/core/threads/types";
import type { Message } from "@/core/types";
import { useIsMobile } from "@/hooks/use-mobile";
import { cn } from "@/lib/utils";

import { MarkdownContent } from "../messages/markdown-content";

import { SessionDetail } from "./session-detail";
import { SessionStatus } from "./session-status";

export interface SubagentPanelProps {
  threadId: string;
  sessions: ChildSession[];
  selectedId: string | null;
  loading: boolean;
  error: unknown;
  mainBusy: boolean;
  state: AgentThreadState;
  pendingQuestion: PendingUserQuestion | null;
  tab: "agents" | "plan";
  embedded?: boolean;
  onTab: (tab: "agents" | "plan") => void;
  onSelect: (id: string) => void;
  onContinue: (sessionId: string, prompt: string) => Promise<void>;
  onRefresh: () => void;
  onClose: () => void;
}

function planEntries(messages: Message[]) {
  return messages.flatMap((message) =>
    message.type === "ai"
      ? (message.tool_calls ?? []).flatMap((call) =>
          call.name === "exit_plan_mode" && typeof call.args.plan === "string"
            ? [{ id: call.id ?? `${message.id}-plan`, content: call.args.plan }]
            : [],
        )
      : [],
  );
}

function PlanView({
  state,
  question,
}: {
  state: AgentThreadState;
  question: PendingUserQuestion | null;
}) {
  const { t } = useI18n();
  const labels = t.subagentWorkspace;
  const plans = useMemo(() => planEntries(state.messages), [state.messages]);
  const latest =
    question?.intent === "plan-review"
      ? question.detail
      : plans.at(-1)?.content;
  const todos = state.todos ?? [];
  return (
    <div className="min-h-0 flex-1 space-y-6 overflow-auto p-4 text-sm">
      {todos.length > 0 && (
        <section>
          <h3 className="text-muted-foreground mb-3 flex justify-between text-xs font-medium">
            <span>{labels.tasks}</span>
            <span>
              {todos.filter((todo) => todo.status === "completed").length}/
              {todos.length}
            </span>
          </h3>
          <ol className="space-y-3">
            {todos.map((todo, index) => (
              <li key={`${index}-${todo.content}`} className="flex gap-2.5">
                <span
                  className={cn(
                    "mt-1.5 size-1.5 shrink-0 rounded-full",
                    todo.status === "completed"
                      ? "bg-emerald-600"
                      : todo.status === "in_progress"
                        ? "bg-blue-500"
                        : "bg-muted-foreground/35",
                  )}
                />
                <span
                  className={cn(
                    todo.status === "completed" &&
                      "text-muted-foreground line-through",
                  )}
                >
                  {todo.content}
                </span>
              </li>
            ))}
          </ol>
        </section>
      )}
      {question?.intent === "plan-review" && (
        <p className="bg-muted/40 text-muted-foreground rounded-md border p-3 text-xs">
          {labels.reviewPlan}
        </p>
      )}
      {latest ? (
        <MarkdownContent
          content={latest}
          isLoading={false}
          rehypePlugins={[]}
          className="text-sm [overflow-wrap:anywhere]"
        />
      ) : (
        <div className="text-muted-foreground py-12 text-center">
          <ClipboardListIcon className="mx-auto mb-3 size-6 opacity-50" />
          <p>{labels.noPlan}</p>
          <p className="mt-2 text-xs leading-relaxed">{labels.noPlanHint}</p>
        </div>
      )}
      {plans.length > 1 && (
        <section className="space-y-2 border-t pt-4">
          <h3 className="text-muted-foreground text-xs font-medium">
            {labels.planHistory}
          </h3>
          {plans
            .slice(0, -1)
            .reverse()
            .map((plan, index) => (
              <details key={plan.id} className="rounded-md border">
                <summary className="cursor-pointer px-3 py-2 text-xs">
                  {labels.plan} {plans.length - index - 1}
                </summary>
                <div className="border-t p-3">
                  <MarkdownContent
                    content={plan.content}
                    isLoading={false}
                    rehypePlugins={[]}
                  />
                </div>
              </details>
            ))}
        </section>
      )}
    </div>
  );
}

function sessionTitles(messages: Message[]) {
  const calls = new Map<string, string>();
  const titles = new Map<string, string>();
  for (const message of messages) {
    if (message.type === "ai")
      for (const call of message.tool_calls ?? []) {
        if (
          call.name === "task" &&
          call.id &&
          typeof call.args.description === "string"
        )
          calls.set(call.id, call.args.description);
      }
    if (message.type === "tool" && message.tool_call_id) {
      const id = message.additional_kwargs?.subagent_session_id;
      const title = calls.get(message.tool_call_id);
      if (typeof id === "string" && title && !titles.has(id))
        titles.set(id, title);
    }
  }
  return titles;
}

export function SubagentPanel(props: SubagentPanelProps) {
  const { t } = useI18n();
  const labels = t.subagentWorkspace;
  const titles = useMemo(
    () => sessionTitles(props.state.messages),
    [props.state.messages],
  );
  const rows = useMemo(() => childTree(props.sessions), [props.sessions]);
  return (
    <aside
      onKeyDown={(event) => {
        if (event.key === "Escape" && !props.embedded) {
          event.stopPropagation();
          props.onClose();
        }
      }}
      className="bg-background flex h-full min-h-0 w-full flex-col overflow-hidden"
      aria-label={props.tab === "agents" ? labels.title : labels.plan}
    >
      {!props.embedded && (
        <header className="flex h-12 shrink-0 items-center justify-between gap-2 border-b px-3">
          <div role="tablist" aria-label={labels.title} className="flex gap-1">
            {(["agents", "plan"] as const).map((tab) => (
              <button
                key={tab}
                type="button"
                role="tab"
                id={`execution-tab-${tab}`}
                aria-selected={props.tab === tab}
                aria-controls={`execution-${tab}`}
                onClick={() => props.onTab(tab)}
                className={cn(
                  "flex items-center gap-1.5 rounded-md px-2.5 py-1.5 text-xs transition-colors",
                  props.tab === tab
                    ? "bg-muted text-foreground font-medium"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {tab === "agents" ? (
                  <ListTreeIcon className="size-3.5" />
                ) : (
                  <ClipboardListIcon className="size-3.5" />
                )}
                {tab === "agents" ? labels.title : labels.plan}
                {tab === "agents" && props.sessions.length > 0 && (
                  <span className="text-muted-foreground ml-0.5 font-mono text-[10px]">
                    {props.sessions.length}
                  </span>
                )}
              </button>
            ))}
          </div>
          <div className="flex">
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={labels.refresh}
              onClick={props.onRefresh}
            >
              <RefreshCwIcon className="size-3.5" />
            </Button>
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              aria-label={labels.close}
              onClick={props.onClose}
            >
              <XIcon className="size-4" />
            </Button>
          </div>
        </header>
      )}
      {props.tab === "plan" ? (
        <div
          role={props.embedded ? undefined : "tabpanel"}
          id="execution-plan"
          aria-labelledby={props.embedded ? undefined : "execution-tab-plan"}
          className="flex min-h-0 flex-1 flex-col"
        >
          <PlanView state={props.state} question={props.pendingQuestion} />
        </div>
      ) : (
        <div
          role={props.embedded ? undefined : "tabpanel"}
          id="execution-agents"
          aria-labelledby={props.embedded ? undefined : "execution-tab-agents"}
          className="flex min-h-0 flex-1 flex-col"
        >
          <div className="bg-muted/10 max-h-[30%] shrink-0 overflow-y-auto border-b px-2 py-2">
            <div className="text-muted-foreground mb-1 flex items-center gap-1.5 px-2 py-1 text-[11px]">
              <BotIcon className="size-3.5" />
              {labels.main}
              <ChevronRightIcon className="size-3" />
              {labels.title}
            </div>
            <div
              role="tree"
              aria-label={labels.title}
              onKeyDown={(event) => {
                if (
                  !["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)
                )
                  return;
                const items = [
                  ...event.currentTarget.querySelectorAll<HTMLButtonElement>(
                    '[role="treeitem"]',
                  ),
                ];
                const current = items.indexOf(
                  document.activeElement as HTMLButtonElement,
                );
                const next =
                  event.key === "Home"
                    ? 0
                    : event.key === "End"
                      ? items.length - 1
                      : Math.max(
                          0,
                          Math.min(
                            items.length - 1,
                            current + (event.key === "ArrowDown" ? 1 : -1),
                          ),
                        );
                event.preventDefault();
                items[next]?.focus();
              }}
            >
              {rows.map(({ session, depth }) => (
                <button
                  key={session.id}
                  type="button"
                  role="treeitem"
                  aria-level={depth + 1}
                  aria-selected={props.selectedId === session.id}
                  onClick={() => props.onSelect(session.id)}
                  className={cn(
                    "hover:bg-muted flex w-full items-center gap-2 rounded-md py-2 pr-2 text-left text-xs transition-colors",
                    props.selectedId === session.id && "bg-muted",
                  )}
                  style={{ paddingLeft: 8 + Math.min(depth, 8) * 14 }}
                >
                  <SessionStatus status={session.result.status} compact />
                  <span className="min-w-0 flex-1 truncate">
                    {session.result.description ??
                      titles.get(session.id) ??
                      session.result.subagent_type}
                    <span className="text-muted-foreground ml-2 font-mono text-[10px]">
                      {session.id.slice(0, 8)}
                    </span>
                  </span>
                  <span className="text-muted-foreground text-[10px]">
                    {session.result.provider}
                  </span>
                  <ChevronRightIcon className="text-muted-foreground size-3" />
                </button>
              ))}
            </div>
            {props.loading && (
              <p className="text-muted-foreground px-2 py-3 text-xs">
                {t.common.loading}
              </p>
            )}
            {props.error != null && (
              <div role="alert" className="text-destructive px-2 py-3 text-xs">
                {labels.loadError}
                <Button variant="link" size="sm" onClick={props.onRefresh}>
                  {labels.retry}
                </Button>
              </div>
            )}
          </div>
          {props.selectedId ? (
            <SessionDetail
              key={`${props.threadId}:${props.selectedId}`}
              threadId={props.threadId}
              sessionId={props.selectedId}
              title={
                props.sessions.find((s) => s.id === props.selectedId)?.result
                  .description ?? titles.get(props.selectedId)
              }
              mainBusy={props.mainBusy}
              onContinue={props.onContinue}
            />
          ) : (
            <div className="text-muted-foreground flex flex-1 flex-col items-center justify-center gap-2 p-8 text-center">
              <BotIcon className="mb-2 size-7 opacity-40" />
              <p className="text-sm">
                {props.loading
                  ? t.common.loading
                  : props.error
                    ? labels.loadError
                    : props.sessions.length
                      ? labels.select
                      : labels.empty}
              </p>
              <p className="max-w-64 text-xs leading-relaxed">
                {!props.loading &&
                  !props.error &&
                  (props.sessions.length
                    ? labels.selectHint
                    : labels.emptyHint)}
              </p>
            </div>
          )}
        </div>
      )}
    </aside>
  );
}

export function SubagentDraggablePanel(props: SubagentPanelProps) {
  const { t } = useI18n();
  const mobile = useIsMobile();
  const [width, setWidth] = useState(440);
  const drag = useRef<{ x: number; width: number } | null>(null);
  const resize = (next: number) => setWidth(Math.max(340, Math.min(640, next)));
  if (mobile)
    return (
      <WorkspaceSideSheet
        title={t.subagentWorkspace.title}
        description={t.subagentWorkspace.selectHint}
        onClose={props.onClose}
        restoreFocus="[data-agent-panel-trigger]"
      >
        <SubagentPanel {...props} />
      </WorkspaceSideSheet>
    );
  return (
    <div
      className="fixed inset-0 z-50 flex h-dvh w-full shrink-0 border-l md:static md:z-auto md:h-full md:w-[var(--execution-panel-width)]"
      style={{ "--execution-panel-width": `${width}px` } as CSSProperties}
    >
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label={t.subagentWorkspace.resize}
        aria-valuenow={width}
        aria-valuemin={340}
        aria-valuemax={640}
        tabIndex={0}
        className="focus-visible:bg-ring/40 z-10 -ml-1 hidden w-2 shrink-0 cursor-col-resize touch-none focus-visible:outline-none md:block"
        onKeyDown={(event) => {
          if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
            event.preventDefault();
            resize(width + (event.key === "ArrowLeft" ? 24 : -24));
          }
        }}
        onPointerDown={(event) => {
          event.preventDefault();
          event.currentTarget.setPointerCapture(event.pointerId);
          drag.current = { x: event.clientX, width };
        }}
        onPointerMove={(event) => {
          if (drag.current)
            resize(drag.current.width + drag.current.x - event.clientX);
        }}
        onPointerUp={() => {
          drag.current = null;
        }}
        onPointerCancel={() => {
          drag.current = null;
        }}
      />
      <div className="min-w-0 flex-1">
        <SubagentPanel {...props} />
      </div>
    </div>
  );
}
