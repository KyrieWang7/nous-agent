"use client";

import { useRouter } from "next/navigation";
import { FolderOpen, Trash } from "lucide-react";

import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import type { DramaProject } from "@/core/drama/types";

interface DramaProjectCardProps {
  project: DramaProject;
  onDelete?: (id: number) => void;
}

export function DramaProjectCard({ project, onDelete }: DramaProjectCardProps) {
  const router = useRouter();

  const handleDelete = (e: React.MouseEvent) => {
    e.stopPropagation();
    if (window.confirm("确定要删除这个项目吗？")) {
      onDelete?.(project.id);
    }
  };

  return (
    <Card
      className="cursor-pointer hover:shadow-md transition-shadow"
      onClick={() => router.push(`/drama/projects/${project.id}`)}
    >
      <CardContent className="p-4">
        <div className="flex items-start justify-between">
          <div className="flex items-center gap-3">
            <div className="flex items-center justify-center w-12 h-12 rounded-xl bg-gradient-to-br from-primary to-primary/60">
              <FolderOpen className="h-6 w-6 text-primary-foreground" />
            </div>
            <div>
              <h3 className="font-semibold">{project.name}</h3>
              <p className="text-sm text-muted-foreground">
                类型：{project.type ?? "未设置"}
              </p>
            </div>
          </div>
          {onDelete && (
            <Button
              variant="ghost"
              size="icon"
              className="text-destructive hover:text-destructive hover:bg-destructive/10"
              onClick={handleDelete}
            >
              <Trash className="h-4 w-4" />
            </Button>
          )}
        </div>

        {project.intro && (
          <p className="mt-3 text-sm text-muted-foreground line-clamp-2">
            {project.intro}
          </p>
        )}

        <div className="mt-4 pt-3 border-t text-xs text-muted-foreground">
          创建于 {new Date(project.created_at).toLocaleString("zh-CN")}
        </div>
      </CardContent>
    </Card>
  );
}
