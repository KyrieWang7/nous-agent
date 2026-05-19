"use client";

import { useState, useEffect, useCallback } from "react";
import { Pencil, Save, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { dramaProjectApi } from "@/core/drama/api";
import { useDramaStore } from "@/core/drama/store";
import { cn } from "@/lib/utils";

interface ProjectStats {
  roleCount: number;
  scriptCount: number;
  storyboardCount: number;
  videoCount: number;
}

const ART_STYLES = [
  "2D动漫风格",
  "3D动画风格",
  "写实风格",
  "水墨风格",
  "扁平化风格",
];

const VIDEO_RATIOS = [
  { label: "16:9 (横屏)", value: "16:9" },
  { label: "9:16 (竖屏)", value: "9:16" },
  { label: "1:1 (方形)", value: "1:1" },
  { label: "4:3 (传统)", value: "4:3" },
];

const PROJECT_TYPES = [
  { label: "基于小说原文", value: "基于小说原文" },
  { label: "基于剧本", value: "基于剧本" },
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
  const [loading, setLoading] = useState(false);

  // Intro edit state
  const [editingIntro, setEditingIntro] = useState(false);
  const [introText, setIntroText] = useState(currentProject?.intro || "");

  // Global settings edit state
  const [editingSettings, setEditingSettings] = useState(false);
  const [settingsData, setSettingsData] = useState({
    type: currentProject?.type || "",
    artStyle: currentProject?.art_style || "2D动漫风格",
    videoRatio: currentProject?.video_ratio || "16:9",
    projectType: currentProject?.projectType || "",
  });

  const fetchStats = useCallback(async () => {
    try {
      const response = await dramaProjectApi.getProjectStats(projectId);
      if (response.code === 200) {
        setStats(response.data || stats);
      }
    } catch (error) {
      console.error("获取项目统计失败:", error);
    }
  }, [projectId]);

  useEffect(() => {
    fetchStats();
  }, [fetchStats]);

  useEffect(() => {
    if (currentProject) {
      setIntroText(currentProject.intro || "");
      setSettingsData({
        type: currentProject.type || "",
        artStyle: currentProject.art_style || "2D动漫风格",
        videoRatio: currentProject.video_ratio || "16:9",
        projectType: currentProject.projectType || "",
      });
    }
  }, [currentProject]);

  const handleSaveIntro = async () => {
    setLoading(true);
    try {
      const response = await dramaProjectApi.updateProject(projectId, { intro: introText });
      if (response.code === 200) {
        if (currentProject) {
          setCurrentProject({ ...currentProject, intro: introText });
        }
        setEditingIntro(false);
      }
    } catch (error) {
      console.error("保存简介失败:", error);
    } finally {
      setLoading(false);
    }
  };

  const handleSaveSettings = async () => {
    setLoading(true);
    try {
      const response = await dramaProjectApi.updateProject(projectId, {
        type: settingsData.type,
        art_style: settingsData.artStyle,
        video_ratio: settingsData.videoRatio,
      });
      if (response.code === 200) {
        if (currentProject) {
          setCurrentProject({
            ...currentProject,
            type: settingsData.type,
            art_style: settingsData.artStyle,
            video_ratio: settingsData.videoRatio,
          });
        }
        setEditingSettings(false);
      }
    } catch (error) {
      console.error("保存设置失败:", error);
    } finally {
      setLoading(false);
    }
  };

  if (!currentProject) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-muted-foreground">项目信息加载中...</div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Stats Cards */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-4">
        <Card>
          <CardContent className="pt-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-muted-foreground">角色数量</p>
                <p className="text-2xl font-bold">{stats.roleCount}</p>
              </div>
              <div className="w-10 h-10 rounded-lg bg-purple-100 dark:bg-purple-900 flex items-center justify-center text-purple-600 dark:text-purple-300">
                👤
              </div>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-muted-foreground">剧本集数</p>
                <p className="text-2xl font-bold">{stats.scriptCount}</p>
              </div>
              <div className="w-10 h-10 rounded-lg bg-blue-100 dark:bg-blue-900 flex items-center justify-center text-blue-600 dark:text-blue-300">
                📄
              </div>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-muted-foreground">分镜数量</p>
                <p className="text-2xl font-bold">{stats.storyboardCount}</p>
              </div>
              <div className="w-10 h-10 rounded-lg bg-green-100 dark:bg-green-900 flex items-center justify-center text-green-600 dark:text-green-300">
                🎬
              </div>
            </div>
          </CardContent>
        </Card>
        <Card>
          <CardContent className="pt-6">
            <div className="flex items-center justify-between">
              <div>
                <p className="text-sm text-muted-foreground">视频数量</p>
                <p className="text-2xl font-bold">{stats.videoCount}</p>
              </div>
              <div className="w-10 h-10 rounded-lg bg-orange-100 dark:bg-orange-900 flex items-center justify-center text-orange-600 dark:text-orange-300">
                🎥
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      {/* Project Summary */}
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle>小说简介</CardTitle>
            {!editingIntro ? (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setEditingIntro(true)}
              >
                <Pencil className="h-4 w-4 mr-2" />
                编辑
              </Button>
            ) : (
              <div className="flex gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setEditingIntro(false);
                    setIntroText(currentProject.intro || "");
                  }}
                >
                  <X className="h-4 w-4 mr-2" />
                  取消
                </Button>
                <Button size="sm" onClick={handleSaveIntro} disabled={loading}>
                  <Save className="h-4 w-4 mr-2" />
                  保存
                </Button>
              </div>
            )}
          </div>
        </CardHeader>
        <CardContent>
          {editingIntro ? (
            <Textarea
              value={introText}
              onChange={(e) => setIntroText(e.target.value)}
              rows={4}
              placeholder="输入小说简介..."
            />
          ) : (
            <p className="text-muted-foreground whitespace-pre-wrap">
              {currentProject.intro || "暂无简介"}
            </p>
          )}
        </CardContent>
      </Card>

      {/* Global Settings */}
      <Card>
        <CardHeader>
          <div className="flex items-center justify-between">
            <CardTitle>全局设置</CardTitle>
            {!editingSettings ? (
              <Button
                variant="ghost"
                size="sm"
                onClick={() => setEditingSettings(true)}
              >
                <Pencil className="h-4 w-4 mr-2" />
                编辑
              </Button>
            ) : (
              <div className="flex gap-2">
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => {
                    setEditingSettings(false);
                    setSettingsData({
                      type: currentProject.type || "",
                      artStyle: currentProject.art_style || "2D动漫风格",
                      videoRatio: currentProject.video_ratio || "16:9",
                      projectType: currentProject.projectType || "",
                    });
                  }}
                >
                  <X className="h-4 w-4 mr-2" />
                  取消
                </Button>
                <Button size="sm" onClick={handleSaveSettings} disabled={loading}>
                  <Save className="h-4 w-4 mr-2" />
                  保存
                </Button>
              </div>
            )}
          </div>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label>项目类型</Label>
              {editingSettings ? (
                <Select
                  value={settingsData.projectType}
                  onValueChange={(v) =>
                    setSettingsData({ ...settingsData, projectType: v })
                  }
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
                <p className="font-medium">{currentProject.projectType || "无类型"}</p>
              )}
            </div>

            <div className="space-y-2">
              <Label>影片比例</Label>
              {editingSettings ? (
                <Select
                  value={settingsData.videoRatio}
                  onValueChange={(v) =>
                    setSettingsData({ ...settingsData, videoRatio: v })
                  }
                >
                  <SelectTrigger>
                    <SelectValue />
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
                <p className="font-medium">{currentProject.video_ratio || "16:9"}</p>
              )}
            </div>

            <div className="space-y-2">
              <Label>画风</Label>
              {editingSettings ? (
                <Select
                  value={settingsData.artStyle}
                  onValueChange={(v) =>
                    setSettingsData({ ...settingsData, artStyle: v })
                  }
                >
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {ART_STYLES.map((style) => (
                      <SelectItem key={style} value={style}>
                        {style}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              ) : (
                <p className="font-medium">{currentProject.art_style || "未设置"}</p>
              )}
            </div>

            <div className="space-y-2">
              <Label>小说类型</Label>
              {editingSettings ? (
                <Input
                  value={settingsData.type}
                  onChange={(e) =>
                    setSettingsData({ ...settingsData, type: e.target.value })
                  }
                  placeholder="如：玄幻、科幻"
                />
              ) : (
                <p className="font-medium">{currentProject.type || "未设置"}</p>
              )}
            </div>
          </div>
        </CardContent>
      </Card>

      {/* Project Info */}
      <Card>
        <CardHeader>
          <CardTitle>项目信息</CardTitle>
        </CardHeader>
        <CardContent>
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-1">
              <Label className="text-muted-foreground text-xs">项目名称</Label>
              <p className="font-medium">{currentProject.name}</p>
            </div>
            <div className="space-y-1">
              <Label className="text-muted-foreground text-xs">创建时间</Label>
              <p className="font-medium">
                {new Date(currentProject.created_at).toLocaleString("zh-CN")}
              </p>
            </div>
            <div className="space-y-1">
              <Label className="text-muted-foreground text-xs">最后更新</Label>
              <p className="font-medium">
                {currentProject.updated_at
                  ? new Date(currentProject.updated_at).toLocaleString("zh-CN")
                  : "未更新"}
              </p>
            </div>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
