"use client";

import { useEffect, useState } from "react";
import { useRouter } from "next/navigation";
import { Search, ChevronLeft, ChevronRight } from "lucide-react";

import { TaskDetails } from "@/components/drama/tasks/task-details";
import { Button } from "@/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { dramaProjectApi } from "@/core/drama/api";
import { useDramaStore } from "@/core/drama/store";
import { cn } from "@/lib/utils";

interface TaskItem {
  id: number;
  taskClass: string;
  relatedObjects: string;
  model: string;
  describe: string;
  state: string;
  startTime: number;
}

const TASK_CLASSES = [
  { value: "all", label: "全部" },
  { value: "outline", label: "大纲生成" },
  { value: "script", label: "剧本生成" },
  { value: "storyboard", label: "分镜生成" },
  { value: "video", label: "视频生成" },
  { value: "asset", label: "素材生成" },
];

const STATE_OPTIONS = [
  { value: "all", label: "全部" },
  { value: "pending", label: "等待中" },
  { value: "running", label: "进行中" },
  { value: "completed", label: "已完成" },
  { value: "failed", label: "失败" },
];

export default function DramaTasksPage() {
  const router = useRouter();
  const currentProject = useDramaStore((s) => s.currentProject);

  const [tasks, setTasks] = useState<TaskItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const [totalPages, setTotalPages] = useState(1);
  const [taskClass, setTaskClass] = useState("all");
  const [state, setState] = useState("all");
  const [selectedTask, setSelectedTask] = useState<TaskItem | null>(null);
  const [detailsOpen, setDetailsOpen] = useState(false);

  const limit = 10;

  useEffect(() => {
    const fetchTasks = async () => {
      if (!currentProject) {
        setLoading(false);
        return;
      }
      try {
        const response = await dramaProjectApi.getTaskList({
          page,
          limit,
          taskClass,
          state,
          projectId: currentProject.id,
        });
        if (response.code === 200) {
          const list = response.data?.data ?? [];
          const total = response.data?.total ?? 0;
          setTasks(
            list.map((t: Record<string, unknown>) => ({
              id: t.id as number,
              taskClass: t.task_type as string,
              relatedObjects: (t.result as Record<string, unknown>)?.name as string ?? "-",
              model: "-",
              describe: (t.result as Record<string, unknown>)?.prompt as string ?? "-",
              state: t.status as string,
              startTime: t.start_time
                ? new Date(t.start_time as string).getTime()
                : Date.now(),
            }))
          );
          setTotalPages(Math.ceil(total / limit) || 1);
        }
      } catch (error) {
        console.error("获取任务列表失败:", error);
      } finally {
        setLoading(false);
      }
    };

    void fetchTasks();
  }, [page, taskClass, state, currentProject]);

  const handleTaskClick = (task: TaskItem) => {
    setSelectedTask(task);
    setDetailsOpen(true);
  };

  return (
    <div className="max-w-5xl mx-auto p-8">
      <div className="flex items-center justify-between mb-6">
        <h1 className="text-2xl font-bold">任务中心</h1>
      </div>

      {/* 筛选栏 */}
      <Card className="mb-6">
        <CardContent className="pt-6">
          <div className="flex flex-wrap gap-4">
            <div className="flex items-center gap-2">
              <span className="text-sm text-muted-foreground">任务类型:</span>
              <Select value={taskClass} onValueChange={setTaskClass}>
                <SelectTrigger className="w-[140px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {TASK_CLASSES.map((c) => (
                    <SelectItem key={c.value} value={c.value}>
                      {c.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-sm text-muted-foreground">状态:</span>
              <Select value={state} onValueChange={setState}>
                <SelectTrigger className="w-[140px]">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {STATE_OPTIONS.map((s) => (
                    <SelectItem key={s.value} value={s.value}>
                      {s.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          </div>
        </CardContent>
      </Card>

      {/* 任务列表 */}
      <Card>
        <CardHeader>
          <CardTitle className="text-base">任务列表</CardTitle>
        </CardHeader>
        <CardContent>
          {loading ? (
            <div className="flex items-center justify-center py-8">
              <div className="text-muted-foreground">加载中...</div>
            </div>
          ) : tasks.length === 0 ? (
            <div className="flex flex-col items-center justify-center py-8 text-center">
              <div className="text-muted-foreground mb-2">暂无任务</div>
            </div>
          ) : (
            <>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>任务类型</TableHead>
                    <TableHead>关联对象</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>开始时间</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {tasks.map((task) => (
                    <TableRow
                      key={task.id}
                      className="cursor-pointer hover:bg-muted/50"
                      onClick={() => handleTaskClick(task)}
                    >
                      <TableCell className="font-medium">
                        {TASK_CLASSES.find((c) => c.value === task.taskClass)?.label ?? task.taskClass}
                      </TableCell>
                      <TableCell>{task.relatedObjects}</TableCell>
                      <TableCell>
                        <span
                          className={cn(
                            "px-2 py-0.5 rounded-full text-xs",
                            task.state === "completed" && "bg-green-100 text-green-700",
                            task.state === "running" && "bg-blue-100 text-blue-700",
                            task.state === "failed" && "bg-red-100 text-red-700",
                            task.state === "pending" && "bg-yellow-100 text-yellow-700"
                          )}
                        >
                          {STATE_OPTIONS.find((s) => s.value === task.state)?.label ?? task.state}
                        </span>
                      </TableCell>
                      <TableCell className="text-muted-foreground text-sm">
                        {new Date(task.startTime).toLocaleString("zh-CN")}
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>

              {/* 分页 */}
              {totalPages > 1 && (
                <div className="flex items-center justify-between mt-4">
                  <span className="text-sm text-muted-foreground">
                    第 {page} / {totalPages} 页
                  </span>
                  <div className="flex items-center gap-2">
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setPage((p) => Math.max(1, p - 1))}
                      disabled={page <= 1}
                    >
                      <ChevronLeft className="h-4 w-4" />
                    </Button>
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
                      disabled={page >= totalPages}
                    >
                      <ChevronRight className="h-4 w-4" />
                    </Button>
                  </div>
                </div>
              )}
            </>
          )}
        </CardContent>
      </Card>

      <TaskDetails
        open={detailsOpen}
        onOpenChange={setDetailsOpen}
        task={selectedTask}
      />
    </div>
  );
}
