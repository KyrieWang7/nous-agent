"use client";

import { useEffect, useState } from "react";
import { useParams } from "next/navigation";

import { AssetsManager } from "@/components/drama/assets-manager";
import { DramaHeader } from "@/components/drama/drama-header";
import { DramaNav } from "@/components/drama/drama-nav";
import { OriginalText } from "@/components/drama/original-text";
import { OutlineManager } from "@/components/drama/outline-manager";
import { Overview } from "@/components/drama/overview";
import { ScriptManager } from "@/components/drama/script-manager";
import { dramaProjectApi } from "@/core/drama/api";
import { useDramaStore } from "@/core/drama/store";
import type { DramaProject } from "@/core/drama/types";

export default function DramaProjectDetailPage() {
  const params = useParams();
  const projectId = Number(params.id);

  const [project, setProject] = useState<DramaProject | null>(null);
  const [loading, setLoading] = useState(true);

  const currentSubView = useDramaStore((s) => s.currentSubView);
  const setCurrentProject = useDramaStore((s) => s.setCurrentProject);

  useEffect(() => {
    const fetchProject = async () => {
      try {
        const response = await dramaProjectApi.getProject(projectId);
        if (response.code === 200 && response.data?.length > 0) {
          const projectData = response.data[0];
          setProject(projectData);
          setCurrentProject(projectData);
        }
      } catch (error) {
        console.error("获取项目详情失败:", error);
      } finally {
        setLoading(false);
      }
    };

    if (projectId) {
      void fetchProject();
    }

    return () => {
      setCurrentProject(null);
    };
  }, [projectId, setCurrentProject]);

  if (loading) {
    return (
      <div className="flex flex-col h-full">
        <DramaHeader title="加载中..." />
        <div className="flex-1 flex items-center justify-center">
          <div className="text-muted-foreground">加载中...</div>
        </div>
      </div>
    );
  }

  if (!project) {
    return (
      <div className="flex flex-col h-full">
        <DramaHeader title="项目不存在" showBack backPath="/drama/projects" />
        <div className="flex-1 flex items-center justify-center">
          <div className="text-muted-foreground">项目不存在</div>
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col h-full">
      <DramaHeader
        title={project.name}
        showBack
        backPath="/drama/projects"
      />

      <div className="px-6 pt-4">
        <DramaNav />
      </div>

      <div className="flex-1 overflow-auto px-6 py-4">
        <SubViewContent view={currentSubView} projectId={projectId} />
      </div>
    </div>
  );
}

function SubViewContent({
  view,
  projectId,
}: {
  view: string;
  projectId: number;
}) {
  switch (view) {
    case "overview":
      return <Overview projectId={projectId} />;
    case "originalText":
      return <OriginalText projectId={projectId} />;
    case "outline":
      return <OutlineManager projectId={projectId} />;
    case "script":
      return <ScriptManager projectId={projectId} />;
    case "assets":
      return <AssetsManager projectId={projectId} />;
    default:
      return <div>未知视图</div>;
  }
}
