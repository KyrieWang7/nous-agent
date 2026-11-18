"use client";

import { XIcon } from "lucide-react";
import { useRef, useState, type ReactNode } from "react";

import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useI18n } from "@/core/i18n/hooks";

import { WorkspaceSideSheet } from "./workspace-side-sheet";

export type WorkbenchTab = "agents" | "plan" | "files" | "swarm";

export function WorkspaceWorkbench({
  tab,
  mobile,
  hasSwarm,
  onTab,
  onClose,
  restoreFocus,
  children,
}: {
  tab: WorkbenchTab;
  mobile: boolean;
  hasSwarm: boolean;
  onTab: (tab: WorkbenchTab) => void;
  onClose: () => void;
  restoreFocus: () => HTMLElement | null;
  children: ReactNode;
}) {
  const { t } = useI18n();
  const [width, setWidth] = useState(440);
  const drag = useRef<{ x: number; width: number } | null>(null);
  const minWidth = 320;
  const maxWidth = 680;
  const resize = (value: number) =>
    setWidth(Math.min(maxWidth, Math.max(minWidth, value)));
  const labels = {
    agents: t.subagentWorkspace.title,
    plan: t.subagentWorkspace.plan,
    files: t.workbenchPanel.files,
    swarm: t.workbenchPanel.swarm,
  };
  const available: WorkbenchTab[] = hasSwarm
    ? ["agents", "plan", "files", "swarm"]
    : ["agents", "plan", "files"];
  const content = (
    <Tabs
      value={tab}
      onValueChange={(value) => {
        if (available.includes(value as WorkbenchTab))
          onTab(value as WorkbenchTab);
      }}
      className="size-full min-h-0 gap-0"
      onKeyDown={(event) => {
        if (event.key === "Escape" && !event.defaultPrevented && !mobile)
          onClose();
      }}
    >
      <div className="flex shrink-0 items-center justify-between gap-1 border-b px-2">
        <TabsList
          variant="line"
          aria-label={t.workbenchPanel.title}
          className="min-w-0 justify-start overflow-x-auto"
        >
          {available.map((item) => (
            <TabsTrigger key={item} value={item} className="px-1.5 text-xs">
              {labels[item]}
            </TabsTrigger>
          ))}
        </TabsList>
        <Button
          type="button"
          size="icon-sm"
          variant="ghost"
          aria-label={t.subagentWorkspace.close}
          onClick={onClose}
        >
          <XIcon className="size-4" />
        </Button>
      </div>
      <TabsContent value={tab} className="min-h-0 overflow-hidden">
        {children}
      </TabsContent>
    </Tabs>
  );
  if (mobile)
    return (
      <WorkspaceSideSheet
        title={t.workbenchPanel.title}
        description={t.workbenchPanel.filesHint}
        restoreFocus={restoreFocus}
        onClose={onClose}
      >
        {content}
      </WorkspaceSideSheet>
    );
  return (
    <aside
      className="bg-background relative flex h-full min-h-0 shrink-0 border-l"
      style={{ width: `min(${width}px, 58%)` }}
    >
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label={t.subagentWorkspace.resize}
        aria-valuemin={minWidth}
        aria-valuemax={maxWidth}
        aria-valuenow={width}
        tabIndex={0}
        className="focus-visible:bg-ring/40 absolute inset-y-0 -left-1 z-10 w-2 cursor-col-resize touch-none"
        onPointerDown={(event) => {
          event.currentTarget.setPointerCapture(event.pointerId);
          drag.current = { x: event.clientX, width };
        }}
        onPointerMove={(event) => {
          if (drag.current)
            resize(drag.current.width + drag.current.x - event.clientX);
        }}
        onLostPointerCapture={() => {
          drag.current = null;
        }}
        onKeyDown={(event) => {
          if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
          event.preventDefault();
          resize(width + (event.key === "ArrowLeft" ? 24 : -24));
        }}
      />
      {content}
    </aside>
  );
}
