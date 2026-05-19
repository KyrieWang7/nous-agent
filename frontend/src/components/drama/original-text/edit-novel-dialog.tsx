"use client";

import { useState } from "react";
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
import { dramaNovelApi } from "@/core/drama/api";

interface NovelRow {
  id: number;
  index: number;
  reel: string;
  chapter: string;
  chapterData: string;
  chapter_index: number;
}

interface EditNovelDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  novel: NovelRow;
  onSuccess: () => void;
}

export function EditNovelDialog({
  open,
  onOpenChange,
  novel,
  onSuccess,
}: EditNovelDialogProps) {
  const [chapter, setChapter] = useState(novel.chapter || "");
  const [chapterData, setChapterData] = useState(novel.chapterData || "");
  const [loading, setLoading] = useState(false);

  const handleSubmit = async () => {
    if (!chapterData.trim()) {
      toast.warning("请输入章节内容");
      return;
    }

    setLoading(true);
    try {
      // Parse reel from chapter if it exists
      let reel = novel.reel;
      let chapterName = chapter;

      // Try to extract reel and chapter from the combined chapter field
      if (chapter.includes("卷") && chapter.includes("章")) {
        const reelMatch = chapter.match(/^(第[^\s章]+卷)/);
        const chapterMatch = chapter.match(/第[^\s章]+章\s*(.+)/);
        if (reelMatch && reelMatch[1]) reel = reelMatch[1];
        if (chapterMatch && chapterMatch[1]) chapterName = chapterMatch[1];
      }

      const fullChapter = `${reel} ${chapter}`;

      const response = await dramaNovelApi.updateNovel(novel.id, {
        chapter: fullChapter,
        chapter_data: chapterData,
      });

      if (response.code === 200) {
        toast.success("更新成功");
        onSuccess();
      }
    } catch (error) {
      console.error("更新失败:", error);
      toast.error("更新失败");
    } finally {
      setLoading(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>编辑章节</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div>
              <label className="text-sm font-medium mb-2 block">卷</label>
              <Input value={novel.reel} disabled />
            </div>
            <div>
              <label className="text-sm font-medium mb-2 block">章节序号</label>
              <Input value={novel.index} disabled />
            </div>
          </div>

          <div>
            <label className="text-sm font-medium mb-2 block">章节名称</label>
            <Input
              value={chapter}
              onChange={(e) => setChapter(e.target.value)}
              placeholder="请输入章节名称"
            />
          </div>

          <div>
            <label className="text-sm font-medium mb-2 block">章节内容</label>
            <Textarea
              value={chapterData}
              onChange={(e) => setChapterData(e.target.value)}
              placeholder="请输入章节内容..."
              className="min-h-[300px] font-mono text-sm"
            />
            <div className="text-xs text-muted-foreground mt-1 text-right">
              {chapterData.length} 字符
            </div>
          </div>

          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => onOpenChange(false)}>
              取消
            </Button>
            <Button onClick={handleSubmit} disabled={loading}>
              {loading ? "保存中..." : "保存"}
            </Button>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
