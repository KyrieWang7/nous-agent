"use client";

import { useState, useCallback } from "react";
import { Plus, Pencil, Trash2, FileText } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { dramaProjectApi } from "@/core/drama/api";

interface Chapter {
  id: number;
  index: number;
  reel: string;
  chapter: string;
  chapterData: string;
  created_at?: string;
}

interface AddChapterDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdd: (chapters: Chapter[]) => void;
}

function AddChapterDialog({ open, onOpenChange, onAdd }: AddChapterDialogProps) {
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [loading, setLoading] = useState(false);

  const handleAddRow = () => {
    setChapters([
      ...chapters,
      {
        id: Date.now(),
        index: chapters.length + 1,
        reel: "",
        chapter: "",
        chapterData: "",
      },
    ]);
  };

  const handleUpdateChapter = (id: number, updates: Partial<Chapter>) => {
    setChapters(
      chapters.map((c) => (c.id === id ? { ...c, ...updates } : c))
    );
  };

  const handleRemoveChapter = (id: number) => {
    setChapters(chapters.filter((c) => c.id !== id));
  };

  const handleSubmit = async () => {
    setLoading(true);
    try {
      onAdd(chapters);
      setChapters([]);
      onOpenChange(false);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl max-h-[80vh] overflow-auto">
        <DialogHeader>
          <DialogTitle>新增章节</DialogTitle>
          <DialogDescription>添加小说章节内容</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-4">
          <Button onClick={handleAddRow} variant="outline" size="sm">
            <Plus className="h-4 w-4 mr-2" />
            添加章节
          </Button>
          {chapters.map((ch, idx) => (
            <div key={ch.id} className="border rounded-lg p-4 space-y-3">
              <div className="flex items-center justify-between">
                <span className="text-sm font-medium">章节 {idx + 1}</span>
                <Button
                  variant="ghost"
                  size="sm"
                  onClick={() => handleRemoveChapter(ch.id)}
                  className="text-destructive hover:text-destructive"
                >
                  <Trash2 className="h-4 w-4" />
                </Button>
              </div>
              <div className="grid grid-cols-4 gap-3">
                <div className="space-y-1">
                  <Label htmlFor={`index-${ch.id}`}>章</Label>
                  <Input
                    id={`index-${ch.id}`}
                    type="number"
                    value={ch.index}
                    onChange={(e) =>
                      handleUpdateChapter(ch.id, { index: parseInt(e.target.value) || 0 })
                    }
                  />
                </div>
                <div className="space-y-1 col-span-3">
                  <Label htmlFor={`reel-${ch.id}`}>卷</Label>
                  <Input
                    id={`reel-${ch.id}`}
                    value={ch.reel}
                    onChange={(e) => handleUpdateChapter(ch.id, { reel: e.target.value })}
                    placeholder="可选"
                  />
                </div>
                <div className="space-y-1 col-span-4">
                  <Label htmlFor={`chapter-${ch.id}`}>章节名称</Label>
                  <Input
                    id={`chapter-${ch.id}`}
                    value={ch.chapter}
                    onChange={(e) => handleUpdateChapter(ch.id, { chapter: e.target.value })}
                    placeholder="章节标题"
                  />
                </div>
                <div className="space-y-1 col-span-4">
                  <Label htmlFor={`data-${ch.id}`}>章节内容</Label>
                  <Textarea
                    id={`data-${ch.id}`}
                    value={ch.chapterData}
                    onChange={(e) =>
                      handleUpdateChapter(ch.id, { chapterData: e.target.value })
                    }
                    rows={4}
                    placeholder="输入章节内容..."
                  />
                </div>
              </div>
            </div>
          ))}
          {chapters.length === 0 && (
            <div className="text-center py-8 text-muted-foreground">
              点击上方按钮添加章节
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button onClick={handleSubmit} disabled={loading || chapters.length === 0}>
            {loading ? "添加中..." : "添加"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

interface EditChapterDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  chapter: Chapter | null;
  onUpdate: (chapter: Chapter) => void;
}

function EditChapterDialog({
  open,
  onOpenChange,
  chapter,
  onUpdate,
}: EditChapterDialogProps) {
  const [formData, setFormData] = useState<Chapter>(chapter || {
    id: 0,
    index: 0,
    reel: "",
    chapter: "",
    chapterData: "",
  });
  const [loading, setLoading] = useState(false);

  useState(() => {
    if (chapter) setFormData(chapter);
  });

  const handleSubmit = async () => {
    setLoading(true);
    try {
      onUpdate(formData);
      onOpenChange(false);
    } finally {
      setLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>编辑章节</DialogTitle>
          <DialogDescription>修改章节内容</DialogDescription>
        </DialogHeader>
        <div className="space-y-4 py-4">
          <div className="grid grid-cols-4 gap-3">
            <div className="space-y-1">
              <Label htmlFor="edit-index">章</Label>
              <Input
                id="edit-index"
                type="number"
                value={formData.index}
                onChange={(e) =>
                  setFormData({ ...formData, index: parseInt(e.target.value) || 0 })
                }
              />
            </div>
            <div className="space-y-1 col-span-3">
              <Label htmlFor="edit-reel">卷</Label>
              <Input
                id="edit-reel"
                value={formData.reel}
                onChange={(e) => setFormData({ ...formData, reel: e.target.value })}
              />
            </div>
            <div className="space-y-1 col-span-4">
              <Label htmlFor="edit-chapter">章节名称</Label>
              <Input
                id="edit-chapter"
                value={formData.chapter}
                onChange={(e) => setFormData({ ...formData, chapter: e.target.value })}
              />
            </div>
            <div className="space-y-1 col-span-4">
              <Label htmlFor="edit-data">章节内容</Label>
              <Textarea
                id="edit-data"
                value={formData.chapterData}
                onChange={(e) =>
                  setFormData({ ...formData, chapterData: e.target.value })
                }
                rows={6}
              />
            </div>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button onClick={handleSubmit} disabled={loading}>
            {loading ? "保存中..." : "保存"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function OriginalText({ projectId }: { projectId: number }) {
  const [chapters, setChapters] = useState<Chapter[]>([]);
  const [loading, setLoading] = useState(true);
  const [addDialogOpen, setAddDialogOpen] = useState(false);
  const [editDialogOpen, setEditDialogOpen] = useState(false);
  const [editingChapter, setEditingChapter] = useState<Chapter | null>(null);

  const fetchChapters = useCallback(async () => {
    setLoading(true);
    try {
      const response = await dramaProjectApi.getNovel(projectId);
      if (response.code === 200) {
        setChapters(response.data || []);
      }
    } catch (error) {
      console.error("获取章节列表失败:", error);
    } finally {
      setLoading(false);
    }
  }, [projectId]);

  useState(() => {
    fetchChapters();
  });

  const handleAddChapters = async (newChapters: Chapter[]) => {
    try {
      const response = await dramaProjectApi.addNovel(projectId, newChapters);
      if (response.code === 200) {
        fetchChapters();
      }
    } catch (error) {
      console.error("添加章节失败:", error);
    }
  };

  const handleEditChapter = (chapter: Chapter) => {
    setEditingChapter(chapter);
    setEditDialogOpen(true);
  };

  const handleUpdateChapter = async (chapter: Chapter) => {
    try {
      const response = await dramaProjectApi.updateNovel(chapter);
      if (response.code === 200) {
        fetchChapters();
      }
    } catch (error) {
      console.error("更新章节失败:", error);
    }
  };

  const handleDeleteChapter = async (chapter: Chapter) => {
    if (!confirm("确定要删除这个章节吗？")) return;
    try {
      const response = await dramaProjectApi.delNovel(chapter.id);
      if (response.code === 200) {
        fetchChapters();
      }
    } catch (error) {
      console.error("删除章节失败:", error);
    }
  };

  return (
    <div className="space-y-6">
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <FileText className="h-5 w-5" />
            小说原文
          </CardTitle>
          <CardDescription>管理和查看小说章节内容</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="flex justify-end">
            <Button onClick={() => setAddDialogOpen(true)}>
              <Plus className="h-4 w-4 mr-2" />
              新增
            </Button>
          </div>

          <div className="border rounded-lg">
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[80px]">章</TableHead>
                  <TableHead className="w-[100px]">卷</TableHead>
                  <TableHead className="w-[200px]">章节名称</TableHead>
                  <TableHead>章节内容</TableHead>
                  <TableHead className="w-[120px]">操作</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {loading ? (
                  <TableRow>
                    <TableCell colSpan={5} className="text-center py-8">
                      加载中...
                    </TableCell>
                  </TableRow>
                ) : chapters.length === 0 ? (
                  <TableRow>
                    <TableCell colSpan={5} className="text-center py-8">
                      暂无章节，点击上方按钮添加
                    </TableCell>
                  </TableRow>
                ) : (
                  chapters.map((chapter) => (
                    <TableRow key={chapter.id}>
                      <TableCell className="font-medium">{chapter.index}</TableCell>
                      <TableCell>{chapter.reel || "-"}</TableCell>
                      <TableCell className="max-w-[200px] truncate">
                        {chapter.chapter}
                      </TableCell>
                      <TableCell className="max-w-[400px] truncate text-muted-foreground">
                        {chapter.chapterData}
                      </TableCell>
                      <TableCell>
                        <div className="flex gap-2">
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => handleEditChapter(chapter)}
                          >
                            <Pencil className="h-4 w-4" />
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            onClick={() => handleDeleteChapter(chapter)}
                            className="text-destructive hover:text-destructive"
                          >
                            <Trash2 className="h-4 w-4" />
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))
                )}
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>

      <AddChapterDialog
        open={addDialogOpen}
        onOpenChange={setAddDialogOpen}
        onAdd={handleAddChapters}
      />

      <EditChapterDialog
        open={editDialogOpen}
        onOpenChange={setEditDialogOpen}
        chapter={editingChapter}
        onUpdate={handleUpdateChapter}
      />
    </div>
  );
}
