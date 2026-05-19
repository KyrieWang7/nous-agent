"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { AlertTriangle, LogOut } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Alert, AlertDescription } from "@/components/ui/alert";

export function LogoutConfig() {
  const router = useRouter();
  const [loading, setLoading] = useState(false);

  const handleLogout = async () => {
    setLoading(true);
    try {
      // 清除本地存储的token
      localStorage.removeItem("token");
      localStorage.removeItem("user");

      // 跳转到登录页面
      router.push("/login");
    } catch {
      console.error("退出登录失败");
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="space-y-6">
      <div>
        <h2 className="text-2xl font-semibold mb-2">退出登录</h2>
        <p className="text-muted-foreground">安全退出当前账户</p>
      </div>

      <Alert variant="default" className="border-yellow-500/50 bg-yellow-50 dark:bg-yellow-950">
        <AlertTriangle className="h-4 w-4 text-yellow-600 dark:text-yellow-400" />
        <AlertDescription className="text-yellow-800 dark:text-yellow-200">
          退出登录后，您需要重新登录才能继续使用系统。
        </AlertDescription>
      </Alert>

      <Button
        variant="destructive"
        onClick={handleLogout}
        disabled={loading}
        className="gap-2"
      >
        <LogOut className="h-4 w-4" />
        {loading ? "退出中..." : "退出登录"}
      </Button>
    </div>
  );
}
