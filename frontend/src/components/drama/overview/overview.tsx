"use client";

import { useEffect, useState } from "react";
import { Users, FileText, LayoutGrid, Video, Pencil, Check, X } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { dramaProjectApi, dramaAssetsApi, dramaScriptApi, dramaStoryboardApi, dramaVideoApi } from "@/core/drama/api";
import { useDramaStore } from "@/core/drama/store";

interface ProjectStats {
  roleCount: number;
  scriptCount: number;
  storyboardCount: number;
  videoCount: number;
}

const STATS_CONFIG = [
  { label: "角色数量", key: "roleCount" as const, icon: Users, color: "bg-purple-100 text-purple-600" },
  { label: "剧本集数", key: "scriptCount" as const, icon: FileText, color: "bg-blue-100 text-blue-600" },
  { label: "分镜数量", key: "storyboardCount" as const, icon: LayoutGrid, color: "bg-green-100 text-green-600" },
  { label: "视频数量", key: "videoCount" as const, icon: Video, color: "bg-orange-100 text-orange-600" },
];

const PROJECT_TYPES = [
  { label: "基于小说原文", value: "基于小说原文" },
  { label: "基于剧本", value: "基于剧本" },
];

const VIDEO_RATIOS = [
  { label: "16:9", value: "16:9" },
  { label: "9:16", value: "9:16" },
];

export function Overview({ projectId }: { projectId: number }) {
  const currentProject = useDramaStore((s) => s.currentProject);
  const setCurrentProject = useDramaStore((s) => s.setCurrentProject);

  const [stats, setStats] = useState<ProjectStats>({
    roleCount: 0,
    scriptCount: 0,
    storyboardCount: 0,
    videoCount: 0,
  });

  // Intro edit state
  const [introEdit, setIntroEdit] = useState(false);
  const [introEditData, setIntroEditData] = useState("");

  // Global settings edit state
  const [globalSettingEdit, setGlobalSettingEdit] = useState(false);
  const [projectEditData, setProjectEditData] = useState({
    type: "",
    art_style: "",
    video_ratio: "16:9",
    project_type: "",
  });

  useEffect(() => {
    fetchStats();
  }, [projectId]);

  const fetchStats = async () => {
    try {
      // Fetch all stats in parallel
      const [assetsRes, scriptsRes, storyboardsRes, videosRes] = await Promise.all([
        dramaAssetsApi.getAssets(projectId).catch(() => ({ code: 200, data: [] })),
        dramaScriptApi.getScripts(projectId).catch(() => ({ code: 200, data: [] })),
        dramaStoryboardApi.getStoryboards(projectId).catch(() => ({ code: 200, data: [] })),
        dramaVideoApi.getVideos(projectId).catch(() => ({ code: 200, data: [] })),
      ]);

      // Count roles from assets (type = "role")
      const roleCount = assetsRes.code === 200
        ? (assetsRes.data as unknown[]).filter((a: unknown) => (a as { type?: string }).type === "role").length
        : 0;

      setStats({
        roleCount,
        scriptCount: scriptsRes.code === 200 ? (scriptsRes.data as unknown[]).length : 0,
        storyboardCount: storyboardsRes.code === 200 ? (storyboardsRes.data as unknown[]).length : 0,
        videoCount: videosRes.code === 200 ? (videosRes.data as unknown[]).length : 0,
      });
    } catch (error) {
      console.error("获取项目统计失败:", error);
    }
  };

  const handleIntroEdit = () => {
    setIntroEditData(currentProject?.intro ?? "");
    setIntroEdit(true);
  };

  const updateProjectIntro = async () => {
    try {
      const response = await dramaProjectApi.updateProject(projectId, {
        intro: introEditData,
      });
      if (response.code === 200) {
        toast.success("项目简介更新成功");
        // Refresh project data
        const projectRes = await dramaProjectApi.getProject(projectId);
        if (projectRes.code === 200 && projectRes.data?.length > 0) {
          setCurrentProject(projectRes.data[0]);
        }
        setIntroEdit(false);
      }
    } catch (error) {
      console.error("更新失败:", error);
      toast.error("项目简介更新失败");
    }
  };

  const handleGlobalSettingEdit = () => {
    setProjectEditData({
      type: currentProject?.type ?? "",
      art_style: currentProject?.art_style ?? "",
      video_ratio: currentProject?.video_ratio ?? "16:9",
      project_type: (currentProject as unknown as { project_type?: string })?.project_type ?? "",
    });
    setGlobalSettingEdit(true);
  };

  const updateProject = async () => {
    try {
      const response = await dramaProjectApi.updateProject(projectId, {
        type: projectEditData.type,
        art_style: projectEditData.art_style,
        video_ratio: projectEditData.video_ratio,
      });
      if (response.code === 200) {
        toast.success("全局设置更新成功");
        // Refresh project data
        const projectRes = await dramaProjectApi.getProject(projectId);
        if (projectRes.code === 200 && projectRes.data?.length > 0) {
          setCurrentProject(projectRes.data[0]);
        }
        setGlobalSettingEdit(false);
      }
    } catch (error) {
      console.error("更新失败:", error);
      toast.error("全局设置更新失败");
    }
  };

  return (
    <div className="overview-main">
      {/* Header */}
      <div className="mb-8">
        <h2 className="text-xl font-semibold mb-1">项目概览</h2>
        <p className="text-sm text-muted-foreground">查看项目整体进度和统计信息</p>
      </div>

      {/* Stats Grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-6 mb-6">
        {STATS_CONFIG.map((stat) => {
          const Icon = stat.icon;
          return (
            <div
              key={stat.key}
              className="bg-card rounded-xl p-6 border shadow-sm"
            >
              <div className="flex items-center justify-between mb-3">
                <div className={`w-10 h-10 rounded-lg flex items-center justify-center ${stat.color}`}>
                  <Icon className="h-5 w-5" />
                </div>
                <span className="text-3xl font-bold">{stats[stat.key]}</span>
              </div>
              <p className="text-sm text-muted-foreground">{stat.label}</p>
            </div>
          );
        })}
      </div>

      {/* Project Summary */}
      <div className="bg-card rounded-xl p-6 border shadow-sm mb-6">
        <div className="flex items-center justify-between mb-4">
          <h3 className="font-semibold">小说简介</h3>
          {!introEdit && (
            <Button variant="ghost" size="sm" onClick={handleIntroEdit}>
              <Pencil className="h-4 w-4 mr-1" />
              编辑
            </Button>
          )}
        </div>

        {!introEdit ? (
          <p className="text-muted-foreground leading-relaxed">
            {currentProject?.intro || "暂无简介"}
          </p>
        ) : (
          <div>
            <Textarea
              value={introEditData}
              onChange={(e) => setIntroEditData(e.target.value)}
              placeholder="请输入项目简介..."
              className="min-h-[120px] mb-4"
            />
            <div className="flex gap-2">
              <Button variant="outline" size="sm" onClick={() => setIntroEdit(false)}>
                <X className="h-4 w-4 mr-1" />
                取消
              </Button>
              <Button size="sm" onClick={updateProjectIntro}>
                <Check className="h-4 w-4 mr-1" />
                保存
              </Button>
            </div>
          </div>
        )}
      </div>

      {/* Global Settings */}
      <div className="bg-card rounded-xl p-6 border shadow-sm">
        <div className="flex items-center justify-between mb-4">
          <h3 className="font-semibold">全局设置</h3>
          {!globalSettingEdit && (
            <Button variant="ghost" size="sm" onClick={handleGlobalSettingEdit}>
              <Pencil className="h-4 w-4 mr-1" />
              编辑
            </Button>
          )}
        </div>

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-6">
          <div>
            <label className="text-sm text-muted-foreground mb-2 block">项目类型</label>
            {globalSettingEdit ? (
              <Select
                value={projectEditData.project_type}
                onValueChange={(v) => setProjectEditData({ ...projectEditData, project_type: v })}
              >
                <SelectTrigger>
                  <SelectValue placeholder="选择项目类型" />
                </SelectTrigger>
                <SelectContent>
                  {PROJECT_TYPES.map((type) => (
                    <SelectItem key={type.value} value={type.value}>
                      {type.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <p className="font-medium">
                {(currentProject as unknown as { project_type?: string })?.project_type || "无类型"}
              </p>
            )}
          </div>

          <div>
            <label className="text-sm text-muted-foreground mb-2 block">影片比例</label>
            {globalSettingEdit ? (
              <Select
                value={projectEditData.video_ratio}
                onValueChange={(v) => setProjectEditData({ ...projectEditData, video_ratio: v })}
              >
                <SelectTrigger>
                  <SelectValue placeholder="选择影片比例" />
                </SelectTrigger>
                <SelectContent>
                  {VIDEO_RATIOS.map((ratio) => (
                    <SelectItem key={ratio.value} value={ratio.value}>
                      {ratio.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <p className="font-medium">{currentProject?.video_ratio || "16:9"}</p>
            )}
          </div>

          <div>
            <label className="text-sm text-muted-foreground mb-2 block">画风</label>
            {globalSettingEdit ? (
              <Input
                value={projectEditData.art_style}
                onChange={(e) => setProjectEditData({ ...projectEditData, art_style: e.target.value })}
                placeholder="请输入画风"
              />
            ) : (
              <p className="font-medium">{currentProject?.art_style || "动漫"}</p>
            )}
          </div>

          <div>
            <label className="text-sm text-muted-foreground mb-2 block">小说类型</label>
            {globalSettingEdit ? (
              <Input
                value={projectEditData.type}
                onChange={(e) => setProjectEditData({ ...projectEditData, type: e.target.value })}
                placeholder="请输入小说类型"
              />
            ) : (
              <p className="font-medium">{currentProject?.type || "无类型"}</p>
            )}
          </div>
        </div>

        {globalSettingEdit && (
          <div className="flex gap-2 mt-6 pt-4 border-t">
            <Button variant="outline" size="sm" onClick={() => setGlobalSettingEdit(false)}>
              <X className="h-4 w-4 mr-1" />
              取消
            </Button>
            <Button size="sm" onClick={updateProject}>
              <Check className="h-4 w-4 mr-1" />
              保存
            </Button>
          </div>
        )}
      </div>
    </div>
  );
}
