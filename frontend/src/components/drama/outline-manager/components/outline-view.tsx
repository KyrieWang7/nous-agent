"use client";

import { useEffect, useState } from "react";
import { Plus, Pencil, Trash2, BookOpen, Users, Clapperboard, Sparkles } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  dramaOutlineApi,
  dramaNovelApi,
} from "@/core/drama/api";

interface ObjectItem {
  name: string;
  description: string;
}

interface OutlineItem {
  id: number;
  episodeIndex: number;
  title: string;
  chapterRange: number[];
  scenes: ObjectItem[];
  characters: ObjectItem[];
  props: ObjectItem[];
  coreConflict: string;
  openingHook: string;
  outline: string;
  keyEvents: string[];
  endingHook: string;
  classicQuotes: string[];
}

interface NovelChapter {
  id: number;
  chapter: string;
  chapter_index: number;
}

const defaultOutline = (): OutlineItem => ({
  id: 0,
  episodeIndex: 0,
  title: "",
  chapterRange: [],
  scenes: [],
  characters: [],
  props: [],
  coreConflict: "",
  openingHook: "",
  outline: "",
  keyEvents: [],
  endingHook: "",
  classicQuotes: [],
});

export function OutlineView({ projectId }: { projectId: number }) {
  const [outlines, setOutlines] = useState<OutlineItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [dialogOpen, setDialogOpen] = useState(false);
  const [isAddMode, setIsAddMode] = useState(false);
  const [editingOutline, setEditingOutline] = useState<OutlineItem>(defaultOutline());
  const [chapters, setChapters] = useState<NovelChapter[]>([]);

  useEffect(() => {
    fetchOutlines();
    fetchChapters();
  }, [projectId]);

  const fetchOutlines = async () => {
    try {
      setLoading(true);
      const response = await dramaOutlineApi.getOutlines(projectId);
      if (response.code === 200 && response.data) {
        const parsed = (response.data as { id: number; episode: number; data: string }[]).map((item) => {
          try {
            const parsedData = JSON.parse(item.data);
            return {
              ...defaultOutline(),
              ...parsedData,
              id: item.id,
              episodeIndex: parsedData.episodeIndex || item.episode,
            };
          } catch {
            return {
              ...defaultOutline(),
              id: item.id,
              episodeIndex: item.episode,
            };
          }
        });
        setOutlines(parsed);
      }
    } catch (error) {
      console.error("获取大纲失败:", error);
    } finally {
      setLoading(false);
    }
  };

  const fetchChapters = async () => {
    try {
      const response = await dramaNovelApi.getNovels(projectId);
      if (response.code === 200 && response.data) {
        setChapters(response.data as unknown as NovelChapter[]);
      }
    } catch (error) {
      console.error("获取章节列表失败:", error);
    }
  };

  const handleAdd = () => {
    setIsAddMode(true);
    setEditingOutline({ ...defaultOutline(), episodeIndex: outlines.length + 1 });
    setDialogOpen(true);
  };

  const handleEdit = (index: number) => {
    const outline = outlines[index];
    if (!outline) return;
    setIsAddMode(false);
    setEditingOutline({
      id: outline.id,
      episodeIndex: outline.episodeIndex,
      title: outline.title,
      chapterRange: outline.chapterRange,
      scenes: outline.scenes,
      characters: outline.characters,
      props: outline.props,
      coreConflict: outline.coreConflict,
      openingHook: outline.openingHook,
      outline: outline.outline,
      keyEvents: outline.keyEvents,
      endingHook: outline.endingHook,
      classicQuotes: outline.classicQuotes,
    });
    setDialogOpen(true);
  };

  const handleDelete = async (outline: OutlineItem) => {
    if (!confirm("删除大纲将会删除该大纲下的剧本和独有资产，确定要删除吗？")) return;

    try {
      const response = await dramaOutlineApi.deleteOutline(outline.id, projectId);
      if (response.code === 200) {
        toast.success("删除成功");
        fetchOutlines();
      }
    } catch (error) {
      console.error("删除失败:", error);
      toast.error("删除失败");
    }
  };

  const saveOutline = async () => {
    try {
      const data = JSON.stringify(editingOutline);
      if (isAddMode) {
        const response = await dramaOutlineApi.addOutline(projectId, {
          episode: editingOutline.episodeIndex,
          data: editingOutline as unknown as Record<string, unknown>,
        });
        if (response.code === 200) {
          toast.success("新增成功");
        }
      } else {
        const response = await dramaOutlineApi.updateOutline(projectId, editingOutline.id, {
          episode: editingOutline.episodeIndex,
          data: editingOutline as unknown as Record<string, unknown>,
        });
        if (response.code === 200) {
          toast.success("保存成功");
        }
      }
      setDialogOpen(false);
      fetchOutlines();
    } catch (error) {
      console.error("保存失败:", error);
      toast.error("保存失败");
    }
  };

  const formatChapterIndexes = (indexes?: number[]): string => {
    if (!indexes?.length) return "—";
    return indexes
      .sort((a, b) => a - b)
      .map((i) => `第${i}章`)
      .join("、");
  };

  const formatObjectArray = (arr?: ObjectItem[]): string => {
    return arr?.map((i) => i.name).filter(Boolean).join("、") || "—";
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
          <h2 className="text-xl font-semibold mb-1">大纲管理</h2>
          <p className="text-sm text-muted-foreground">每一集的详细内容</p>
        </div>
        <Button onClick={handleAdd}>
          <Plus className="h-4 w-4 mr-2" />
          新增大纲
        </Button>
      </div>

      {/* Outline List */}
      {outlines.length === 0 ? (
        <div className="text-center py-12 bg-card rounded-xl border">
          <div className="text-5xl mb-4">📋</div>
          <h3 className="text-lg font-medium mb-2">暂无大纲数据</h3>
          <p className="text-muted-foreground mb-6">创建第一个大纲开始规划您的剧本</p>
          <Button onClick={handleAdd}>创建第一个大纲</Button>
        </div>
      ) : (
        <div className="space-y-4">
          {outlines.map((outline, index) => (
            <div key={outline.id} className="bg-card rounded-xl border shadow-sm overflow-hidden">
              {/* Card Header */}
              <div className="flex items-center gap-3 px-4 py-3 bg-primary/5 border-b">
                <span className="px-3 py-1 bg-primary text-primary-foreground text-sm font-medium rounded-full">
                  第 {outline.episodeIndex} 集
                </span>
                <span className="font-medium flex-1">{outline.title || "未命名"}</span>
                <div className="flex gap-1">
                  <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => handleEdit(index)}>
                    <Pencil className="h-4 w-4" />
                  </Button>
                  <Button variant="ghost" size="icon" className="h-8 w-8 text-destructive hover:text-destructive" onClick={() => handleDelete(outline)}>
                    <Trash2 className="h-4 w-4" />
                  </Button>
                </div>
              </div>

              {/* Card Body */}
              <div className="p-4">
                <div className="grid grid-cols-2 gap-3">
                  {/* Chapter Range */}
                  <div className="flex items-center gap-2 p-3 bg-primary/5 rounded-lg">
                    <BookOpen className="h-4 w-4 text-primary" />
                    <span className="text-sm text-muted-foreground">章节范围：</span>
                    <span className="text-sm font-medium">{formatChapterIndexes(outline.chapterRange)}</span>
                  </div>

                  {/* Core Conflict */}
                  <div className="flex items-center gap-2 p-3 bg-muted rounded-lg">
                    <Sparkles className="h-4 w-4 text-muted-foreground" />
                    <span className="text-sm text-muted-foreground">核心冲突：</span>
                    <span className="text-sm font-medium">{outline.coreConflict || "—"}</span>
                  </div>

                  {/* Characters */}
                  <div className="flex items-center gap-2 p-3 bg-muted rounded-lg">
                    <Users className="h-4 w-4 text-muted-foreground" />
                    <span className="text-sm text-muted-foreground">角色：</span>
                    <span className="text-sm font-medium">{formatObjectArray(outline.characters)}</span>
                  </div>

                  {/* Opening Hook */}
                  <div className="flex items-center gap-2 p-3 bg-muted rounded-lg">
                    <Clapperboard className="h-4 w-4 text-muted-foreground" />
                    <span className="text-sm text-muted-foreground">黄金3秒：</span>
                    <span className="text-sm font-medium">{outline.openingHook || "—"}</span>
                  </div>
                </div>

                {/* Outline Text */}
                {outline.outline && (
                  <div className="mt-4 p-3 bg-muted rounded-lg">
                    <div className="text-xs text-muted-foreground mb-1">剧情主干</div>
                    <p className="text-sm text-muted-foreground line-clamp-3">{outline.outline}</p>
                  </div>
                )}

                {/* Tags */}
                {(outline.keyEvents?.length > 0 || outline.classicQuotes?.length > 0) && (
                  <div className="mt-4 flex flex-wrap gap-2">
                    {outline.keyEvents?.slice(0, 3).map((event, i) => (
                      <span key={`event-${i}`} className="px-2 py-1 bg-primary/10 text-primary text-xs rounded-md font-medium">
                        {event}
                      </span>
                    ))}
                    {outline.classicQuotes?.slice(0, 2).map((quote, i) => (
                      <span key={`quote-${i}`} className="px-2 py-1 bg-purple-100 text-purple-700 text-xs rounded-md font-medium">
                        {quote}
                      </span>
                    ))}
                  </div>
                )}
              </div>
            </div>
          ))}
        </div>
      )}

      {/* Edit Dialog */}
      <Dialog open={dialogOpen} onOpenChange={setDialogOpen}>
        <DialogContent className="max-w-2xl max-h-[85vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>{isAddMode ? "新增大纲" : "编辑大纲"}</DialogTitle>
          </DialogHeader>

          <div className="space-y-6">
            {/* Basic Info */}
            <div className="space-y-4">
              <h4 className="font-medium">基础信息</h4>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="text-sm text-muted-foreground mb-2 block">集数</label>
                  <Input
                    type="number"
                    min={1}
                    value={editingOutline.episodeIndex}
                    onChange={(e) => setEditingOutline({ ...editingOutline, episodeIndex: parseInt(e.target.value) || 1 })}
                  />
                </div>
                <div>
                  <label className="text-sm text-muted-foreground mb-2 block">标题</label>
                  <Input
                    value={editingOutline.title}
                    onChange={(e) => setEditingOutline({ ...editingOutline, title: e.target.value })}
                    placeholder="请输入标题"
                  />
                </div>
              </div>
            </div>

            {/* Plot Design */}
            <div className="space-y-4">
              <h4 className="font-medium">剧情设计</h4>
              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="text-sm text-muted-foreground mb-2 block">黄金3秒</label>
                  <Input
                    value={editingOutline.openingHook}
                    onChange={(e) => setEditingOutline({ ...editingOutline, openingHook: e.target.value })}
                    placeholder="开头吸引观众的亮点"
                  />
                </div>
                <div>
                  <label className="text-sm text-muted-foreground mb-2 block">结尾悬念</label>
                  <Input
                    value={editingOutline.endingHook}
                    onChange={(e) => setEditingOutline({ ...editingOutline, endingHook: e.target.value })}
                    placeholder="结尾留下的悬念"
                  />
                </div>
              </div>
              <div>
                <label className="text-sm text-muted-foreground mb-2 block">核心冲突</label>
                <Input
                  value={editingOutline.coreConflict}
                  onChange={(e) => setEditingOutline({ ...editingOutline, coreConflict: e.target.value })}
                  placeholder="本集的核心矛盾点"
                />
              </div>
              <div>
                <label className="text-sm text-muted-foreground mb-2 block">剧情主干</label>
                <Textarea
                  value={editingOutline.outline}
                  onChange={(e) => setEditingOutline({ ...editingOutline, outline: e.target.value })}
                  placeholder="详细描述本集剧情走向"
                  className="min-h-[100px]"
                />
              </div>
            </div>

            {/* Actions */}
            <div className="flex justify-end gap-2">
              <Button variant="outline" onClick={() => setDialogOpen(false)}>
                取消
              </Button>
              <Button onClick={saveOutline}>
                保存
              </Button>
            </div>
          </div>
        </DialogContent>
      </Dialog>
    </div>
  );
}
