"use client";

import { FolderOpen } from "lucide-react";

import { Button } from "@/components/ui/button";

interface DramaEmptyProps {
  title?: string;
  description?: string;
  actionLabel?: string;
  onAction?: () => void;
  icon?: React.ReactNode;
}

export function DramaEmpty({
  title = "暂无数据",
  description = "暂无数据，请先创建",
  actionLabel,
  onAction,
  icon,
}: DramaEmptyProps) {
  return (
    <div className="flex flex-col items-center justify-center py-16 text-center">
      <div className="flex items-center justify-center w-24 h-24 rounded-full bg-muted mb-4">
        {icon ?? <FolderOpen className="h-12 w-12 text-muted-foreground" />}
      </div>
      <h3 className="text-lg font-medium mb-2">{title}</h3>
      <p className="text-sm text-muted-foreground max-w-sm mb-6">
        {description}
      </p>
      {actionLabel && onAction && (
        <Button onClick={onAction} size="lg">
          {actionLabel}
        </Button>
      )}
    </div>
  );
}
