"use client";

import { GripVerticalIcon, UsersIcon, XIcon } from "lucide-react";
import { useCallback, useRef, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { useSwarmStream } from "@/core/swarm/hooks";
import { cn } from "@/lib/utils";

import { MessageStream } from "./message-stream";
import { TeammateTabs } from "./teammate-tabs";

export interface SwarmPanelProps {
  teamId: string;
  onClose: () => void;
  className?: string;
}

export function SwarmPanel({ teamId, onClose, className }: SwarmPanelProps) {
  const { members, messages, statuses, connected, error } =
    useSwarmStream(teamId);
  const [activeTab, setActiveTab] = useState("all");

  const activeMembers = members.filter((m) => m.status !== "removed");

  return (
    <aside
      className={cn(
        "flex h-full w-full flex-col overflow-hidden border-sidebar-border bg-sidebar text-sidebar-foreground animate-swarm-panel-in",
        className,
      )}
    >
      <header className="flex shrink-0 items-start justify-between gap-2 border-b border-sidebar-border px-3 py-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-sidebar-foreground text-base font-semibold tracking-tight">
              Swarm
            </h2>
            <Badge
              variant="secondary"
              className="border-sidebar-border bg-sidebar-accent text-sidebar-accent-foreground gap-1"
            >
              <UsersIcon className="size-3" aria-hidden />
              {activeMembers.length}
            </Badge>
            {!connected && (
              <span className="text-sidebar-foreground/60 text-xs">
                Connecting…
              </span>
            )}
          </div>
          <p
            className="text-sidebar-foreground/50 mt-0.5 truncate font-mono text-[11px]"
            title={teamId}
          >
            {teamId}
          </p>
        </div>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          className="text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground shrink-0"
          aria-label="Close swarm panel"
          onClick={onClose}
        >
          <XIcon className="size-4" />
        </Button>
      </header>

      {error ? (
        <p className="text-destructive px-3 py-2 text-xs">{error}</p>
      ) : null}

      <TeammateTabs
        members={members}
        statuses={statuses}
        activeTab={activeTab}
        onTabChange={setActiveTab}
        className="border-sidebar-border border-b"
      />

      <MessageStream messages={messages} filter={activeTab} />
    </aside>
  );
}

const SWARM_MIN_W = 260;
const SWARM_MAX_W = 600;
const SWARM_DEFAULT_W = 340;

export function SwarmDraggablePanel({
  teamId,
  onClose,
}: {
  teamId: string;
  onClose: () => void;
}) {
  const [width, setWidth] = useState(SWARM_DEFAULT_W);
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
        const next = Math.min(SWARM_MAX_W, Math.max(SWARM_MIN_W, startW.current + delta));
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
        <SwarmPanel teamId={teamId} onClose={onClose} />
      </div>
    </div>
  );
}
