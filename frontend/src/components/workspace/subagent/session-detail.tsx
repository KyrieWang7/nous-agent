"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowUpIcon, Clock3Icon, Loader2Icon, SquareIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { useI18n } from "@/core/i18n/hooks";
import {
  cancelChild,
  childInputs,
  childSessionKey,
  ChildAPIError,
  getChild,
  sendChildInput,
  waitForChild,
} from "@/core/subagents/api";
import { isChildActive } from "@/core/subagents/types";

import { MarkdownContent } from "../messages/markdown-content";

import { SessionConversation } from "./session-conversation";
import { SessionStatus } from "./session-status";

export function SessionDetail({
  threadId,
  sessionId,
  title,
  mainBusy,
  onContinue,
}: {
  threadId: string;
  sessionId: string;
  title?: string;
  mainBusy: boolean;
  onContinue: (sessionId: string, prompt: string) => Promise<void>;
}) {
  const { t } = useI18n();
  const labels = t.subagentWorkspace;
  const client = useQueryClient();
  const key = [...childSessionKey(threadId), sessionId];
  const query = useQuery({
    queryKey: key,
    queryFn: ({ signal }) => getChild(threadId, sessionId, signal),
    refetchInterval: (q) =>
      q.state.data && isChildActive(q.state.data.result.status) ? 1500 : 5000,
    retry: 1,
  });
  const inbox = useQuery({
    queryKey: [...key, "inbox"],
    queryFn: ({ signal }) => childInputs(threadId, sessionId, signal),
    refetchInterval: 2000,
    retry: 1,
  });
  const [command, setCommand] = useState<"cancel" | "send" | null>(null);
  const [waiting, setWaiting] = useState(false);
  const waitAbort = useRef<AbortController | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const draftKey = `nous-child-draft:${threadId}:${sessionId}`;
  const pendingKey = `${draftKey}:pending-message`;
  const [draft, setDraft] = useState("");
  const draftRef = useRef(draft);
  const mounted = useRef(false);
  const lifetime = useRef<AbortController | null>(null);
  const pendingInput = useRef<{ id: string; content: string } | null>(null);
  useEffect(() => {
    mounted.current = true;
    lifetime.current = new AbortController();
    try {
      const pending = sessionStorage.getItem(pendingKey);
      if (pending) {
        const parsed = JSON.parse(pending) as {
          id?: unknown;
          content?: unknown;
        };
        if (typeof parsed.id === "string" && typeof parsed.content === "string")
          pendingInput.current = { id: parsed.id, content: parsed.content };
      }
      const saved = sessionStorage.getItem(draftKey);
      if (saved) {
        setDraft(saved);
        draftRef.current = saved;
      }
    } catch {
      /* storage is optional */
    }
    return () => {
      mounted.current = false;
      lifetime.current?.abort();
    };
  }, [draftKey, pendingKey]);
  const updateDraft = (value: string) => {
    draftRef.current = value;
    setDraft(value);
    try {
      if (value) sessionStorage.setItem(draftKey, value);
      else sessionStorage.removeItem(draftKey);
    } catch {
      /* storage is optional */
    }
  };
  const refresh = () =>
    client.invalidateQueries({ queryKey: childSessionKey(threadId) });
  const explain = (cause: unknown) =>
    cause instanceof ChildAPIError && cause.status === 409
      ? labels.conflict
      : cause instanceof ChildAPIError && cause.status === 404
        ? labels.notFound
        : cause instanceof Error
          ? cause.message
          : labels.loadError;

  const runCommand = async (kind: "cancel" | "send") => {
    if (command || !query.data) return;
    waitAbort.current?.abort();
    setCommand(kind);
    setError(null);
    setNotice(null);
    const signal = lifetime.current?.signal;
    try {
      if (kind === "cancel") {
        const updated = await cancelChild(threadId, sessionId, signal);
        if (mounted.current) client.setQueryData(key, updated);
      } else {
        const submitted = draftRef.current.trim();
        if (!submitted) return;
        if (isChildActive(query.data.result.status)) {
          if (pendingInput.current?.content !== submitted)
            pendingInput.current = {
              id: crypto.randomUUID(),
              content: submitted,
            };
          try {
            sessionStorage.setItem(
              pendingKey,
              JSON.stringify(pendingInput.current),
            );
          } catch {
            /* storage is optional */
          }
          await sendChildInput(
            threadId,
            sessionId,
            pendingInput.current,
            signal,
          );
          pendingInput.current = null;
          try {
            sessionStorage.removeItem(pendingKey);
          } catch {
            /* storage is optional */
          }
        } else {
          await onContinue(sessionId, submitted);
        }
        // A later edit must survive an earlier request completing.
        if (mounted.current && draftRef.current.trim() === submitted)
          updateDraft("");
      }
      if (mounted.current) await refresh();
    } catch (cause) {
      if (mounted.current && !signal?.aborted) {
        setError(explain(cause));
        void refresh();
      }
    } finally {
      if (mounted.current) setCommand(null);
    }
  };

  const runWait = async () => {
    if (waiting || !query.data) return;
    const controller = new AbortController();
    waitAbort.current = controller;
    const signal = lifetime.current
      ? AbortSignal.any([lifetime.current.signal, controller.signal])
      : controller.signal;
    setWaiting(true);
    setError(null);
    setNotice(null);
    try {
      const updated = await waitForChild(threadId, sessionId, signal);
      if (mounted.current && !signal.aborted) {
        client.setQueryData(key, updated);
        if (isChildActive(updated.result.status)) setNotice(labels.waitTimeout);
      }
    } catch (cause) {
      if (mounted.current && !signal.aborted) setError(explain(cause));
    } finally {
      if (mounted.current) setWaiting(false);
    }
  };

  if (!query.data)
    return (
      <div className="text-muted-foreground flex flex-1 flex-col items-center justify-center gap-3 p-6 text-center text-sm">
        {query.isPending ? (
          <Loader2Icon className="size-5 animate-spin" />
        ) : (
          <>
            <p role="alert">{explain(query.error)}</p>
            <Button
              variant="outline"
              size="sm"
              onClick={() => void query.refetch()}
            >
              {labels.retry}
            </Button>
          </>
        )}
      </div>
    );

  const session = query.data;
  const result = session.result;
  const active = isChildActive(result.status);
  const busy = !!command;
  const disabled =
    busy ||
    (session.cancel_requested && active) ||
    (!active && (mainBusy || !!session.parent_session_id));
  return (
    <section
      className="flex min-h-0 flex-1 flex-col"
      aria-label={`${result.subagent_type} · ${sessionId}`}
    >
      <div className="shrink-0 space-y-2 border-b px-4 py-3">
        <div className="flex flex-wrap items-center justify-between gap-2">
          <span className="min-w-0 truncate font-medium" title={title}>
            {title ?? result.subagent_type}
          </span>
          <SessionStatus status={result.status} />
        </div>
        <div className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px]">
          <span title={labels.provider}>
            {result.provider === "fork" ? labels.fork : labels.spawn}
          </span>
          <span>
            {(
              (result.usage.input_tokens ?? 0) +
              (result.usage.output_tokens ?? 0)
            ).toLocaleString()}{" "}
            {labels.tokens}
          </span>
          {result.stop_reason && (
            <span title={labels.stopReason}>{result.stop_reason}</span>
          )}
        </div>
        <p
          className="text-muted-foreground truncate font-mono text-[10px]"
          title={sessionId}
        >
          {sessionId}
        </p>
        {active && (
          <div className="flex gap-2">
            <Button
              variant="outline"
              size="sm"
              className="h-7 text-xs"
              disabled={busy || session.cancel_requested}
              onClick={() => void runCommand("cancel")}
            >
              <SquareIcon className="size-3" />
              {labels.cancel}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="h-7 text-xs"
              disabled={waiting}
              onClick={() => void runWait()}
            >
              {waiting ? (
                <Loader2Icon className="size-3 animate-spin" />
              ) : (
                <Clock3Icon className="size-3" />
              )}
              {waiting ? labels.waiting : labels.wait}
            </Button>
          </div>
        )}
        {session.cancel_requested && active && (
          <p className="text-muted-foreground text-xs" role="status">
            {labels.canceling}
          </p>
        )}
        {query.error && (
          <p className="text-destructive text-xs" role="alert">
            {explain(query.error)}
          </p>
        )}
      </div>
      <SessionConversation
        sessionId={sessionId}
        history={session.history ?? []}
      />
      {(result.error ??
        result.coordination_error ??
        (result.output && result.status !== "completed")) && (
        <div className="bg-destructive/5 max-h-44 shrink-0 space-y-2 overflow-auto border-t px-4 py-3 text-xs">
          {result.error && <p className="text-destructive">{result.error}</p>}
          {result.coordination_error && (
            <p className="text-destructive">
              {labels.coordination}: {result.coordination_error}
            </p>
          )}
          {result.output && result.status !== "completed" && (
            <details>
              <summary className="cursor-pointer font-medium">
                {labels.partial}
              </summary>
              <MarkdownContent
                content={result.output}
                isLoading={false}
                rehypePlugins={[]}
              />
            </details>
          )}
        </div>
      )}
      <div className="bg-background shrink-0 space-y-2 border-t px-4 py-3">
        {!!inbox.data?.length && (
          <details
            className="text-muted-foreground text-xs"
            open={inbox.data.some((m) => m.status === "pending")}
          >
            <summary className="cursor-pointer">
              {labels.inbox} · {inbox.data.length}
            </summary>
            <ul className="mt-2 max-h-28 space-y-2 overflow-auto">
              {inbox.data.map((input) => (
                <li
                  key={input.id}
                  className="flex items-start justify-between gap-3"
                >
                  <span className="min-w-0 break-words whitespace-pre-wrap">
                    {input.content}
                  </span>
                  <span className="shrink-0 text-[10px]">
                    {input.status === "consumed"
                      ? labels.consumed
                      : labels.pending}
                  </span>
                </li>
              ))}
            </ul>
          </details>
        )}
        {inbox.error && (
          <p className="text-destructive text-xs" role="alert">
            {explain(inbox.error)}
          </p>
        )}
        <form
          onSubmit={(event) => {
            event.preventDefault();
            void runCommand("send");
          }}
          className="focus-within:ring-ring rounded-lg border focus-within:ring-1"
        >
          <textarea
            aria-label={labels.placeholder}
            placeholder={labels.placeholder}
            value={draft}
            onChange={(event) => updateDraft(event.target.value)}
            disabled={disabled}
            rows={2}
            maxLength={32000}
            className="block max-h-36 min-h-16 w-full resize-y bg-transparent px-3 py-2 text-sm outline-none disabled:opacity-50"
          />
          <div className="flex justify-end px-2 pb-2">
            <Button
              type="submit"
              size="sm"
              disabled={disabled || !draft.trim()}
              className="h-7 gap-1 text-xs"
            >
              {command === "send" ? (
                <Loader2Icon className="size-3 animate-spin" />
              ) : (
                <ArrowUpIcon className="size-3" />
              )}
              {command === "send"
                ? labels.sending
                : active
                  ? labels.send
                  : labels.continue}
            </Button>
          </div>
        </form>
        <p className="text-muted-foreground text-[11px] leading-relaxed">
          {active
            ? labels.activeHint
            : session.parent_session_id
              ? labels.nestedHint
              : mainBusy
                ? labels.busyHint
                : labels.idleHint}
        </p>
        {notice && (
          <p className="text-muted-foreground text-xs" role="status">
            {notice}
          </p>
        )}
        {error && (
          <p className="text-destructive text-xs" role="alert">
            {error}
          </p>
        )}
      </div>
    </section>
  );
}
