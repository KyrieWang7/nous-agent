"use client";

import { useEffect, useState } from "react";
import { Plus } from "lucide-react";

import { CreateProjectDialog } from "@/components/drama/create-project-dialog";
import { DramaEmpty } from "@/components/drama/drama-empty";
import { DramaProjectCard } from "@/components/drama/drama-project-card";
import { Button } from "@/components/ui/button";
import { dramaProjectApi } from "@/core/drama/api";
import type { DramaProject } from "@/core/drama/types";

export default function DramaProjectsPage() {
  const [projects, setProjects] = useState<DramaProject[]>([]);
  const [loading, setLoading] = useState(true);
  const [createLoading, setCreateLoading] = useState(false);
  const [createDialogOpen, setCreateDialogOpen] = useState(false);

  const fetchProjects = async () => {
    try {
      const response = await dramaProjectApi.getProjects();
      if (response.code === 200) {
        setProjects(response.data ?? []);
      }
    } catch (error) {
      console.error("获取项目列表失败:", error);
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    void fetchProjects();
  }, []);

  const handleCreate = async (data: {
    name: string;
    intro: string;
    type: string;
    art_style: string;
    video_ratio: string;
  }) => {
    setCreateLoading(true);
    try {
      const response = await dramaProjectApi.createProject(data);
      if (response.code === 200) {
        await fetchProjects();
        setCreateDialogOpen(false);
      }
    } catch (error) {
      console.error("创建项目失败:", error);
    } finally {
      setCreateLoading(false);
    }
  };

  const handleDelete = async (id: number) => {
    try {
      const response = await dramaProjectApi.deleteProject(id);
      if (response.code === 200) {
        setProjects(projects.filter((p) => p.id !== id));
      }
    } catch (error) {
      console.error("删除项目失败:", error);
    }
  };

  if (loading) {
    return (
      <div className="max-w-5xl mx-auto p-8">
        <div className="flex items-center justify-center py-16">
          <div className="text-muted-foreground">加载中...</div>
        </div>
      </div>
    );
  }

  return (
    <div className="max-w-5xl mx-auto p-8">
      <div className="flex items-center justify-between mb-8">
        <h1 className="text-2xl font-bold">短剧项目</h1>
        <Button onClick={() => setCreateDialogOpen(true)}>
          <Plus className="mr-2 h-4 w-4" />
          新建项目
        </Button>
      </div>

      {projects.length === 0 ? (
        <DramaEmpty
          title="暂无短剧项目"
          description="创建一个新的短剧项目，开始AI创作之旅"
          actionLabel="新建项目"
          onAction={() => setCreateDialogOpen(true)}
        />
      ) : (
        <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
          {projects.map((project) => (
            <DramaProjectCard
              key={project.id}
              project={project}
              onDelete={handleDelete}
            />
          ))}
        </div>
      )}

      <CreateProjectDialog
        open={createDialogOpen}
        onOpenChange={setCreateDialogOpen}
        onCreate={handleCreate}
        loading={createLoading}
      />
    </div>
  );
}
