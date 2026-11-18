"use client";

import { useEffect, useRef } from "react";

import { useI18n } from "@/core/i18n/hooks";
import type { ChildMessage } from "@/core/subagents/types";
import { cn } from "@/lib/utils";

import { MarkdownContent } from "../messages/markdown-content";

export function SessionConversation({
  history,
  sessionId,
}: {
  history: ChildMessage[];
  sessionId: string;
}) {
  const { t } = useI18n();
  const scroller = useRef<HTMLDivElement>(null);
  const follow = useRef(true);
  useEffect(() => {
    follow.current = true;
  }, [sessionId]);
  useEffect(() => {
    if (follow.current && scroller.current)
      scroller.current.scrollTop = scroller.current.scrollHeight;
  }, [history]);

  return (
    <div
      ref={scroller}
      onScroll={() => {
        const el = scroller.current;
        if (el)
          follow.current =
            el.scrollHeight - el.scrollTop - el.clientHeight < 80;
      }}
      className="min-h-0 flex-1 overflow-y-auto overscroll-contain px-4 py-5"
      aria-label={t.subagentWorkspace.history}
    >
      {history.length === 0 && (
        <p className="text-muted-foreground py-6 text-center text-sm">
          {t.subagentWorkspace.noHistory}
        </p>
      )}
      <div className="space-y-5">
        {history.map((message, index) => {
          const content =
            message.content ??
            message.content_blocks
              ?.filter((b) => b.type === "text")
              .map((b) => b.text ?? "")
              .join("\n") ??
            "";
          if (message.role === "system") return null;
          if (message.role === "tool")
            return (
              <details
                key={`${sessionId}-${index}`}
                className={cn(
                  "bg-muted/20 rounded-md border text-xs",
                  message.is_error && "border-destructive/30",
                )}
              >
                <summary className="text-muted-foreground cursor-pointer px-3 py-2 font-mono">
                  {message.name ?? t.subagentWorkspace.tool}
                  {message.is_error ? " · !" : ""}
                </summary>
                <pre className="max-h-64 overflow-auto border-t p-3 break-words whitespace-pre-wrap">
                  {content}
                </pre>
              </details>
            );
          if (!content && !message.tool_calls?.length) return null;
          return (
            <article
              key={`${sessionId}-${index}`}
              className={cn(
                "min-w-0 text-sm",
                message.role === "user" &&
                  "bg-muted/25 rounded-lg border px-3 py-2.5",
              )}
            >
              <p className="text-muted-foreground mb-2 text-[11px] font-medium tracking-wide">
                {message.role === "user"
                  ? t.subagentWorkspace.user
                  : t.subagentWorkspace.assistant}
              </p>
              <MarkdownContent
                content={content}
                isLoading={false}
                rehypePlugins={[]}
                className="text-sm [overflow-wrap:anywhere] [&_pre]:max-w-full [&_pre]:overflow-auto"
              />
              {message.tool_calls?.map((call) => (
                <details key={call.id} className="mt-2 rounded border text-xs">
                  <summary className="text-muted-foreground cursor-pointer px-3 py-2 font-mono">
                    {call.name}
                  </summary>
                  <pre
                    className="max-h-48 overflow-auto border-t p-3 break-words whitespace-pre-wrap"
                    aria-label={t.subagentWorkspace.request}
                  >
                    {typeof call.arguments === "string"
                      ? call.arguments
                      : JSON.stringify(call.arguments, null, 2)}
                  </pre>
                </details>
              ))}
            </article>
          );
        })}
      </div>
    </div>
  );
}
