"use client";

import { useEffect, useRef, useState } from "react";

import type {
  SwarmTeamMember,
  TeammateDisplayStatus,
} from "@/core/swarm/types";
import { cn } from "@/lib/utils";

import { StatusIndicator } from "./status-indicator";

const ALL_TAB = "all";

export interface TeammateTabsProps {
  members: SwarmTeamMember[];
  statuses: Record<string, TeammateDisplayStatus>;
  activeTab: string;
  onTabChange: (name: string) => void;
  className?: string;
}

function displayStatusFor(
  member: SwarmTeamMember,
  statuses: Record<string, TeammateDisplayStatus>,
): TeammateDisplayStatus {
  const explicit = statuses[member.name];
  if (explicit) return explicit;

  if (member.status === "removed") return "completed";
  if (member.status === "completed") return "completed";
  if (member.status === "failed") return "failed";
  if (member.status === "running") return "running";
  if (member.status === "active") return "active";
  return "waiting";
}

export function TeammateTabs({
  members,
  statuses,
  activeTab,
  onTabChange,
  className,
}: TeammateTabsProps) {
  const seenRef = useRef<Set<string>>(new Set());
  const [newNames, setNewNames] = useState<Set<string>>(new Set());

  useEffect(() => {
    const nextNew = new Set<string>();
    for (const m of members) {
      if (!seenRef.current.has(m.name)) {
        if (m.status !== "removed") nextNew.add(m.name);
        seenRef.current.add(m.name);
      }
    }
    if (nextNew.size > 0) {
      setNewNames(nextNew);
      const t = window.setTimeout(() => {
        setNewNames(new Set());
      }, 400);
      return () => window.clearTimeout(t);
    }
  }, [members]);

  const visibleMembers = [...members].sort((a, b) => {
    if (a.status === "removed" && b.status !== "removed") return 1;
    if (a.status !== "removed" && b.status === "removed") return -1;
    return 0;
  });

  return (
    <div
      className={cn(
        "flex shrink-0 gap-1 overflow-x-auto border-b border-border px-1 py-0.5",
        className,
      )}
      role="tablist"
    >
      <button
        type="button"
        role="tab"
        aria-selected={activeTab === ALL_TAB}
        className={cn(
          "flex shrink-0 items-center gap-1.5 rounded-t-md px-2.5 py-2 text-sm transition-colors",
          activeTab === ALL_TAB
            ? "border-b-2 border-primary font-medium text-foreground"
            : "border-b-2 border-transparent text-muted-foreground hover:text-foreground",
        )}
        onClick={() => {
          onTabChange(ALL_TAB);
        }}
      >
        All
      </button>
      {visibleMembers.map((m) => (
        <button
          key={m.name}
          type="button"
          role="tab"
          aria-selected={activeTab === m.name}
          className={cn(
            "flex shrink-0 items-center gap-1.5 rounded-t-md px-2.5 py-2 text-sm transition-colors",
            newNames.has(m.name) && "animate-swarm-tab-in",
            activeTab === m.name
              ? "border-b-2 border-primary font-medium text-foreground"
              : "border-b-2 border-transparent text-muted-foreground hover:text-foreground",
          )}
          onClick={() => {
            onTabChange(m.name);
          }}
        >
          <StatusIndicator status={displayStatusFor(m, statuses)} />
          <span className={cn("max-w-[120px] truncate", m.status === "removed" && "opacity-60")}>{m.name}</span>
        </button>
      ))}
    </div>
  );
}
