"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

interface CreateProjectDialogProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onCreate: (data: {
    name: string;
    intro: string;
    type: string;
    art_style: string;
    video_ratio: string;
  }) => Promise<void>;
  loading?: boolean;
}

export function CreateProjectDialog({
  open,
  onOpenChange,
  onCreate,
  loading = false,
}: CreateProjectDialogProps) {
  const [formData, setFormData] = useState({
    name: "",
    intro: "",
    type: "短剧",
    art_style: "2D动漫风格",
    video_ratio: "16:9",
  });

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    await onCreate(formData);
    // 重置表单
    setFormData({
      name: "",
      intro: "",
      type: "短剧",
      art_style: "2D动漫风格",
      video_ratio: "16:9",
    });
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-[500px]">
        <DialogHeader>
          <DialogTitle>新建项目</DialogTitle>
          <DialogDescription>
            创建一个新的短剧项目，开始AI创作之旅
          </DialogDescription>
        </DialogHeader>
        <form onSubmit={handleSubmit}>
          <div className="grid gap-4 py-4">
            <div className="grid gap-2">
              <Label htmlFor="name">项目名称</Label>
              <Input
                id="name"
                placeholder="输入项目名称"
                value={formData.name}
                onChange={(e) =>
                  setFormData({ ...formData, name: e.target.value })
                }
                required
              />
            </div>

            <div className="grid gap-2">
              <Label htmlFor="type">小说类型</Label>
              <Input
                id="type"
                placeholder="例如：玄幻、科幻、言情"
                value={formData.type}
                onChange={(e) =>
                  setFormData({ ...formData, type: e.target.value })
                }
              />
            </div>

            <div className="grid gap-2">
              <Label htmlFor="art_style">影片画风</Label>
              <Select
                value={formData.art_style}
                onValueChange={(value) =>
                  setFormData({ ...formData, art_style: value })
                }
              >
                <SelectTrigger id="art_style">
                  <SelectValue placeholder="选择影片画风" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="2D动漫风格">2D动漫风格</SelectItem>
                  <SelectItem value="3D动画风格">3D动画风格</SelectItem>
                  <SelectItem value="写实风格">写实风格</SelectItem>
                  <SelectItem value="水墨风格">水墨风格</SelectItem>
                  <SelectItem value="扁平化风格">扁平化风格</SelectItem>
                </SelectContent>
              </Select>
            </div>

            <div className="grid gap-2">
              <Label htmlFor="video_ratio">影片比例</Label>
              <Select
                value={formData.video_ratio}
                onValueChange={(value) =>
                  setFormData({ ...formData, video_ratio: value })
                }
              >
                <SelectTrigger id="video_ratio">
                  <SelectValue placeholder="选择影片比例" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="16:9">16:9 (横屏)</SelectItem>
                  <SelectItem value="9:16">9:16 (竖屏)</SelectItem>
                  <SelectItem value="1:1">1:1 (方形)</SelectItem>
                  <SelectItem value="4:3">4:3 (传统)</SelectItem>
                </SelectContent>
              </Select>
            </div>

            <div className="grid gap-2">
              <Label htmlFor="intro">小说简介</Label>
              <Textarea
                id="intro"
                placeholder="输入小说简介..."
                value={formData.intro}
                onChange={(e) =>
                  setFormData({ ...formData, intro: e.target.value })
                }
                rows={3}
              />
            </div>
          </div>

          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => onOpenChange(false)}
              disabled={loading}
            >
              取消
            </Button>
            <Button type="submit" disabled={loading || !formData.name}>
              {loading ? "创建中..." : "创建"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
