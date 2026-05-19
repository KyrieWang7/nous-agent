"use client";

import { useEffect, useRef, useState } from "react";

import type { SwarmMessage } from "@/core/swarm/types";
import { cn } from "@/lib/utils";

function formatRelativeTime(iso: string): string {
  const t = new Date(iso).getTime();
  if (Number.isNaN(t)) return "";
  const now = Date.now();
  const sec = Math.max(0, Math.floor((now - t) / 1000));
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

function messageMatchesFilter(msg: SwarmMessage, filter: string): boolean {
  if (filter === "all") return true;
  return msg.from === filter || msg.to === filter;
}

export interface MessageStreamProps {
  messages: SwarmMessage[];
  filter: string;
  className?: string;
}

export function MessageStream({
  messages,
  filter,
  className,
}: MessageStreamProps) {
  const containerRef = useRef<HTMLDivElement>(null);
  const prevIdsRef = useRef<Set<number>>(new Set());
  const [enteringIds, setEnteringIds] = useState<Set<number>>(new Set());

  const filtered = messages.filter((m) => messageMatchesFilter(m, filter));

  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    requestAnimationFrame(() => {
      el.scrollTop = el.scrollHeight;
    });
  }, [messages, filtered]);

  useEffect(() => {
    const prev = prevIdsRef.current;
    const isInitialBulk = prev.size === 0 && messages.length > 0;

    const nextNew = new Set<number>();
    for (const m of messages) {
      if (!prev.has(m.id)) nextNew.add(m.id);
    }
    prevIdsRef.current = new Set(messages.map((m) => m.id));

    if (isInitialBulk || nextNew.size === 0) return;

    setEnteringIds(nextNew);
    const clear = window.setTimeout(() => {
      setEnteringIds(new Set());
    }, 520);
    return () => window.clearTimeout(clear);
  }, [messages]);

  return (
    <div
      ref={containerRef}
      className={cn(
        "min-h-0 flex-1 overflow-y-auto overflow-x-hidden px-2 py-2",
        className,
      )}
    >
      <div className="flex flex-col gap-2">
        {filtered.length === 0 ? (
          <p className="text-muted-foreground py-6 text-center text-sm">
            No messages yet.
          </p>
        ) : (
          filtered.map((msg) => {
            if (msg.from === "system") {
              const systemStyle = msg.content.startsWith("[Joined]")
                ? "text-blue-500"
                : msg.content.startsWith("[Completed]")
                  ? "text-green-600 dark:text-green-400"
                  : msg.content.startsWith("[Failed]")
                    ? "text-red-500"
                    : msg.content.startsWith("[Timeout]")
                      ? "text-orange-500"
                      : "text-muted-foreground";

              return (
                <div
                  key={msg.id}
                  className="my-2 flex items-center gap-3 text-xs"
                >
                  <div className="border-border h-px flex-1 border-t border-dashed" />
                  <span className={cn("max-w-[min(100%,280px)] shrink text-center font-medium", systemStyle)}>
                    {msg.content}
                  </span>
                  <div className="border-border h-px flex-1 border-t border-dashed" />
                </div>
              );
            }

            const isEntering = enteringIds.has(msg.id);

            return (
              <div
                key={msg.id}
                className={cn(
                  "rounded-md px-2 py-1.5",
                  isEntering && "animate-swarm-bg-flash",
                )}
              >
                <div className={cn(isEntering && "animate-swarm-msg-in")}>
                  <p className="text-muted-foreground mb-1 text-[11px] leading-tight">
                    {msg.from} → {msg.to}
                  </p>
                  <p className="text-foreground text-sm leading-snug whitespace-pre-wrap break-words">
                    {msg.content}
                  </p>
                  <p className="text-muted-foreground mt-1 text-[11px]">
                    {formatRelativeTime(msg.created_at)}
                  </p>
                </div>
              </div>
            );
          })
        )}
      </div>
    </div>
  );
}
