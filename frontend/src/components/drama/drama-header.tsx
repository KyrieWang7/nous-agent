"use client";

import { useRouter } from "next/navigation";
import { ChevronLeft } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useDramaStore } from "@/core/drama/store";

interface DramaHeaderProps {
  title?: string;
  showBack?: boolean;
  backPath?: string;
  actions?: React.ReactNode;
}

export function DramaHeader({
  title = "短剧生成",
  showBack = false,
  backPath = "/drama",
  actions,
}: DramaHeaderProps) {
  const router = useRouter();
  const currentProject = useDramaStore((s) => s.currentProject);

  return (
    <div className="flex items-center justify-between px-6 py-4 border-b bg-background">
      <div className="flex items-center gap-4">
        {showBack && (
          <Button
            variant="ghost"
            size="icon"
            onClick={() => router.push(backPath)}
          >
            <ChevronLeft className="h-5 w-5" />
          </Button>
        )}
        <div>
          <h1 className="text-xl font-semibold">
            {currentProject?.name ?? title}
          </h1>
          {currentProject?.updated_at && (
            <p className="text-sm text-muted-foreground">
              最后更新 {new Date(currentProject.updated_at).toLocaleString("zh-CN")}
            </p>
          )}
        </div>
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  );
}
