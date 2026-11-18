"use client";

import { GripVerticalIcon, UsersIcon, XIcon } from "lucide-react";
import { useCallback, useRef, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { WorkspaceSideSheet } from "@/components/workspace/workspace-side-sheet";
import { useI18n } from "@/core/i18n/hooks";
import { useSwarmStream } from "@/core/swarm/hooks";
import { useIsMobile } from "@/hooks/use-mobile";
import { cn } from "@/lib/utils";

import { MessageStream } from "./message-stream";
import { TeammateTabs } from "./teammate-tabs";

export interface SwarmPanelProps {
  teamId: string;
  onClose: () => void;
  className?: string;
  embedded?: boolean;
}

export function SwarmPanel({
  teamId,
  onClose,
  className,
  embedded,
}: SwarmPanelProps) {
  const { t } = useI18n();
  const { members, messages, statuses, connected, error, closed, reconnect } =
    useSwarmStream(teamId);
  const [activeTab, setActiveTab] = useState("all");

  const activeMembers = members.filter((m) => m.status !== "removed");

  return (
    <aside
      className={cn(
        "border-sidebar-border bg-sidebar text-sidebar-foreground animate-swarm-panel-in flex h-full w-full flex-col overflow-hidden",
        className,
      )}
    >
      <header className="border-sidebar-border flex shrink-0 items-start justify-between gap-2 border-b px-3 py-3">
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <h2 className="text-sidebar-foreground text-base font-semibold tracking-tight">
              {t.workbenchPanel.swarm}
            </h2>
            <Badge
              variant="secondary"
              className="border-sidebar-border bg-sidebar-accent text-sidebar-accent-foreground gap-1"
            >
              <UsersIcon className="size-3" aria-hidden />
              {activeMembers.length}
            </Badge>
            {(!connected || closed) && (
              <span className="text-sidebar-foreground/60 text-xs">
                {closed
                  ? t.workbenchPanel.closed
                  : error
                    ? t.workbenchPanel.disconnected
                    : t.workbenchPanel.connecting}
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
        {!embedded && (
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            className="text-sidebar-foreground hover:bg-sidebar-accent hover:text-sidebar-accent-foreground shrink-0"
            aria-label={t.subagentWorkspace.close}
            onClick={onClose}
          >
            <XIcon className="size-4" />
          </Button>
        )}
      </header>

      {error ? (
        <p className="text-destructive px-3 py-2 text-xs">{error}</p>
      ) : null}

      {!connected && !closed && (
        <Button type="button" variant="ghost" size="sm" onClick={reconnect}>
          {t.subagentWorkspace.retry}
        </Button>
      )}
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
  const { t } = useI18n();
  const mobile = useIsMobile();
  const [width, setWidth] = useState(SWARM_DEFAULT_W);
  const dragging = useRef<{ x: number; width: number } | null>(null);
  const resize = useCallback(
    (next: number) =>
      setWidth(Math.min(SWARM_MAX_W, Math.max(SWARM_MIN_W, next))),
    [],
  );
  if (mobile)
    return (
      <WorkspaceSideSheet
        title={t.workbenchPanel.swarm}
        description={t.workbenchPanel.swarmHint}
        onClose={onClose}
        restoreFocus="[data-swarm-panel-trigger]"
      >
        <SwarmPanel teamId={teamId} onClose={onClose} />
      </WorkspaceSideSheet>
    );
  return (
    <div
      className="animate-swarm-panel-in relative flex h-full shrink-0"
      style={{ width }}
    >
      <div
        role="separator"
        tabIndex={0}
        aria-orientation="vertical"
        aria-label={t.workbenchPanel.resize}
        aria-valuenow={width}
        aria-valuemin={SWARM_MIN_W}
        aria-valuemax={SWARM_MAX_W}
        onKeyDown={(event) => {
          if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
            event.preventDefault();
            resize(width + (event.key === "ArrowLeft" ? 24 : -24));
          }
        }}
        onPointerDown={(event) => {
          event.preventDefault();
          event.currentTarget.setPointerCapture(event.pointerId);
          dragging.current = { x: event.clientX, width };
        }}
        onPointerMove={(event) => {
          if (dragging.current)
            resize(dragging.current.width + dragging.current.x - event.clientX);
        }}
        onPointerUp={() => {
          dragging.current = null;
        }}
        onPointerCancel={() => {
          dragging.current = null;
        }}
        className="group hover:bg-primary/10 absolute top-0 left-0 z-50 flex h-full w-2 -translate-x-1/2 cursor-col-resize items-center justify-center"
      >
        <div className="bg-border flex h-8 w-3 items-center justify-center rounded-sm border opacity-0 transition-opacity group-hover:opacity-100">
          <GripVerticalIcon className="size-2.5" />
        </div>
      </div>
      <div className="border-sidebar-border h-full w-full overflow-hidden border-l">
        <SwarmPanel teamId={teamId} onClose={onClose} />
      </div>
    </div>
  );
}
