"use client";

import { useDramaStore } from "@/core/drama/store";
import type { SubView } from "@/core/drama/store";
import { cn } from "@/lib/utils";

interface NavItem {
  id: SubView;
  label: string;
  icon: React.ReactNode;
}

const navItems: NavItem[] = [
  {
    id: "overview",
    label: "项目概览",
    icon: <span className="text-lg">📊</span>,
  },
  {
    id: "originalText",
    label: "小说原文",
    icon: <span className="text-lg">📖</span>,
  },
  {
    id: "outline",
    label: "大纲管理",
    icon: <span className="text-lg">📝</span>,
  },
  {
    id: "script",
    label: "剧本管理",
    icon: <span className="text-lg">🎬</span>,
  },
  {
    id: "assets",
    label: "资产管理",
    icon: <span className="text-lg">🖼️</span>,
  },
];

interface DramaNavProps {
  className?: string;
}

export function DramaNav({ className }: DramaNavProps) {
  const currentSubView = useDramaStore((s) => s.currentSubView);
  const setCurrentSubView = useDramaStore((s) => s.setCurrentSubView);

  return (
    <div
      className={cn(
        "flex gap-2 overflow-x-auto py-2 scrollbar-hide",
        className
      )}
    >
      {navItems.map((item) => (
        <button
          key={item.id}
          onClick={() => setCurrentSubView(item.id)}
          className={cn(
            "flex items-center gap-2 px-4 py-2 rounded-lg text-sm font-medium whitespace-nowrap transition-colors",
            currentSubView === item.id
              ? "bg-primary text-primary-foreground"
              : "bg-muted hover:bg-muted/80 text-muted-foreground"
          )}
        >
          {item.icon}
          <span>{item.label}</span>
        </button>
      ))}
    </div>
  );
}
