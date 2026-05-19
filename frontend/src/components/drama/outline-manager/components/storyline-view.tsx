"use client";

import { useEffect, useState } from "react";
import { Pencil, Save, X, BookOpen } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { dramaOutlineApi } from "@/core/drama/api";

export function StorylineView({ projectId }: { projectId: number }) {
  const [storyLine, setStoryLine] = useState("");
  const [isEditing, setIsEditing] = useState(false);
  const [editData, setEditData] = useState("");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    fetchStoryline();
  }, [projectId]);

  const fetchStoryline = async () => {
    try {
      setLoading(true);
      const response = await dramaOutlineApi.getStoryline(projectId);
      if (response.code === 200 && response.data) {
        setStoryLine(response.data.content || "");
      }
    } catch (error) {
      console.error("获取故事线失败:", error);
    } finally {
      setLoading(false);
    }
  };

  const saveStoryLine = async () => {
    try {
      const response = await dramaOutlineApi.updateStoryline(projectId, {
        content: editData,
      });
      if (response.code === 200) {
        toast.success("保存成功");
        setStoryLine(editData);
        setIsEditing(false);
      }
    } catch (error) {
      console.error("保存失败:", error);
      toast.error("保存失败");
    }
  };

  const startEditing = () => {
    setEditData(storyLine);
    setIsEditing(true);
  };

  const cancelEditing = () => {
    setEditData("");
    setIsEditing(false);
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center h-full">
        <div className="text-muted-foreground">加载中...</div>
      </div>
    );
  }

  return (
    <div className="p-6">
      {/* Header */}
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-xl font-semibold mb-1">故事线管理</h2>
          <p className="text-sm text-muted-foreground">根据上传的小说原文生成大纲和故事线</p>
        </div>
        <Button onClick={isEditing ? cancelEditing : startEditing} variant={isEditing ? "outline" : "default"}>
          {isEditing ? (
            <>
              <X className="h-4 w-4 mr-2" />
              取消
            </>
          ) : (
            <>
              <Pencil className="h-4 w-4 mr-2" />
              编辑故事线
            </>
          )}
        </Button>
      </div>

      {/* Content Card */}
      <div className="bg-card rounded-xl border shadow-sm overflow-hidden">
        {isEditing ? (
          /* Edit Mode */
          <div className="p-6">
            <div className="flex items-center gap-2 mb-4 text-sm text-muted-foreground">
              <Pencil className="h-4 w-4" />
              <span>编辑故事线 - 支持多行输入，描述完整的故事脉络</span>
            </div>
            <Textarea
              value={editData}
              onChange={(e) => setEditData(e.target.value)}
              placeholder="请输入故事线，包括主要情节、角色发展、冲突转折等..."
              className="min-h-[300px] font-mono text-sm mb-4"
            />
            <div className="flex justify-end">
              <Button onClick={saveStoryLine}>
                <Save className="h-4 w-4 mr-2" />
                保存
              </Button>
            </div>
          </div>
        ) : storyLine ? (
          /* Preview Mode */
          <div className="p-6">
            <div className="flex items-center gap-2 mb-4">
              <BookOpen className="h-5 w-5 text-primary" />
              <span className="font-medium">故事线内容</span>
            </div>
            <div className="text-muted-foreground leading-relaxed whitespace-pre-wrap">
              {storyLine}
            </div>
          </div>
        ) : (
          /* Empty State */
          <div className="p-12 text-center">
            <div className="text-5xl mb-4">📝</div>
            <h3 className="text-lg font-medium mb-2">暂无故事线</h3>
            <p className="text-muted-foreground mb-6">点击上方&quot;编辑故事线&quot;开始创作</p>
            <Button onClick={startEditing}>开始编辑</Button>
          </div>
        )}
      </div>
    </div>
  );
}
