"use client";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";

interface TaskItem {
  id: number;
  taskClass: string;
  relatedObjects: string;
  model: string;
  describe: string;
  state: string;
  startTime: number;
}

interface TaskDetailsProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  task: TaskItem | null;
}

export function TaskDetails({ open, onOpenChange, task }: TaskDetailsProps) {
  if (!task) return null;

  const formatTime = (timestamp: number) => {
    return new Date(timestamp).toLocaleString("zh-CN");
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>任务详情</DialogTitle>
          <DialogDescription>查看任务的详细信息</DialogDescription>
        </DialogHeader>
        <div className="grid grid-cols-2 gap-4 py-4">
          <div className="space-y-1">
            <Label className="text-muted-foreground text-xs">任务大类</Label>
            <p className="font-medium">{task.taskClass}</p>
          </div>
          <div className="space-y-1">
            <Label className="text-muted-foreground text-xs">关联对象</Label>
            <p className="font-medium">{task.relatedObjects}</p>
          </div>
          <div className="space-y-1">
            <Label className="text-muted-foreground text-xs">模型</Label>
            <p className="font-medium">{task.model}</p>
          </div>
          <div className="space-y-1">
            <Label className="text-muted-foreground text-xs">状态</Label>
            <p className="font-medium">{task.state}</p>
          </div>
          <div className="space-y-1">
            <Label className="text-muted-foreground text-xs">开始时间</Label>
            <p className="font-medium">{formatTime(task.startTime)}</p>
          </div>
          <div className="col-span-2 space-y-1">
            <Label className="text-muted-foreground text-xs">描述</Label>
            <p className="font-medium whitespace-pre-wrap">{task.describe}</p>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
