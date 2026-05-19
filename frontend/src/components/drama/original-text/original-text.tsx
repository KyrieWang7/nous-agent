"use client";

import { useEffect, useState } from "react";
import { FileText, Plus, Pencil, Trash2 } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { dramaNovelApi } from "@/core/drama/api";
import type { DramaNovel } from "@/core/drama/api";
import { AddNovelDialog } from "./add-novel-dialog";
import { EditNovelDialog } from "./edit-novel-dialog";

interface NovelRow {
  id: number;
  index: number;
  reel: string;
  chapter: string;
  chapterData: string;
  chapter_index: number;
}

export function OriginalText({ projectId }: { projectId: number }) {
  const [novels, setNovels] = useState<NovelRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [addDialogOpen, setAddDialogOpen] = useState(false);
  const [editDialogOpen, setEditDialogOpen] = useState(false);
  const [editingNovel, setEditingNovel] = useState<NovelRow | null>(null);

  const fetchNovels = async () => {
    try {
      const response = await dramaNovelApi.getNovels(projectId);
      if (response.code === 200) {
        // Transform data to match our interface
        const data = response.data || [];
        setNovels(
          data.map((item: Record<string, unknown>) => ({
            id: item.id as number,
            index: item.chapter_index as number,
            reel: extractReel(item.chapter as string),
            chapter: extractChapterName(item.chapter as string),
            chapterData: item.chapter_data as string,
            chapter_index: item.chapter_index as number,
          }))
        );
      }
    } catch (error) {
      console.error("获取小说列表失败:", error);
      toast.error("获取小说列表失败");
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    fetchNovels();
  }, [projectId]);

  const handleDelete = async (id: number) => {
    try {
      const response = await dramaNovelApi.deleteNovel(id);
      if (response.code === 200) {
        toast.success("删除成功");
        fetchNovels();
      }
    } catch (error) {
      console.error("删除失败:", error);
      toast.error("删除失败");
    }
  };

  const handleEdit = (novel: NovelRow) => {
    setEditingNovel(novel);
    setEditDialogOpen(true);
  };

  const handleAddSuccess = () => {
    setAddDialogOpen(false);
    fetchNovels();
  };

  const handleEditSuccess = () => {
    setEditDialogOpen(false);
    setEditingNovel(null);
    fetchNovels();
  };

  if (loading) {
    return (
      <div className="flex items-center justify-center py-12">
        <div className="text-muted-foreground">加载中...</div>
      </div>
    );
  }

  return (
    <div className="novel-list">
      {/* Header */}
      <div className="mb-6">
        <h2 className="text-xl font-semibold mb-1">小说原文</h2>
        <p className="text-sm text-muted-foreground">查看和管理小说章节</p>
      </div>

      {/* Action Bar */}
      <div className="flex items-center justify-between p-4 bg-muted/50 rounded-lg mb-4">
        <div className="flex items-center gap-3">
          <FileText className="h-6 w-6 text-primary" />
          <span className="font-medium">原文管理</span>
        </div>
        <Button onClick={() => setAddDialogOpen(true)}>
          <Plus className="h-4 w-4 mr-2" />
          新增
        </Button>
      </div>

      {/* Novel Table */}
      {novels.length === 0 ? (
        <div className="text-center py-12 text-muted-foreground">
          <FileText className="h-12 w-12 mx-auto mb-4 opacity-50" />
          <p>暂无小说章节</p>
          <p className="text-sm">点击上方"新增"按钮导入小说</p>
        </div>
      ) : (
        <div className="border rounded-lg overflow-hidden">
          <div className="overflow-x-auto">
            <table className="w-full">
              <thead className="bg-muted/50">
                <tr>
                  <th className="px-4 py-3 text-left text-sm font-medium text-muted-foreground w-24">
                    章
                  </th>
                  <th className="px-4 py-3 text-left text-sm font-medium text-muted-foreground w-24">
                    卷
                  </th>
                  <th className="px-4 py-3 text-left text-sm font-medium text-muted-foreground">
                    章节名称
                  </th>
                  <th className="px-4 py-3 text-left text-sm font-medium text-muted-foreground">
                    内容预览
                  </th>
                  <th className="px-4 py-3 text-center text-sm font-medium text-muted-foreground w-24">
                    操作
                  </th>
                </tr>
              </thead>
              <tbody className="divide-y">
                {novels.map((novel) => (
                  <tr key={novel.id} className="hover:bg-muted/30">
                    <td className="px-4 py-3 text-sm">{novel.index}</td>
                    <td className="px-4 py-3 text-sm">{novel.reel}</td>
                    <td className="px-4 py-3 text-sm max-w-xs truncate">
                      {novel.chapter || "（无标题）"}
                    </td>
                    <td className="px-4 py-3 text-sm text-muted-foreground max-w-md truncate">
                      {novel.chapterData}
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center justify-center gap-2">
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-8 w-8"
                          onClick={() => handleEdit(novel)}
                        >
                          <Pencil className="h-4 w-4" />
                        </Button>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="h-8 w-8 text-destructive hover:text-destructive"
                          onClick={() => handleDelete(novel.id)}
                        >
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {/* Add Dialog */}
      <AddNovelDialog
        open={addDialogOpen}
        onOpenChange={setAddDialogOpen}
        projectId={projectId}
        onSuccess={handleAddSuccess}
      />

      {/* Edit Dialog */}
      {editingNovel && (
        <EditNovelDialog
          open={editDialogOpen}
          onOpenChange={setEditDialogOpen}
          novel={editingNovel}
          onSuccess={handleEditSuccess}
        />
      )}
    </div>
  );
}

// Helper functions to parse chapter field
function extractReel(chapter: string): string {
  const match = chapter.match(/^(第[^\s章]+卷)/);
  return match && match[1] ? match[1] : "正文卷";
}

function extractChapterName(chapter: string): string {
  const match = chapter.match(/第[^\s章]+章\s*(.+)/);
  return match && match[1] ? match[1] : chapter;
}
