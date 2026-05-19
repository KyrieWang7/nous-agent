"use client";

import { useState } from "react";
import { Database, RefreshCw, Trash2 } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";

export function DBConfig() {
  const [loading, setLoading] = useState(false);
  const [operation, setOperation] = useState("");

  const handleOperation = async (op: string) => {
    setLoading(true);
    setOperation(op);
    try {
      // Simulated operations - in real implementation, call backend API
      await new Promise((resolve) => setTimeout(resolve, 1000));
      alert(`${op} 操作完成`);
    } catch (error) {
      console.error("操作失败:", error);
      alert("操作失败，请重试");
    } finally {
      setLoading(false);
      setOperation("");
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold mb-2">数据库操作</h2>
        <p className="text-muted-foreground">数据库维护和管理操作</p>
      </div>

      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <Database className="h-5 w-5" />
            数据库管理
          </CardTitle>
          <CardDescription>
            执行数据库维护操作，请谨慎使用
          </CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <Alert>
            <AlertDescription>
              这些操作会直接影响数据库，请确认后再执行。
            </AlertDescription>
          </Alert>

          <div className="space-y-3">
            <Button
              variant="outline"
              onClick={() => handleOperation("清理缓存")}
              disabled={loading}
              className="w-full justify-start gap-2"
            >
              <RefreshCw className="h-4 w-4" />
              {loading && operation === "清理缓存" ? "清理中..." : "清理缓存"}
            </Button>

            <Button
              variant="outline"
              onClick={() => handleOperation("优化数据库")}
              disabled={loading}
              className="w-full justify-start gap-2"
            >
              <Database className="h-4 w-4" />
              {loading && operation === "优化数据库" ? "优化中..." : "优化数据库"}
            </Button>

            <Button
              variant="destructive"
              onClick={() => {
                if (confirm("确定要清空所有对话历史吗？此操作不可恢复！")) {
                  handleOperation("清空历史");
                }
              }}
              disabled={loading}
              className="w-full justify-start gap-2"
            >
              <Trash2 className="h-4 w-4" />
              {loading && operation === "清空历史" ? "清空中..." : "清空对话历史"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
