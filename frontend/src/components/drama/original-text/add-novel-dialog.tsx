"use client";

import { useCallback, useState, useRef, useMemo } from "react";
import { Upload } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { dramaNovelApi } from "@/core/drama/api";
import { parseNovel, type ParsedChapter, type ParsedReel } from "@/lib/drama/parse-novel";

interface AddNovelDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  projectId: number;
  onSuccess: () => void;
}

export function AddNovelDialog({
  open,
  onOpenChange,
  projectId,
  onSuccess,
}: AddNovelDialogProps) {
  const [activeTab, setActiveTab] = useState("step1");
  const [content, setContent] = useState("");
  const [selectedKeys, setSelectedKeys] = useState<Set<number>>(new Set());
  const fileInputRef = useRef<HTMLInputElement>(null);

  // Parse chapters from content using useMemo
  const parsedReels: ParsedReel[] = useMemo(() => {
    if (!content) return [];
    try {
      return parseNovel(content);
    } catch {
      return [];
    }
  }, [content]);

  const allChapters: ParsedChapter[] = useMemo(
    () => parsedReels.flatMap((reel) => reel.chapters),
    [parsedReels]
  );

  const selectedChapters = useMemo(
    () => allChapters.filter((ch) => selectedKeys.has(ch.index)),
    [allChapters, selectedKeys]
  );

  const selectedTextLength = useMemo(
    () => selectedChapters.reduce((sum, ch) => sum + ch.text.length, 0),
    [selectedChapters]
  );

  const handleFileUpload = useCallback(async (file: File) => {
    const allowedTypes = ["text/plain"];
    const maxSize = 10 * 1024 * 1024; // 10MB

    if (!allowedTypes.includes(file.type)) {
      toast.error("不支持的文件类型，请上传 .txt 文件");
      return;
    }

    if (file.size > maxSize) {
      toast.error("文件大小超过 10MB");
      return;
    }

    try {
      const text = await file.text();
      setContent(text);
      toast.success("文件解析成功");
    } catch {
      toast.error("文件解析失败");
    }
  }, []);

  const handleDrop = useCallback(
    (e: React.DragEvent) => {
      e.preventDefault();
      const files = e.dataTransfer?.files;
      if (files && files.length > 0 && files[0]) {
        handleFileUpload(files[0]);
      }
    },
    [handleFileUpload]
  );

  const handleFileSelect = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const files = e.target.files;
      if (files && files.length > 0 && files[0]) {
        handleFileUpload(files[0]);
      }
    },
    [handleFileUpload]
  );

  const toggleChapter = useCallback((index: number) => {
    setSelectedKeys((prev) => {
      const next = new Set(prev);
      if (next.has(index)) {
        next.delete(index);
      } else {
        next.add(index);
      }
      return next;
    });
  }, []);

  const toggleAllChapters = useCallback(() => {
    if (selectedKeys.size === allChapters.length) {
      setSelectedKeys(new Set());
    } else {
      setSelectedKeys(new Set(allChapters.map((ch) => ch.index)));
    }
  }, [allChapters, selectedKeys]);

  const handleSubmit = async () => {
    if (selectedChapters.length === 0) {
      toast.warning("请先选择章节");
      return;
    }

    try {
      // Transform to API format and save each chapter
      for (let i = 0; i < selectedChapters.length; i++) {
        const ch = selectedChapters[i];
        if (!ch) continue;
        // Find the parent reel
        const reel = parsedReels.find((r) =>
          r.chapters.some((c) => c.index === ch.index)
        );
        const chapterToSave = {
          chapter: `${reel?.reel || "正文卷"} 第${ch.index}章 ${ch.chapter}`,
          chapter_data: ch.text,
          chapter_index: i,
        };

        const response = await dramaNovelApi.createNovel(projectId, chapterToSave);
        if (response.code !== 200) {
          throw new Error("导入失败");
        }
      }

      toast.success("导入成功");
      resetAndClose();
      onSuccess();
    } catch (error) {
      console.error("导入失败:", error);
      toast.error("导入失败");
    }
  };

  const resetAndClose = () => {
    setContent("");
    setSelectedKeys(new Set());
    setActiveTab("step1");
    onOpenChange(false);
  };

  const handleNextStep = () => {
    if (!content) {
      toast.warning("请先上传文件或粘贴内容");
      return;
    }
    if (allChapters.length === 0) {
      toast.warning("无法解析章节，请检查内容格式");
      return;
    }
    setActiveTab("step2");
    // Select all chapters by default
    setSelectedKeys(new Set(allChapters.map((ch) => ch.index)));
  };

  return (
    <Dialog open={open} onOpenChange={resetAndClose}>
      <DialogContent className="max-w-3xl max-h-[85vh] overflow-hidden flex flex-col">
        <DialogHeader>
          <DialogTitle>上传小说原文</DialogTitle>
        </DialogHeader>

        <Tabs
          value={activeTab}
          onValueChange={setActiveTab}
          className="flex-1 overflow-hidden flex flex-col"
        >
          <TabsList className="grid w-full grid-cols-2">
            <TabsTrigger value="step1">第一步</TabsTrigger>
            <TabsTrigger value="step2" disabled={!content}>
              第二步
            </TabsTrigger>
          </TabsList>

          <TabsContent value="step1" className="flex-1 overflow-auto">
            <div className="space-y-4">
              {/* Upload Area */}
              <div
                className="border-2 border-dashed rounded-lg p-8 text-center cursor-pointer hover:border-primary/50 transition-colors"
                onClick={() => fileInputRef.current?.click()}
                onDragOver={(e) => e.preventDefault()}
                onDrop={handleDrop}
              >
                <input
                  ref={fileInputRef}
                  type="file"
                  accept=".txt"
                  className="hidden"
                  onChange={handleFileSelect}
                />
                <Upload className="h-8 w-8 mx-auto mb-3 text-muted-foreground" />
                <p className="text-sm font-medium mb-1">
                  拖拽小说原文文件到此处或点击上传
                </p>
                <p className="text-xs text-muted-foreground">
                  支持 .txt 格式，建议文件大小不超过 10MB
                </p>
              </div>

              {/* Divider */}
              <div className="relative">
                <div className="absolute inset-0 flex items-center">
                  <span className="w-full border-t" />
                </div>
                <div className="relative flex justify-center text-xs uppercase">
                  <span className="bg-background px-2 text-muted-foreground">或</span>
                </div>
              </div>

              {/* Paste Content */}
              <div>
                <label className="text-sm font-medium mb-2 block">
                  直接粘贴小说原文内容
                </label>
                <Textarea
                  value={content}
                  onChange={(e) => setContent(e.target.value)}
                  placeholder="请输入小说原文内容..."
                  className="min-h-[200px] font-mono text-sm"
                />
                <div className="flex justify-between items-center mt-2 text-xs text-muted-foreground">
                  <span>
                    {content.length} 字符
                    {content.length > 0 && content.length < 100 && (
                      <span className="text-yellow-600 ml-2">内容过短，建议至少100字符</span>
                    )}
                  </span>
                  <span>已解析 {allChapters.length} 章节</span>
                </div>
              </div>

              {/* Next Button */}
              <div className="flex justify-end">
                <Button onClick={handleNextStep} disabled={!content || allChapters.length === 0}>
                  下一步
                </Button>
              </div>
            </div>
          </TabsContent>

          <TabsContent value="step2" className="flex-1 overflow-auto">
            <div className="space-y-4">
              {/* Chapter List */}
              <div className="border rounded-lg overflow-hidden">
                <div className="bg-muted/50 px-4 py-2 flex items-center gap-2">
                  <input
                    type="checkbox"
                    checked={selectedKeys.size === allChapters.length && allChapters.length > 0}
                    onChange={toggleAllChapters}
                    className="rounded"
                  />
                  <span className="text-sm">全选</span>
                  <span className="text-sm text-muted-foreground ml-2">
                    (已选 {selectedChapters.length} 章节)
                  </span>
                </div>
                <div className="max-h-[400px] overflow-y-auto">
                  {parsedReels.map((reel) => (
                    <div key={reel.reel}>
                      <div className="bg-muted/30 px-4 py-1.5 text-xs font-medium text-muted-foreground sticky top-0">
                        {reel.reel}（{reel.chapters.length} 章）
                      </div>
                      {reel.chapters.map((chapter) => (
                        <div
                          key={chapter.index}
                          className="px-4 py-2 hover:bg-muted/30 flex items-start gap-3 border-t"
                        >
                          <input
                            type="checkbox"
                            checked={selectedKeys.has(chapter.index)}
                            onChange={() => toggleChapter(chapter.index)}
                            className="mt-1 rounded"
                          />
                          <div className="flex-1 min-w-0">
                            <div className="text-sm font-medium">
                              第{chapter.index}章 {chapter.chapter || "（无标题）"}
                            </div>
                            <div className="text-xs text-muted-foreground line-clamp-2 mt-0.5">
                              {chapter.text.substring(0, 100)}
                              {chapter.text.length > 100 && "..."}
                            </div>
                          </div>
                        </div>
                      ))}
                    </div>
                  ))}
                </div>
              </div>

              {/* Selected Info */}
              <div className="text-sm text-muted-foreground">
                已勾选：{selectedTextLength.toLocaleString()} 字
                {selectedTextLength > 200000 && (
                  <span className="text-destructive ml-2">（超过20万字限制）</span>
                )}
              </div>

              {/* Actions */}
              <div className="flex justify-between">
                <Button variant="outline" onClick={() => setActiveTab("step1")}>
                  上一步
                </Button>
                <Button
                  onClick={handleSubmit}
                  disabled={selectedChapters.length === 0 || selectedTextLength > 200000}
                >
                  保存
                </Button>
              </div>
            </div>
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}
